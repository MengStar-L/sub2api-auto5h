package scheduler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MengStar-L/sub2api-auto5h/internal/secure"
	"github.com/MengStar-L/sub2api-auto5h/internal/store"
	"github.com/MengStar-L/sub2api-auto5h/internal/sub2api"
)

type errorRemote struct {
	accountsErr error
	quotaErr    error
}

func (r *errorRemote) Accounts(context.Context) ([]sub2api.Account, error) {
	if r.accountsErr != nil {
		return nil, r.accountsErr
	}
	return []sub2api.Account{}, nil
}

func (r *errorRemote) Quota(context.Context, int64) (sub2api.Quota, error) {
	return sub2api.Quota{}, r.quotaErr
}

func (*errorRemote) Models(context.Context, int64) ([]string, error) {
	return []string{}, nil
}

func (*errorRemote) TestAccount(context.Context, int64, string) (sub2api.TestResult, error) {
	return sub2api.TestResult{}, nil
}

func TestQuotaAuthFailurePausesOnlyTheAccount(t *testing.T) {
	data, account := schedulerTestStore(t)
	remote := &errorRemote{quotaErr: &sub2api.APIError{
		Kind: sub2api.ErrorAuth, StatusCode: http.StatusUnauthorized, Message: "upstream OAuth token expired",
	}}
	automation := schedulerWithRemote(data, remote)

	if err := automation.ProcessAccount(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	if automation.Status().GlobalPause {
		t.Fatal("single-account quota auth failure paused the whole scheduler")
	}
	updated, err := data.GetAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RuntimeState != "paused" || !strings.Contains(updated.LastError, "OAuth token expired") {
		t.Fatalf("account=%#v", updated)
	}
}

func TestRefreshQuotaPersistsAccountError(t *testing.T) {
	data, account := schedulerTestStore(t)
	remoteErr := &sub2api.APIError{Kind: sub2api.ErrorSchema, Message: "quota window is invalid"}
	automation := schedulerWithRemote(data, &errorRemote{quotaErr: remoteErr})

	if err := automation.RefreshQuota(context.Background(), account.ID); err == nil {
		t.Fatal("refresh should return the upstream schema error")
	}
	updated, err := data.GetAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RuntimeState != "attention" || !strings.Contains(updated.LastError, "quota window is invalid") {
		t.Fatalf("account=%#v", updated)
	}
}

func TestInventoryAuthFailureStillPausesTheConnection(t *testing.T) {
	data, _ := schedulerTestStore(t)
	remoteErr := &sub2api.APIError{Kind: sub2api.ErrorAuth, StatusCode: http.StatusUnauthorized, Message: "invalid admin API key"}
	automation := schedulerWithRemote(data, &errorRemote{accountsErr: remoteErr})

	if err := automation.Sync(context.Background()); err == nil {
		t.Fatal("sync should return the management authentication error")
	}
	if !automation.Status().GlobalPause {
		t.Fatal("management authentication failure did not pause the scheduler")
	}
}

func schedulerWithRemote(data *store.Store, remote Remote) *Scheduler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(data, func(store.Settings) (Remote, error) { return remote, nil }, logger)
}

func schedulerTestStore(t *testing.T) (*store.Store, store.Account) {
	t.Helper()
	box, err := secure.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	data, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	ctx := context.Background()
	if err := data.SetSetupToken(ctx, "hash", time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	settings := store.Settings{
		ConnectionUUID: "connection", BaseURL: "https://example.com", APIKey: "admin-secret", GlobalModel: "gpt-text",
		SyncIntervalSeconds: 300, ResetGraceSeconds: 30, MaxRetries: 3, RetryBaseSeconds: 30,
		RequestTimeoutSeconds: 90, MaxConcurrency: 4,
	}
	if err := data.CompleteSetup(ctx, "hash", "admin", "password-hash", settings); err != nil {
		t.Fatal(err)
	}
	input := store.RemoteAccountInput{
		RemoteID: 7, RemoteCreatedAt: "2026-08-27T00:00:00Z", Name: "test", PlanType: "plus",
		Platform: "openai", AccountType: "oauth", Status: "active", Schedulable: true, Eligible: true,
	}
	if err := data.ReplaceInventory(ctx, settings.ConnectionUUID, []store.RemoteAccountInput{input}); err != nil {
		t.Fatal(err)
	}
	accounts, err := data.ListAccounts(ctx)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts=%d err=%v", len(accounts), err)
	}
	if err := data.SetPolicy(ctx, accounts[0].ID, store.Policy{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	account, err := data.GetAccount(ctx, accounts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return data, account
}
