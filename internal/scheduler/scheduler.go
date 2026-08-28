package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MengStar-L/sub2api-auto5h/internal/store"
	"github.com/MengStar-L/sub2api-auto5h/internal/sub2api"
)

type Remote interface {
	Accounts(context.Context) ([]sub2api.Account, error)
	Quota(context.Context, int64) (sub2api.Quota, error)
	Models(context.Context, int64) ([]string, error)
	TestAccount(context.Context, int64, string) (sub2api.TestResult, error)
}

type Factory func(store.Settings) (Remote, error)

type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                             { return time.Now() }
func (realClock) After(delay time.Duration) <-chan time.Time { return time.After(delay) }

type Status struct {
	Running       bool   `json:"running"`
	GlobalPause   bool   `json:"global_pause"`
	PauseReason   string `json:"pause_reason,omitempty"`
	ActiveWorkers int32  `json:"active_workers"`
	LastSyncAt    int64  `json:"last_sync_at,omitempty"`
}

type Scheduler struct {
	store       *store.Store
	factory     Factory
	clock       Clock
	log         *slog.Logger
	wake        chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	locksMu     sync.Mutex
	locks       map[string]*sync.Mutex
	active      atomic.Int32
	running     atomic.Bool
	paused      atomic.Bool
	pauseMu     sync.RWMutex
	pauseReason string
	lastSync    atomic.Int64
}

func New(data *store.Store, factory Factory, logger *slog.Logger) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		store: data, factory: factory, clock: realClock{}, log: logger,
		wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel, locks: make(map[string]*sync.Mutex),
	}
}

func (s *Scheduler) Start() {
	if !s.running.CompareAndSwap(false, true) {
		return
	}
	s.wg.Add(1)
	go s.loop()
}

func (s *Scheduler) Stop() {
	s.cancel()
	s.wg.Wait()
	s.running.Store(false)
}

func (s *Scheduler) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) Status() Status {
	s.pauseMu.RLock()
	reason := s.pauseReason
	s.pauseMu.RUnlock()
	return Status{Running: s.running.Load(), GlobalPause: s.paused.Load(), PauseReason: reason, ActiveWorkers: s.active.Load(), LastSyncAt: s.lastSync.Load()}
}

func (s *Scheduler) setPause(reason string) {
	s.pauseMu.Lock()
	s.pauseReason = reason
	s.pauseMu.Unlock()
	s.paused.Store(reason != "")
}

func (s *Scheduler) loop() {
	defer s.wg.Done()
	nextSync := time.Time{}
	for {
		settings, err := s.store.GetSettings(s.ctx)
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			case <-s.wake:
			case <-s.clock.After(5 * time.Second):
			}
			continue
		}
		now := s.clock.Now()
		if nextSync.IsZero() || !now.Before(nextSync) {
			if err := s.Sync(s.ctx); err != nil {
				s.log.Warn("inventory sync failed", "error", err)
			} else {
				nextSync = now.Add(time.Duration(settings.SyncIntervalSeconds) * time.Second)
			}
		}
		if !s.paused.Load() {
			ids, err := s.store.ListDueAccountIDs(s.ctx, now.Unix(), 100)
			if err != nil {
				s.log.Error("load due accounts", "error", err)
			} else {
				for _, id := range ids {
					s.dispatchWorker(id, settings.MaxConcurrency)
				}
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-s.clock.After(5 * time.Second):
		}
	}
}

func (s *Scheduler) dispatchWorker(id string, limit int) {
	for {
		current := s.active.Load()
		if int(current) >= limit || int(current) >= 16 {
			return
		}
		if s.active.CompareAndSwap(current, current+1) {
			break
		}
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.active.Add(-1)
		if err := s.ProcessAccount(s.ctx, id); err != nil && !errors.Is(err, context.Canceled) {
			s.log.Warn("account processing failed", "account_id", id, "error", err)
		}
	}()
}

func (s *Scheduler) accountLock(id string) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	lock := s.locks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[id] = lock
	}
	return lock
}

