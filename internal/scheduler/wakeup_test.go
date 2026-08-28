package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MengStar-L/sub2api-auto5h/internal/codex"
	"github.com/MengStar-L/sub2api-auto5h/internal/sub2api"
)

type scriptedRemote struct {
	mu              sync.Mutex
	quota           sub2api.Quota
	quotas          []sub2api.Quota
	quotaErr        error
	testResult      codex.Result
	testErr         error
	testCalls       int
	credentialCalls int
}

type fixedClock struct {
	now time.Time
}

func (clock fixedClock) Now() time.Time { return clock.now }

func (fixedClock) After(time.Duration) <-chan time.Time {
	return make(chan time.Time)
}

func (*scriptedRemote) Accounts(context.Context) ([]sub2api.Account, error) {
	return []sub2api.Account{}, nil
}

func (*scriptedRemote) Models(context.Context, int64) ([]string, error) {
	return []string{}, nil
}

func (r *scriptedRemote) Quota(context.Context, int64) (sub2api.Quota, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.quotas) > 0 {
		value := r.quotas[0]
		r.quotas = r.quotas[1:]
		return value, nil
	}
	return r.quota, r.quotaErr
}

func (r *scriptedRemote) ActivationMaterial(context.Context, int64, string, string) (sub2api.ActivationMaterial, error) {
	r.mu.Lock()
	r.credentialCalls++
	r.mu.Unlock()
	return sub2api.ActivationMaterial{AccessToken: "token", ChatGPTID: "workspace"}, nil
}

func (*scriptedRemote) RefreshAccessToken(context.Context, int64) error { return nil }

func (r *scriptedRemote) Activate(context.Context, codex.Request) (codex.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.testCalls++
	return r.testResult, r.testErr
}

func (r *scriptedRemote) testCallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.testCalls
}

func (r *scriptedRemote) credentialCallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.credentialCalls
}

func testQuota(now time.Time, used float64) sub2api.Quota {
	return sub2api.Quota{
		PlanType:  "plus",
		AccountID: "workspace",
		FetchedAt: now.Unix(),
		Allowed:   true,
		FiveHour: &sub2api.Window{
			UsedPercent:        used,
			LimitWindowSeconds: sub2api.FiveHoursSeconds,
			ResetAfterSeconds:  sub2api.FiveHoursSeconds,
			ResetAt:            now.Add(5 * time.Hour).Unix(),
		},
		SevenDay: &sub2api.Window{
			UsedPercent:        10,
			LimitWindowSeconds: sub2api.SevenDaysSeconds,
			ResetAfterSeconds:  6 * 24 * 60 * 60,
			ResetAt:            now.Add(6 * 24 * time.Hour).Unix(),
		},
	}
}

