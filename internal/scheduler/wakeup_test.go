package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MengStar-L/sub2api-auto5h/internal/sub2api"
)

type scriptedRemote struct {
	mu         sync.Mutex
	quota      sub2api.Quota
	quotas     []sub2api.Quota
	quotaErr   error
	testResult sub2api.TestResult
	testErr    error
	testCalls  int
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

func (r *scriptedRemote) TestAccount(context.Context, int64, string) (sub2api.TestResult, error) {
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
		testResult: sub2api.TestResult{HTTPStatus: 200, Success: true},
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