func DefaultFactory(settings store.Settings) (Remote, error) {
	return sub2api.NewClient(settings.BaseURL, settings.APIKey, settings.AllowPrivateHTTP, time.Duration(settings.RequestTimeoutSeconds)*time.Second)
}

func (s *Scheduler) Sync(ctx context.Context) error {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return err
	}
	client, err := s.factory(settings)
	if err != nil {
		return err
	}
	accounts, err := client.Accounts(ctx)
	if err != nil {
		s.handleGlobalError(ctx, err)
		return err
	}
	now := s.clock.Now()
	inputs := make([]store.RemoteAccountInput, 0, len(accounts))
	for _, account := range accounts {
		plan := sub2api.CredentialString(account, "plan_type", "account_plan", "subscription_type")
		email := sub2api.CredentialString(account, "email")
		eligible, reason := Classify(account, plan, now)
		inputs = append(inputs, store.RemoteAccountInput{
			RemoteID: account.ID, RemoteCreatedAt: account.CreatedAt, IdentityHash: sub2api.AccountIdentityHash(account),
			Name: account.Name, Email: email, PlanType: plan, Platform: account.Platform, AccountType: account.Type,
			Status: account.Status, Schedulable: account.Schedulable, ParentAccountID: account.ParentAccountID,
			ExpiresAt: value(account.ExpiresAt), AutoPauseOnExpired: account.AutoPauseOnExpired,
			RateLimitResetAt: value(account.RateLimitResetAt), TempUnschedulableUntil: value(account.TempUnschedulableUntil),
			TempUnschedulableReason: account.TempUnschedulableReason, Eligible: eligible, EligibilityReason: reason,
		})
	}
	if err := s.store.ReplaceInventory(ctx, settings.ConnectionUUID, inputs); err != nil {
		return err
	}
	s.setPause("")
	s.lastSync.Store(now.Unix())
	_ = s.store.AddEvent(ctx, "info", "scheduler", "inventory_sync", "", fmt.Sprintf("已同步 %d 个 OpenAI OAuth 账号", len(inputs)), "{}")
	s.Wake()
	return nil
}

func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}

func Classify(account sub2api.Account, plan string, now time.Time) (bool, string) {
	if account.Platform != "openai" || account.Type != "oauth" {
		return false, "仅支持 OpenAI OAuth 账号"
	}
	if account.ParentAccountID != nil {
		return false, "影子账号不持有独立额度"
	}
	if account.Status != "active" {
		return false, "账号状态不是 active"
	}
	if !account.Schedulable {
		return false, "sub2api 已关闭账号调度"
	}
	if account.AutoPauseOnExpired && account.ExpiresAt != nil {
		if expires, err := time.Parse(time.RFC3339, *account.ExpiresAt); err == nil && !now.Before(expires) {
			return false, "账号已过期"
		}
	}
	if account.TempUnschedulableUntil != nil {
		if until, err := time.Parse(time.RFC3339, *account.TempUnschedulableUntil); err == nil && now.Before(until) && !quotaThresholdReason(account.TempUnschedulableReason) {
			return false, "账号因认证或传输故障临时暂停"
		}
	}
	normalized := strings.ToLower(strings.TrimSpace(plan))
	if normalized == "" {
		return false, "套餐未知，请刷新额度"
	}
	if strings.Contains(normalized, "enterprise") || normalized == "pro" || normalized == "chatgptpro" || strings.Contains(normalized, "free") {
		return false, "套餐不在 Plus/Team/Business 白名单"
	}
	if normalized == "plus" || normalized == "team" || strings.Contains(normalized, "business") {
		return true, ""
	}
	return false, "套餐不在 Plus/Team/Business 白名单"
}

func quotaThresholdReason(reason string) bool {
	var object struct {
		Source string `json:"source"`
	}
	return json.Unmarshal([]byte(reason), &object) == nil && object.Source == "account_scheduling_threshold"
}

