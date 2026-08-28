package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MengStar-L/sub2api-auto5h/internal/codex"
	"github.com/MengStar-L/sub2api-auto5h/internal/secure"
	"github.com/MengStar-L/sub2api-auto5h/internal/store"
	"github.com/MengStar-L/sub2api-auto5h/internal/sub2api"
)

func (s *Scheduler) sendAttempt(ctx context.Context, client Remote, account store.Account, cycle store.Cycle, settings store.Settings) error {
	if !settings.DirectWakeupEnabled {
		return s.store.SetAccountRuntime(ctx, account.ID, "direct_disabled", "", nil)
	}
	started := s.clock.Now().Unix()
	claimed, ok, err := s.store.StartAttempt(ctx, cycle.ID, started, started+int64(settings.RequestTimeoutSeconds)+30)
	if err != nil || !ok {
		return err
	}
	latest, err := s.store.GetSettings(ctx)
	if err != nil {
		return s.finishPreparationError(ctx, account, claimed, settings, started, effectiveModel(account.Policy, settings), err)
	}
	if !latest.DirectWakeupEnabled {
		return s.store.FinishAttempt(ctx, claimed, started, s.clock.Now().Unix(), store.AttemptResult{
			Outcome: "cancelled", Status: "direct_disabled", AccountState: "direct_disabled", Reason: "直连唤醒已关闭",
		})
	}
	settings = latest
	model := effectiveModel(account.Policy, settings)
	material, err := client.ActivationMaterial(ctx, account.RemoteID, account.IdentityHash, account.Email)
	if err != nil {
		return s.finishPreparationError(ctx, account, claimed, settings, started, model, err)
	}
	_ = s.store.AddEvent(ctx, "info", "scheduler", "credential_exported", account.ID, "已按需取得单账号临时认证材料", "{}")
	refreshed := false
	if !material.ExpiresAt.IsZero() && !material.ExpiresAt.After(s.clock.Now().Add(2*time.Minute)) {
		material, err = s.refreshMaterial(ctx, client, account)
		if err != nil {
			return s.finishPreparationError(ctx, account, claimed, settings, started, model, err)
		}
		refreshed = true
	}
	request := activationRequest(material, model, settings.RequestTimeoutSeconds)
	attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(settings.RequestTimeoutSeconds)*time.Second)
	result, activationErr := s.activator.Activate(attemptCtx, request)
	cancel()
	if isUnauthorized(activationErr) && !refreshed {
		material, err = s.refreshMaterial(ctx, client, account)
		if err != nil {
			return s.finishPreparationError(ctx, account, claimed, settings, started, model, err)
		}
		refreshed = true
		request = activationRequest(material, model, settings.RequestTimeoutSeconds)
		attemptCtx, cancel = context.WithTimeout(ctx, time.Duration(settings.RequestTimeoutSeconds)*time.Second)
		result, activationErr = s.activator.Activate(attemptCtx, request)
		cancel()
	}
	ended := s.clock.Now().Unix()
	if activationErr == nil {
		return s.finishAccepted(ctx, account, claimed, settings, model, result, started, ended)
	}
	if handled, err := s.finishRateLimited(ctx, account, claimed, settings, model, result, activationErr, started, ended); handled {
		return err
	}
	return s.reconcileActivationError(ctx, client, account, claimed, settings, model, result, activationErr, started, ended)
}

func activationRequest(material sub2api.ActivationMaterial, model string, timeoutSeconds int) codex.Request {
	var proxy *codex.Proxy
	if material.Proxy != nil {
		proxy = &codex.Proxy{
			Protocol: material.Proxy.Protocol, Host: material.Proxy.Host, Port: material.Proxy.Port,
			Username: material.Proxy.Username, Password: material.Proxy.Password,
		}
	}
	return codex.Request{
		AccessToken: material.AccessToken, AccountID: material.ChatGPTID, UserAgent: material.UserAgent,
		Model: model, Proxy: proxy, Timeout: time.Duration(timeoutSeconds) * time.Second,
	}
}