func TestProcessAccountDispatchesWhenFiveHourUsageIsZero(t *testing.T) {
	data, account := schedulerTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	remote := &scriptedRemote{
		quota:      testQuota(now, 0),
		testResult: codex.Result{HTTPStatus: 200, Terminal: "response.completed", TransportPath: "direct"},
	}
	automation := schedulerWithRemote(data, remote)
	t.Cleanup(automation.Stop)

	if err := automation.ProcessAccount(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	if calls := remote.testCallCount(); calls != 1 {
		t.Fatalf("test calls=%d", calls)
	}
}

func TestDirectWakeupDisabledNeverExportsCredentialsOrDispatches(t *testing.T) {
	data, account := schedulerTestStore(t)
	settings, err := data.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings.DirectWakeupEnabled = false
	if err := data.UpdateSettings(context.Background(), settings, false, false); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	remote := &scriptedRemote{quota: testQuota(now, 0)}
	automation := schedulerWithRemote(data, remote)
	t.Cleanup(automation.Stop)

	if err := automation.ProcessAccount(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	if remote.credentialCallCount() != 0 || remote.testCallCount() != 0 {
		t.Fatalf("credential calls=%d activation calls=%d", remote.credentialCallCount(), remote.testCallCount())
	}
	updated, err := data.GetAccount(context.Background(), account.ID)
	if err != nil || updated.RuntimeState != "direct_disabled" || updated.NextActionAt != nil {
		t.Fatalf("account=%#v err=%v", updated, err)
	}
}

func TestOfficialFiveHourHeaderVerifiesImmediately(t *testing.T) {
	data, account := schedulerTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	remote := &scriptedRemote{
		quota: testQuota(now, 0),
		testResult: codex.Result{
			HTTPStatus: 200, Terminal: "response.completed", TransportPath: "direct", Reply: "29",
			RateLimits: codex.RateLimits{FiveHour: &codex.RateWindow{UsedPercent: 1, ResetAfterSeconds: 17_900, ResetAt: now.Add(17_900 * time.Second).Unix()}},
		},
	}
	automation := schedulerWithRemote(data, remote)
	t.Cleanup(automation.Stop)
	if err := automation.ProcessAccount(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	updated, err := data.GetAccount(context.Background(), account.ID)
	if err != nil || updated.RuntimeState != "verified" || updated.LastAnswerStatus != "abnormal" || updated.LastAnswerText != "29" || updated.LastQuotaEvidence != "official_headers" {
		t.Fatalf("account=%#v err=%v", updated, err)
	}
}

func TestProcessAccountWaitsForPositiveUsageWindow(t *testing.T) {
	data, account := schedulerTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	remote := &scriptedRemote{quota: testQuota(now, 0.01)}
	automation := schedulerWithRemote(data, remote)
	t.Cleanup(automation.Stop)

	if err := automation.ProcessAccount(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	if calls := remote.testCallCount(); calls != 0 {
		t.Fatalf("test calls=%d", calls)
	}
	updated, err := data.GetAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RuntimeState != "waiting" {
		t.Fatalf("runtime state=%q", updated.RuntimeState)
	}
}

func TestProcessAccountDoesNotDispatchWhenExpiredQuotaDisallowsRequests(t *testing.T) {
	data, account := schedulerTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	quota := testQuota(now, 0)
	quota.Allowed = false
	quota.FiveHour.ResetAt = now.Add(-time.Second).Unix()
	remote := &scriptedRemote{quota: quota}
	automation := schedulerWithRemote(data, remote)
	t.Cleanup(automation.Stop)

	if err := automation.ProcessAccount(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	if calls := remote.testCallCount(); calls != 0 {
		t.Fatalf("test calls=%d", calls)
	}
	updated, err := data.GetAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RuntimeState != "attention" {
		t.Fatalf("runtime state=%q", updated.RuntimeState)
	}
}

func TestZeroUsageResetAdvanceDoesNotInferSuccess(t *testing.T) {
	data, account := schedulerTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	before := testQuota(now, 0)
	after := testQuota(now.Add(time.Second), 0)
	remote := &scriptedRemote{
		quotas:  []sub2api.Quota{before, after},
		quota:   after,
		testErr: errors.New("ambiguous test failure"),
	}
	automation := schedulerWithRemote(data, remote)
	t.Cleanup(automation.Stop)

	if err := automation.ProcessAccount(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	cycles, err := data.ListCycles(context.Background(), account.ID, 10)
	if err != nil || len(cycles) != 1 {
		t.Fatalf("cycles=%#v err=%v", cycles, err)
	}
	if cycles[0].Status != "retry_wait" {
		t.Fatalf("cycle status=%q reason=%q", cycles[0].Status, cycles[0].Reason)
	}
}

func TestSuccessfulPuzzleRepliesDoNotChangeActivationSuccess(t *testing.T) {
	tests := []struct {
		name       string
		reply      string
		assessment string
	}{
		{name: "normal", reply: " 21\n", assessment: "normal"},
		{name: "wrong number", reply: "29", assessment: "abnormal"},
		{name: "empty reply", reply: "", assessment: "no_answer"},
		{name: "explanation", reply: "答案是21", assessment: "abnormal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, account := schedulerTestStore(t)
			now := time.Now().UTC().Truncate(time.Second)
			remote := &scriptedRemote{
				quota:      testQuota(now, 0),
				testResult: codex.Result{HTTPStatus: 200, Terminal: "response.completed", TransportPath: "direct", Reply: test.reply},
			}
			automation := schedulerWithRemote(data, remote)
			t.Cleanup(automation.Stop)

			if err := automation.ProcessAccount(context.Background(), account.ID); err != nil {
				t.Fatal(err)
			}
			if calls := remote.testCallCount(); calls != 1 {
				t.Fatalf("test calls=%d", calls)
			}
			cycles, err := data.ListCycles(context.Background(), account.ID, 10)
			if err != nil || len(cycles) != 1 {
				t.Fatalf("cycles=%#v err=%v", cycles, err)
			}
			if cycles[0].Status != "verifying" || cycles[0].AttemptCount != 1 {
				t.Fatalf("cycle=%#v", cycles[0])
			}
			attempts, err := data.ListAttempts(context.Background(), cycles[0].ID)
			if err != nil || len(attempts) != 1 {
				t.Fatalf("attempts=%#v err=%v", attempts, err)
			}
			if attempts[0].Outcome != "accepted" || attempts[0].AnswerStatus != test.assessment || attempts[0].AnswerText != test.reply {
				t.Fatalf("attempt=%#v", attempts[0])
			}
			updated, err := data.GetAccount(context.Background(), account.ID)
			if err != nil {
				t.Fatal(err)
			}
			if updated.RuntimeState != "verifying" || updated.LastAnswerStatus != test.assessment || updated.LastAnswerText != test.reply || updated.LastError != "" {
				t.Fatalf("account=%#v", updated)
			}
		})
	}
}

func TestAcceptedUnverifiedCreatesFallbackCycleAtNextDueTime(t *testing.T) {
	data, account := schedulerTestStore(t)
	firstNow := time.Now().UTC().Truncate(time.Second)
	firstRemote := &scriptedRemote{
		quota:      testQuota(firstNow, 0),
		testResult: codex.Result{HTTPStatus: 200, Terminal: "response.completed", TransportPath: "direct", Reply: "21"},
	}
	first := schedulerWithRemote(data, firstRemote)
	if err := first.ProcessAccount(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	first.Stop()

	initialCycles, err := data.ListCycles(context.Background(), account.ID, 10)
	if err != nil || len(initialCycles) != 1 {
		t.Fatalf("cycles=%#v err=%v", initialCycles, err)
	}
	accepted, err := data.GetAccount(context.Background(), account.ID)
	if err != nil || accepted.NextActionAt == nil || accepted.RuntimeState != "verifying" {
		t.Fatalf("accepted=%#v err=%v", accepted, err)
	}
	finalized, err := data.FinalizeVerification(context.Background(), initialCycles[0].ID, firstNow.Add(60*time.Second).Unix(), *accepted.NextActionAt, "请求成功，60 秒内未获得 5h 额度证据")
	if err != nil || !finalized {
		t.Fatalf("finalized=%v err=%v", finalized, err)
	}
	accepted, err = data.GetAccount(context.Background(), account.ID)
	if err != nil || accepted.RuntimeState != "accepted_unverified" {
		t.Fatalf("accepted=%#v err=%v", accepted, err)
	}
	secondNow := time.Unix(*accepted.NextActionAt, 0)
	secondRemote := &scriptedRemote{
		quota:      testQuota(secondNow, 0),
		testResult: codex.Result{HTTPStatus: 200, Terminal: "response.completed", TransportPath: "direct", Reply: "29"},
	}
	second := schedulerWithRemote(data, secondRemote)
	second.clock = fixedClock{now: secondNow}
	t.Cleanup(second.Stop)
	if err := second.ProcessAccount(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}

	cycles, err := data.ListCycles(context.Background(), account.ID, 10)
	if err != nil || len(cycles) != 2 {
		t.Fatalf("cycles=%#v err=%v", cycles, err)
	}
	foundFallback := false
	for _, cycle := range cycles {
		if cycle.Kind == "fallback" && cycle.Status == "verifying" {
			foundFallback = true
		}
	}
	if !foundFallback || secondRemote.testCallCount() != 1 {
		t.Fatalf("fallback=%v calls=%d cycles=%#v", foundFallback, secondRemote.testCallCount(), cycles)
	}
}