func (s *Scheduler) ProcessAccount(ctx context.Context, id string) error {
	lock := s.accountLock(id)
	lock.Lock()
	defer lock.Unlock()

	account, err := s.store.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	if !account.Policy.Enabled || account.Missing {
		return nil
	}
	if !account.Eligible {
		return s.store.SetAccountRuntime(ctx, id, "paused", account.EligibilityReason, nil)
	}
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return err
	}
	client, err := s.factory(settings)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	cycle, cycleErr := s.store.DueCycle(ctx, id, now.Unix())
	quota, quotaErr := client.Quota(ctx, account.RemoteID)
	if quotaErr != nil {
		if cycleErr == nil && sub2api.IsTransient(quotaErr) && account.QuotaState == "valid" && now.Unix() >= cycle.DueAt+120 && !futureExhausted(account.SevenResetAt, account.SevenUsedPercent, now) {
			return s.sendAttempt(ctx, client, account, cycle, settings)
		}
		return s.recordQuotaError(ctx, account, quotaErr, now)
	}
	if delta := math.Abs(float64(quota.FetchedAt - now.Unix())); delta > 120 {
		message := fmt.Sprintf("sub2api 与本机时钟相差 %.0f 秒", delta)
		return s.store.SetAccountRuntime(ctx, id, "attention", message, nil)
	}
	identity := sub2api.QuotaIdentityHash(quota)
	if identity == "" {
		identity = account.IdentityHash
	}
	plan := quota.PlanType
	if plan == "" {
		plan = account.PlanType
	}
	eligible := eligiblePlan(plan)
	if !eligible {
		_ = s.store.SetEligibility(ctx, id, false, "额度接口返回的套餐不在白名单", plan)
		return s.store.SetAccountRuntime(ctx, id, "paused", "额度接口返回的套餐不在白名单", nil)
	}
	_ = s.store.SetEligibility(ctx, id, true, "", plan)
	if quota.SevenExhausted(now) {
		next := quota.SevenDay.ResetAt + int64(effectiveGrace(account.Policy, settings))
		changed, err := s.store.ApplyQuota(ctx, id, quotaUpdate(identity, plan, quota, &next, "blocked_7d", ""))
		if changed || err != nil {
			return err
		}
		return nil
	}
	if quota.FiveActive(now) {
		due := quota.FiveHour.ResetAt + int64(effectiveGrace(account.Policy, settings))
		if cycleErr == nil && (cycle.SourceResetAt == nil || *cycle.SourceResetAt != quota.FiveHour.ResetAt) {
			_ = s.store.MarkCycle(ctx, cycle.ID, "skipped_external", "检测到外部流量已启动新窗口", &due)
			_ = s.store.AddEvent(ctx, "info", "scheduler", "external_activation", id, "检测到外部流量已启动新 5h 窗口，已跳过", "{}")
		}
		changed, err := s.store.ApplyQuota(ctx, id, quotaUpdate(identity, plan, quota, &due, "waiting", ""))
		if changed || err != nil {
			return err
		}
		_, err = s.store.EnsureCycle(ctx, id, account.IdentityGeneration, fmt.Sprintf("reset:%d", quota.FiveHour.ResetAt), "reset", &quota.FiveHour.ResetAt, due)
		return err
	}
	ready := (quota.FiveHour != nil && quota.FiveHour.ResetAt <= now.Unix()) || quota.KnownIdle(now)
	if !ready {
		return s.store.SetAccountRuntime(ctx, id, "attention", "额度结构有效，但当前状态不允许启动 5h 窗口", nil)
	}
	dueNow := now.Unix()
	changed, err := s.store.ApplyQuota(ctx, id, quotaUpdate(identity, plan, quota, &dueNow, "due", ""))
	if changed || err != nil {
		return err
	}
	if errors.Is(cycleErr, store.ErrNotFound) {
		key := fmt.Sprintf("bootstrap:%d", account.Policy.EnableGeneration)
		cycle, err = s.store.EnsureCycle(ctx, id, account.IdentityGeneration, key, "bootstrap", nil, now.Unix())
		if err != nil {
			return err
		}
	} else if cycleErr != nil {
		return cycleErr
	}
	return s.sendAttempt(ctx, client, account, cycle, settings)
}