func (s *Scheduler) refreshMaterial(ctx context.Context, client Remote, account store.Account) (sub2api.ActivationMaterial, error) {
	if err := client.RefreshAccessToken(ctx, account.RemoteID); err != nil {
		return sub2api.ActivationMaterial{}, err
	}
	_ = s.store.AddEvent(ctx, "info", "scheduler", "token_refreshed", account.ID, "已由 sub2api 刷新账号访问令牌", "{}")
	return client.ActivationMaterial(ctx, account.RemoteID, account.IdentityHash, account.Email)
}

func (s *Scheduler) finishPreparationError(ctx context.Context, account store.Account, cycle store.Cycle, settings store.Settings, started int64, model string, preparationErr error) error {
	ended := s.clock.Now().Unix()
	message := secure.Redact(preparationErr.Error())
	if sub2api.IsTransient(preparationErr) {
		maxAttempts := 1 + effectiveRetries(account.Policy, settings)
		if cycle.AttemptCount < maxAttempts {
			delay := int64(effectiveRetryBase(account.Policy, settings)) << (cycle.AttemptCount - 1)
			if delay > 600 {
				delay = 600
			}
			next := ended + delay
			return s.store.FinishAttempt(ctx, cycle, started, ended, store.AttemptResult{
				Outcome: "retry", ErrorCode: "CREDENTIAL_TRANSIENT", Message: message, RequestModel: model,
				Status: "retry_wait", NextAt: &next, Reason: fmt.Sprintf("临时认证材料获取失败，%d 秒后重试", delay),
			})
		}
		_ = s.store.AddEvent(ctx, "error", "scheduler", "credential_failed", cycle.AccountID, message, "{}")
		return s.store.FinishAttempt(ctx, cycle, started, ended, store.AttemptResult{
			Outcome: "failed", ErrorCode: "CREDENTIAL_TRANSIENT", Message: message, RequestModel: model,
			Status: "attention", Reason: "临时认证材料获取已达到最大尝试次数",
		})
	}
	_ = s.store.AddEvent(ctx, "error", "scheduler", "credential_failed", cycle.AccountID, message, "{}")
	return s.store.FinishAttempt(ctx, cycle, started, ended, store.AttemptResult{
		Outcome: "attention", ErrorCode: "CREDENTIAL_ERROR", Message: message, RequestModel: model,
		Status: "attention", Reason: "临时认证材料不可用",
	})
}

func (s *Scheduler) finishAccepted(ctx context.Context, account store.Account, cycle store.Cycle, settings store.Settings, model string, result codex.Result, started, ended int64) error {
	accepted := ended
	statusCode := result.HTTPStatus
	assessment := answerStatus(result.Reply)
	summary := fmt.Sprintf("%s; reply_runes=%d", result.Terminal, len([]rune(result.Reply)))
	base := store.AttemptResult{
		Outcome: "accepted", HTTPStatus: &statusCode, AcceptedAt: &accepted, AnswerStatus: assessment,
		AnswerText: result.Reply, RequestModel: model, TransportPath: result.TransportPath,
		AnswerSource: "official_codex_sse", TerminalSummary: summary,
	}
	if result.RateLimits.FiveHour != nil {
		window := result.RateLimits.FiveHour
		next := window.ResetAt + int64(effectiveGrace(account.Policy, settings))
		base.Status = "verified"
		base.NextAt = &next
		base.QuotaEvidence = "official_headers"
		base.Reason = "官方响应头已确认 5h 窗口"
		if err := s.store.FinishAttempt(ctx, cycle, started, ended, base); err != nil {
			return err
		}
		quota := codexQuota(window, result.RateLimits.SevenDay, ended, account.PlanType, account.IdentityHash)
		_, _ = s.store.ApplyQuota(ctx, account.ID, quotaUpdate(account.IdentityHash, account.PlanType, quota, &next, "verified", ""))
		_, _ = s.store.EnsureCycle(ctx, account.ID, account.IdentityGeneration, fmt.Sprintf("reset:%d", window.ResetAt), "reset", &window.ResetAt, next)
		_ = s.store.AddEvent(ctx, "info", "scheduler", "activation_verified", account.ID, "激活请求成功，官方响应头已确认新 5h 窗口", "{}")
		return nil
	}
	fallback := ended + sub2api.FiveHoursSeconds + int64(effectiveGrace(account.Policy, settings))
	deadline := ended + 60
	base.Status = "verifying"
	base.NextAt = &fallback
	base.VerificationDeadlineAt = &deadline
	base.Reason = "官方请求已完成，等待额度证据"
	if err := s.store.FinishAttempt(ctx, cycle, started, ended, base); err != nil {
		return err
	}
	_ = s.store.AddEvent(ctx, "info", "scheduler", "activation_accepted", account.ID, assessmentMessage(assessment)+"，等待额度核验", "{}")
	s.startVerification(account.ID, cycle.ID, ended)
	return nil
}

func codexQuota(five *codex.RateWindow, seven *codex.RateWindow, fetched int64, plan, identity string) sub2api.Quota {
	quota := sub2api.Quota{PlanType: plan, AccountID: identity, FetchedAt: fetched, Allowed: true}
	if five != nil {
		quota.FiveHour = &sub2api.Window{UsedPercent: five.UsedPercent, LimitWindowSeconds: sub2api.FiveHoursSeconds, ResetAfterSeconds: five.ResetAfterSeconds, ResetAt: five.ResetAt}
	}
	if seven != nil {
		quota.SevenDay = &sub2api.Window{UsedPercent: seven.UsedPercent, LimitWindowSeconds: sub2api.SevenDaysSeconds, ResetAfterSeconds: seven.ResetAfterSeconds, ResetAt: seven.ResetAt}
	}
	return quota
}

func (s *Scheduler) finishRateLimited(ctx context.Context, account store.Account, cycle store.Cycle, settings store.Settings, model string, result codex.Result, activationErr error, started, ended int64) (bool, error) {
	var apiErr *codex.Error
	if !errors.As(activationErr, &apiErr) || apiErr.Kind != codex.ErrorRateLimited {
		return false, nil
	}
	message := secure.Redact(apiErr.Error())
	statusCode := result.HTTPStatus
	if apiErr.RateLimits.SevenDay != nil && apiErr.RateLimits.SevenDay.UsedPercent >= 100 {
		next := apiErr.RateLimits.SevenDay.ResetAt + int64(effectiveGrace(account.Policy, settings))
		return true, s.store.FinishAttempt(ctx, cycle, started, ended, store.AttemptResult{
			Outcome: "rate_limited", HTTPStatus: &statusCode, ErrorCode: "RATE_LIMITED_7D", Message: message,
			RequestModel: model, TransportPath: result.TransportPath, QuotaEvidence: "official_headers",
			Status: "retry_wait", AccountState: "blocked_7d", NextAt: &next, Reason: "官方 7d 额度已耗尽",
		})
	}
	if apiErr.RateLimits.FiveHour != nil {
		next := apiErr.RateLimits.FiveHour.ResetAt + int64(effectiveGrace(account.Policy, settings))
		return true, s.store.FinishAttempt(ctx, cycle, started, ended, store.AttemptResult{
			Outcome: "rate_limited", HTTPStatus: &statusCode, ErrorCode: "RATE_LIMITED_5H", Message: message,
			RequestModel: model, TransportPath: result.TransportPath, QuotaEvidence: "official_headers",
			Status: "retry_wait", AccountState: "waiting", NextAt: &next, Reason: "官方 5h 窗口仍在限流",
		})
	}
	return true, s.store.FinishAttempt(ctx, cycle, started, ended, store.AttemptResult{
		Outcome: "attention", HTTPStatus: &statusCode, ErrorCode: "RATE_LIMIT_SCHEMA", Message: message,
		RequestModel: model, TransportPath: result.TransportPath, Status: "attention", Reason: "429 响应缺少可信额度窗口",
	})
}