func quotaUpdate(identity, plan string, quota sub2api.Quota, next *int64, state, message string) store.QuotaUpdate {
	update := store.QuotaUpdate{IdentityHash: identity, PlanType: plan, FetchedAt: quota.FetchedAt, State: "valid", NextActionAt: next, RuntimeState: state, LastError: message}
	if quota.FiveHour != nil {
		update.FiveResetAt = &quota.FiveHour.ResetAt
		update.FiveUsedPercent = &quota.FiveHour.UsedPercent
	}
	if quota.SevenDay != nil {
		update.SevenResetAt = &quota.SevenDay.ResetAt
		update.SevenUsedPercent = &quota.SevenDay.UsedPercent
	}
	return update
}

func futureExhausted(reset *int64, used *float64, now time.Time) bool {
	return reset != nil && used != nil && *used >= 100 && *reset > now.Unix()
}

func eligiblePlan(plan string) bool {
	value := strings.ToLower(strings.TrimSpace(plan))
	if value == "" || strings.Contains(value, "enterprise") || value == "pro" || value == "chatgptpro" || strings.Contains(value, "free") {
		return false
	}
	return value == "plus" || value == "team" || strings.Contains(value, "business")
}

func effectiveGrace(policy store.Policy, settings store.Settings) int {
	if policy.GraceOverrideSeconds != nil {
		return *policy.GraceOverrideSeconds
	}
	return settings.ResetGraceSeconds
}

func effectiveRetries(policy store.Policy, settings store.Settings) int {
	if policy.MaxRetriesOverride != nil {
		return *policy.MaxRetriesOverride
	}
	return settings.MaxRetries
}

func effectiveRetryBase(policy store.Policy, settings store.Settings) int {
	if policy.RetryBaseOverrideSeconds != nil {
		return *policy.RetryBaseOverrideSeconds
	}
	return settings.RetryBaseSeconds
}

func effectiveModel(policy store.Policy, settings store.Settings) string {
	if policy.ModelOverride != nil && strings.TrimSpace(*policy.ModelOverride) != "" {
		return strings.TrimSpace(*policy.ModelOverride)
	}
	return settings.GlobalModel
}