func (s *Scheduler) reconcileActivationError(ctx context.Context, client Remote, account store.Account, cycle store.Cycle, settings store.Settings, model string, result codex.Result, activationErr error, started, ended int64) error {
	statusCode, code, message := activationErrorDetails(activationErr, result)
	reconcileCtx, cancel := context.WithTimeout(ctx, time.Duration(settings.RequestTimeoutSeconds)*time.Second)
	quota, quotaErr := client.Quota(reconcileCtx, account.RemoteID)
	cancel()
	if quotaErr == nil && quota.FiveActive(time.Unix(ended, 0)) && (cycle.SourceResetAt == nil || quota.FiveHour.ResetAt != *cycle.SourceResetAt) {
		next := quota.FiveHour.ResetAt + int64(effectiveGrace(account.Policy, settings))
		accepted := ended
		if err := s.store.FinishAttempt(ctx, cycle, started, ended, store.AttemptResult{
			Outcome: "inferred_success", HTTPStatus: statusCode, ErrorCode: code, Message: message,
			RequestModel: model, TransportPath: result.TransportPath, QuotaEvidence: "sub2api_quota",
			Status: "success_inferred", NextAt: &next, AcceptedAt: &accepted, Reason: "请求结果不确定，但额度窗口已前移",
		}); err != nil {
			return err
		}
		_, err := s.store.EnsureCycle(ctx, account.ID, account.IdentityGeneration, fmt.Sprintf("reset:%d", quota.FiveHour.ResetAt), "reset", &quota.FiveHour.ResetAt, next)
		return err
	}
	if quotaErr != nil && !sub2api.IsTransient(quotaErr) {
		reason := secure.Redact("请求结果不确定，额度协调失败: " + quotaErr.Error())
		return s.store.FinishAttempt(ctx, cycle, started, ended, store.AttemptResult{
			Outcome: "attention", HTTPStatus: statusCode, ErrorCode: code, Message: reason,
			RequestModel: model, TransportPath: result.TransportPath, Status: "attention", Reason: reason,
		})
	}
	var apiErr *codex.Error
	if errors.As(activationErr, &apiErr) && apiErr.Kind != codex.ErrorTransient {
		return s.store.FinishAttempt(ctx, cycle, started, ended, store.AttemptResult{
			Outcome: "attention", HTTPStatus: statusCode, ErrorCode: code, Message: message,
			RequestModel: model, TransportPath: result.TransportPath, Status: "attention", Reason: "官方激活请求返回不可重试错误",
		})
	}
	maxAttempts := 1 + effectiveRetries(account.Policy, settings)
	if cycle.AttemptCount < maxAttempts {
		delay := int64(effectiveRetryBase(account.Policy, settings)) << (cycle.AttemptCount - 1)
		if delay > 600 {
			delay = 600
		}
		next := ended + delay
		return s.store.FinishAttempt(ctx, cycle, started, ended, store.AttemptResult{
			Outcome: "retry", HTTPStatus: statusCode, ErrorCode: code, Message: message,
			RequestModel: model, TransportPath: result.TransportPath, Status: "retry_wait", NextAt: &next,
			Reason: fmt.Sprintf("第 %d 次请求未确认，%d 秒后重试", cycle.AttemptCount, delay),
		})
	}
	return s.store.FinishAttempt(ctx, cycle, started, ended, store.AttemptResult{
		Outcome: "failed", HTTPStatus: statusCode, ErrorCode: code, Message: message,
		RequestModel: model, TransportPath: result.TransportPath, Status: "attention", Reason: "已达到最大尝试次数",
	})
}

func answerStatus(reply string) string {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return "no_answer"
	}
	if trimmed == "21" {
		return "normal"
	}
	return "abnormal"
}

func assessmentMessage(status string) string {
	switch status {
	case "normal":
		return "激活请求成功，智商检测正常"
	case "no_answer":
		return "激活请求成功，但没有有效回答"
	default:
		return "激活请求成功，智商检测不正常"
	}
}

func isUnauthorized(err error) bool {
	var apiErr *codex.Error
	return errors.As(err, &apiErr) && apiErr.Kind == codex.ErrorUnauthorized
}

func activationErrorDetails(err error, result codex.Result) (*int, string, string) {
	var status *int
	if result.HTTPStatus != 0 {
		value := result.HTTPStatus
		status = &value
	}
	var apiErr *codex.Error
	if errors.As(err, &apiErr) {
		code := apiErr.Code
		if code == "" {
			code = strings.ToUpper(string(apiErr.Kind))
		}
		return status, code, secure.Redact(apiErr.Message)
	}
	return status, "REQUEST_ERROR", secure.Redact(err.Error())
}