func (s *Scheduler) sendAttempt(ctx context.Context, client Remote, account store.Account, cycle store.Cycle, settings store.Settings) error {
	now := s.clock.Now().Unix()
	claimed, ok, err := s.store.StartAttempt(ctx, cycle.ID, now, now+int64(settings.RequestTimeoutSeconds)+30)
	if err != nil || !ok {
		return err
	}
	model := effectiveModel(account.Policy, settings)
	attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(settings.RequestTimeoutSeconds)*time.Second)
	result, testErr := client.TestAccount(attemptCtx, account.RemoteID, model)
	cancel()
	ended := s.clock.Now().Unix()
	if testErr == nil && result.Success {
		next := ended + sub2api.FiveHoursSeconds + int64(effectiveGrace(account.Policy, settings))
		accepted := ended
		status := result.HTTPStatus
		if err := s.store.FinishAttempt(ctx, claimed, now, ended, store.AttemptResult{
			Outcome: "accepted", HTTPStatus: &status, Status: "success", NextAt: &next, AcceptedAt: &accepted, Reason: "test_complete success",
		}); err != nil {
			return err
		}
		_ = s.store.AddEvent(ctx, "info", "scheduler", "activation_accepted", account.ID, "激活请求已明确成功，等待额度验证", "{}")
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.verify(account.ID, claimed.ID, ended)
		}()
		return nil
	}

	statusCode, errorCode, message := errorDetails(testErr, result)
	reconcileCtx, cancelReconcile := context.WithTimeout(ctx, time.Duration(settings.RequestTimeoutSeconds)*time.Second)
	quota, quotaErr := client.Quota(reconcileCtx, account.RemoteID)
	cancelReconcile()
	if quotaErr == nil && quota.FiveHour != nil && quota.FiveHour.ResetAt > ended && (claimed.SourceResetAt == nil || quota.FiveHour.ResetAt != *claimed.SourceResetAt) {
		next := quota.FiveHour.ResetAt + int64(effectiveGrace(account.Policy, settings))
		accepted := ended
		if err := s.store.FinishAttempt(ctx, claimed, now, ended, store.AttemptResult{
			Outcome: "inferred_success", HTTPStatus: statusCode, ErrorCode: errorCode, Message: message,
			Status: "success_inferred", NextAt: &next, AcceptedAt: &accepted, Reason: "quota reset advanced after ambiguous attempt",
		}); err != nil {
			return err
		}
		_, err := s.store.EnsureCycle(ctx, account.ID, account.IdentityGeneration, fmt.Sprintf("reset:%d", quota.FiveHour.ResetAt), "reset", &quota.FiveHour.ResetAt, next)
		return err
	}
	if quotaErr != nil && !sub2api.IsTransient(quotaErr) {
		reconcileMessage := "请求结果不确定，且额度协调返回不可重试错误: " + quotaErr.Error()
		return s.store.FinishAttempt(ctx, claimed, now, ended, store.AttemptResult{
			Outcome: "attention", HTTPStatus: statusCode, ErrorCode: errorCode, Message: reconcileMessage,
			Status: "attention", Reason: reconcileMessage,
		})
	}
	var testAPIError *sub2api.APIError
	if errors.As(testErr, &testAPIError) && (testAPIError.Kind == sub2api.ErrorAuth || testAPIError.Kind == sub2api.ErrorCompliance || testAPIError.Kind == sub2api.ErrorNotFound || testAPIError.Kind == sub2api.ErrorSchema) {
		return s.store.FinishAttempt(ctx, claimed, now, ended, store.AttemptResult{
			Outcome: "attention", HTTPStatus: statusCode, ErrorCode: errorCode, Message: message,
			Status: "attention", Reason: "激活接口返回不可重试错误",
		})
	}
	maxAttempts := 1 + effectiveRetries(account.Policy, settings)
	if claimed.AttemptCount < maxAttempts {
		base := effectiveRetryBase(account.Policy, settings)
		delay := int64(base) << (claimed.AttemptCount - 1)
		if delay > 600 {
			delay = 600
		}
		next := ended + delay
		return s.store.FinishAttempt(ctx, claimed, now, ended, store.AttemptResult{
			Outcome: "retry", HTTPStatus: statusCode, ErrorCode: errorCode, Message: message,
			Status: "retry_wait", NextAt: &next, Reason: fmt.Sprintf("第 %d 次请求未确认，%d 秒后重试", claimed.AttemptCount, delay),
		})
	}
	return s.store.FinishAttempt(ctx, claimed, now, ended, store.AttemptResult{
		Outcome: "failed", HTTPStatus: statusCode, ErrorCode: errorCode, Message: message,
		Status: "attention", Reason: "已达到最大尝试次数",
	})
}

func errorDetails(err error, result sub2api.TestResult) (*int, string, string) {
	status := result.HTTPStatus
	var statusPtr *int
	if status != 0 {
		statusPtr = &status
	}
	if err == nil {
		return statusPtr, "TEST_FAILED", result.Message
	}
	var apiErr *sub2api.APIError
	if errors.As(err, &apiErr) {
		return statusPtr, apiErr.Code, apiErr.Message
	}
	return statusPtr, "REQUEST_ERROR", err.Error()
}

func (s *Scheduler) verify(accountID, cycleID string, acceptedAt int64) {
	for _, elapsed := range []time.Duration{10 * time.Second, 30 * time.Second, 60 * time.Second} {
		wait := time.Unix(acceptedAt, 0).Add(elapsed).Sub(s.clock.Now())
		if wait > 0 {
			select {
			case <-s.ctx.Done():
				return
			case <-s.clock.After(wait):
			}
		}
		account, err := s.store.GetAccount(s.ctx, accountID)
		if err != nil || !account.Policy.Enabled {
			return
		}
		settings, err := s.store.GetSettings(s.ctx)
		if err != nil {
			continue
		}
		client, err := s.factory(settings)
		if err != nil {
			continue
		}
		quota, err := client.Quota(s.ctx, account.RemoteID)
		if err != nil || quota.FiveHour == nil || quota.FiveHour.ResetAt <= acceptedAt {
			continue
		}
		due := quota.FiveHour.ResetAt + int64(effectiveGrace(account.Policy, settings))
		identity := sub2api.QuotaIdentityHash(quota)
		if identity == "" {
			identity = account.IdentityHash
		}
		changed, err := s.store.ApplyQuota(s.ctx, accountID, quotaUpdate(identity, quota.PlanType, quota, &due, "verified", ""))
		if changed || err != nil {
			return
		}
		_ = s.store.MarkCycle(s.ctx, cycleID, "verified", "额度窗口已验证", &due)
		_, _ = s.store.EnsureCycle(s.ctx, accountID, account.IdentityGeneration, fmt.Sprintf("reset:%d", quota.FiveHour.ResetAt), "reset", &quota.FiveHour.ResetAt, due)
		_ = s.store.AddEvent(s.ctx, "info", "scheduler", "activation_verified", accountID, "新 5h 窗口已验证", "{}")
		return
	}
}

func (s *Scheduler) recordQuotaError(ctx context.Context, account store.Account, quotaErr error, now time.Time) error {
	var apiErr *sub2api.APIError
	if errors.As(quotaErr, &apiErr) && (apiErr.Kind == sub2api.ErrorAuth || apiErr.Kind == sub2api.ErrorCompliance) {
		return s.store.SetAccountRuntime(ctx, account.ID, "paused", quotaErr.Error(), nil)
	}
	if sub2api.IsTransient(quotaErr) {
		next := now.Add(15 * time.Second).Unix()
		return s.store.SetAccountRuntime(ctx, account.ID, "quota_retry", quotaErr.Error(), &next)
	}
	if errors.As(quotaErr, &apiErr) && apiErr.Kind == sub2api.ErrorNotFound {
		return s.store.SetAccountRuntime(ctx, account.ID, "missing", quotaErr.Error(), nil)
	}
	return s.store.SetAccountRuntime(ctx, account.ID, "attention", quotaErr.Error(), nil)
}

func (s *Scheduler) handleGlobalError(ctx context.Context, err error) {
	var apiErr *sub2api.APIError
	if !errors.As(err, &apiErr) {
		return
	}
	if apiErr.Kind == sub2api.ErrorAuth || apiErr.Kind == sub2api.ErrorCompliance {
		s.setPause(err.Error())
		_ = s.store.AddEvent(ctx, "error", "scheduler", "global_pause", "", err.Error(), "{}")
	}
}

func (s *Scheduler) RunNow(ctx context.Context, accountID string) error {
	now := s.clock.Now().Unix()
	if err := s.store.SetAccountRuntime(ctx, accountID, "manual_check", "", &now); err != nil {
		return err
	}
	return s.ProcessAccount(ctx, accountID)
}

func (s *Scheduler) RefreshQuota(ctx context.Context, accountID string) error {
	account, err := s.store.GetAccount(ctx, accountID)
	if err != nil {
		return err
	}
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return err
	}
	client, err := s.factory(settings)
	if err != nil {
		return err
	}
	quota, err := client.Quota(ctx, account.RemoteID)
	if err != nil {
		if recordErr := s.recordQuotaError(ctx, account, err, s.clock.Now()); recordErr != nil {
			return recordErr
		}
		return err
	}
	plan := quota.PlanType
	if plan == "" {
		plan = account.PlanType
	}
	eligible := eligiblePlan(plan)
	_ = s.store.SetEligibility(ctx, accountID, eligible, map[bool]string{true: "", false: "额度接口返回的套餐不在白名单"}[eligible], plan)
	next := account.NextActionAt
	state := account.RuntimeState
	if quota.FiveActive(s.clock.Now()) {
		value := quota.FiveHour.ResetAt + int64(effectiveGrace(account.Policy, settings))
		next = &value
		state = "waiting"
	}
	identity := sub2api.QuotaIdentityHash(quota)
	if identity == "" {
		identity = account.IdentityHash
	}
	_, err = s.store.ApplyQuota(ctx, accountID, quotaUpdate(identity, plan, quota, next, state, ""))
	return err
}

func (s *Scheduler) Models(ctx context.Context, accountID string) ([]string, error) {
	account, err := s.store.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	client, err := s.factory(settings)
	if err != nil {
		return nil, err
	}
	return client.Models(ctx, account.RemoteID)
}
