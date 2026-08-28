package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/MengStar-L/sub2api-auto5h/internal/secure"
)

func TestEmptyListsMarshalAsArrays(t *testing.T) {
	data := openTestStore(t)
	ctx := context.Background()
	values := make([]any, 0, 5)
	accounts, err := data.ListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	due, err := data.ListDueAccountIDs(ctx, time.Now().Unix(), 100)
	if err != nil {
		t.Fatal(err)
	}
	cycles, err := data.ListCycles(ctx, "missing-account", 100)
	if err != nil {
		t.Fatal(err)
	}
	attempts, err := data.ListAttempts(ctx, "missing-cycle")
	if err != nil {
		t.Fatal(err)
	}
	events, err := data.ListEvents(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	values = append(values, accounts, due, cycles, attempts, events)
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != "[]" {
			t.Fatalf("empty public list encoded as %s", encoded)
		}
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	box, err := secure.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	data, err := Open(context.Background(), filepath.Join(t.TempDir(), "app.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	return data
}

func TestSetupEncryptsAPIKeyAndConsumesToken(t *testing.T) {
	data := openTestStore(t)
	ctx := context.Background()
	if err := data.SetSetupToken(ctx, "hash", time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	settings := Settings{ConnectionUUID: "connection", BaseURL: "https://example.com", APIKey: "admin-secret", GlobalModel: "gpt-text", SyncIntervalSeconds: 300, ResetGraceSeconds: 30, MaxRetries: 3, RetryBaseSeconds: 30, RequestTimeoutSeconds: 90, MaxConcurrency: 4}
	if err := data.CompleteSetup(ctx, "hash", "admin", "password-hash", settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := data.GetSettings(ctx)
	if err != nil || loaded.APIKey != "admin-secret" {
		t.Fatalf("settings=%#v err=%v", loaded, err)
	}
	var cipher string
	if err := data.db.QueryRow(`SELECT api_key_cipher FROM settings WHERE id=1`).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	if cipher == "admin-secret" {
		t.Fatal("API key was stored in plaintext")
	}
	if err := data.CompleteSetup(ctx, "hash", "admin", "password-hash", settings); err != ErrSetupComplete {
		t.Fatalf("second setup = %v", err)
	}
}

func TestUniqueCycleAndPolicyGeneration(t *testing.T) {
	data := openTestStore(t)
	ctx := context.Background()
	input := RemoteAccountInput{RemoteID: 7, RemoteCreatedAt: "2026-08-27T00:00:00Z", Name: "test", Platform: "openai", AccountType: "oauth", Status: "active", Schedulable: true, Eligible: true}
	if err := data.ReplaceInventory(ctx, "conn", []RemoteAccountInput{input}); err != nil {
		t.Fatal(err)
	}
	accounts, _ := data.ListAccounts(ctx)
	account := accounts[0]
	if err := data.SetPolicy(ctx, account.ID, Policy{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	account, _ = data.GetAccount(ctx, account.ID)
	first, err := data.EnsureCycle(ctx, account.ID, account.IdentityGeneration, "bootstrap:1", "bootstrap", nil, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	second, err := data.EnsureCycle(ctx, account.ID, account.IdentityGeneration, "bootstrap:1", "bootstrap", nil, time.Now().Unix()+1)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatal("duplicate cycle was created")
	}
}

func TestFinishAttemptPersistsAnswerAssessment(t *testing.T) {
	data := openTestStore(t)
	ctx := context.Background()
	account := insertInventoryAccount(t, data, eligibleInventoryAccount())
	if err := data.SetPolicy(ctx, account.ID, Policy{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	account, err := data.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().Unix()
	cycle, err := data.EnsureCycle(ctx, account.ID, account.IdentityGeneration, "bootstrap:1", "bootstrap", nil, started)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := data.StartAttempt(ctx, cycle.ID, started, started+120)
	if err != nil || !ok {
		t.Fatalf("claimed=%v err=%v", ok, err)
	}
	ended := started + 1
	if err := data.FinishAttempt(ctx, claimed, started, ended, AttemptResult{
		Outcome: "accepted", Status: "verifying", AnswerStatus: "abnormal", AnswerText: "答案是 29",
		RequestModel: "gpt-text", TransportPath: "proxy:socks5", AnswerSource: "official_codex_sse",
		QuotaEvidence: "official_headers", TerminalSummary: "response.completed; reply_runes=5",
	}); err != nil {
		t.Fatal(err)
	}

	updated, err := data.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastAnswerStatus != "abnormal" || updated.LastAnswerText != "答案是 29" || updated.LastAnswerAt == nil || *updated.LastAnswerAt != ended ||
		updated.LastRequestModel != "gpt-text" || updated.LastTransportPath != "proxy:socks5" || updated.LastAnswerSource != "official_codex_sse" ||
		updated.LastQuotaEvidence != "official_headers" || updated.LastTerminalSummary != "response.completed; reply_runes=5" {
		t.Fatalf("account answer=%#v", updated)
	}
	attempts, err := data.ListAttempts(ctx, cycle.ID)
	if err != nil || len(attempts) != 1 || attempts[0].AnswerStatus != "abnormal" || attempts[0].AnswerText != "答案是 29" ||
		attempts[0].RequestModel != "gpt-text" || attempts[0].TransportPath != "proxy:socks5" || attempts[0].AnswerSource != "official_codex_sse" ||
		attempts[0].QuotaEvidence != "official_headers" || attempts[0].TerminalSummary != "response.completed; reply_runes=5" {
		t.Fatalf("attempts=%#v err=%v", attempts, err)
	}

	failedCycle, err := data.EnsureCycle(ctx, account.ID, account.IdentityGeneration, "reset:2", "reset", nil, ended+1)
	if err != nil {
		t.Fatal(err)
	}
	failedClaim, ok, err := data.StartAttempt(ctx, failedCycle.ID, ended+1, ended+121)
	if err != nil || !ok {
		t.Fatalf("claimed=%v err=%v", ok, err)
	}
	if err := data.FinishAttempt(ctx, failedClaim, ended+1, ended+2, AttemptResult{
		Outcome: "failed", Status: "attention", Message: "request failed",
	}); err != nil {
		t.Fatal(err)
	}
	preserved, err := data.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preserved.LastAnswerStatus != "abnormal" || preserved.LastAnswerText != "答案是 29" || preserved.LastAnswerAt == nil || *preserved.LastAnswerAt != ended {
		t.Fatalf("preserved answer=%#v", preserved)
	}
}

func TestAnswerTextLimitPreservesUTF8(t *testing.T) {
	value := strings.Repeat("智", 2001)
	limited := limitRunes(value, 2000)
	if len([]rune(limited)) != 2000 || !utf8.ValidString(limited) {
		t.Fatalf("limited runes=%d valid=%v", len([]rune(limited)), utf8.ValidString(limited))
	}
}

func eligibleInventoryAccount() RemoteAccountInput {
	return RemoteAccountInput{
		RemoteID:        7,
		RemoteCreatedAt: "2026-08-27T00:00:00Z",
		IdentityHash:    "identity-a",
		Name:            "plus-account",
		Platform:        "openai",
		AccountType:     "oauth",
		Status:          "active",
		Schedulable:     true,
		Eligible:        true,
	}
}

func insertInventoryAccount(t *testing.T, data *Store, input RemoteAccountInput) Account {
	t.Helper()
	ctx := context.Background()
	if err := data.ReplaceInventory(ctx, "conn", []RemoteAccountInput{input}); err != nil {
		t.Fatal(err)
	}
	accounts, err := data.ListAccounts(ctx)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts=%d err=%v", len(accounts), err)
	}
	return accounts[0]
}

func markInventoryAccountMissing(t *testing.T, data *Store, id string) {
	t.Helper()
	if err := data.ReplaceInventory(context.Background(), "conn", []RemoteAccountInput{}); err != nil {
		t.Fatal(err)
	}
	missing, err := data.GetAccount(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !missing.Missing || missing.RuntimeState != "missing" || missing.NextActionAt != nil {
		t.Fatalf("missing account=%#v", missing)
	}
}

func TestReplaceInventoryRestoresEnabledAccountAfterReappearance(t *testing.T) {
	data := openTestStore(t)
	ctx := context.Background()
	input := eligibleInventoryAccount()
	account := insertInventoryAccount(t, data, input)
	if err := data.SetPolicy(ctx, account.ID, Policy{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	markInventoryAccountMissing(t, data, account.ID)

	if err := data.ReplaceInventory(ctx, "conn", []RemoteAccountInput{input}); err != nil {
		t.Fatal(err)
	}
	restored, err := data.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Missing || !restored.Policy.Enabled || restored.RuntimeState != "direct_disabled" || restored.NextActionAt != nil {
		t.Fatalf("restored account=%#v", restored)
	}
}

func TestReplaceInventoryRestoresDisabledAccountAfterReappearance(t *testing.T) {
	data := openTestStore(t)
	ctx := context.Background()
	input := eligibleInventoryAccount()
	account := insertInventoryAccount(t, data, input)
	markInventoryAccountMissing(t, data, account.ID)

	if err := data.ReplaceInventory(ctx, "conn", []RemoteAccountInput{input}); err != nil {
		t.Fatal(err)
	}
	restored, err := data.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Missing || restored.Policy.Enabled || restored.RuntimeState != "disabled" || restored.NextActionAt != nil {
		t.Fatalf("restored account=%#v", restored)
	}
}

func TestReplaceInventoryKeepsReappearingIneligibleAccountPaused(t *testing.T) {
	data := openTestStore(t)
	ctx := context.Background()
	input := eligibleInventoryAccount()
	account := insertInventoryAccount(t, data, input)
	markInventoryAccountMissing(t, data, account.ID)
	input.Eligible = false
	input.Schedulable = false
	input.EligibilityReason = "sub2api 已关闭账号调度"

	if err := data.ReplaceInventory(ctx, "conn", []RemoteAccountInput{input}); err != nil {
		t.Fatal(err)
	}
	restored, err := data.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Missing || restored.RuntimeState != "paused" || restored.NextActionAt != nil || restored.EligibilityReason != input.EligibilityReason {
		t.Fatalf("restored account=%#v", restored)
	}
}

func TestReplaceInventoryStillDisablesChangedIdentityAfterReappearance(t *testing.T) {
	data := openTestStore(t)
	ctx := context.Background()
	input := eligibleInventoryAccount()
	account := insertInventoryAccount(t, data, input)
	if err := data.SetPolicy(ctx, account.ID, Policy{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	markInventoryAccountMissing(t, data, account.ID)
	input.IdentityHash = "identity-b"

	if err := data.ReplaceInventory(ctx, "conn", []RemoteAccountInput{input}); err != nil {
		t.Fatal(err)
	}
	restored, err := data.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Missing || restored.Policy.Enabled || restored.RuntimeState != "identity_changed" || restored.IdentityGeneration != 2 || restored.NextActionAt != nil {
		t.Fatalf("restored account=%#v", restored)
	}
}

func TestReplaceInventoryPreservesContinuouslyPresentAccountState(t *testing.T) {
	data := openTestStore(t)
	ctx := context.Background()
	input := eligibleInventoryAccount()
	account := insertInventoryAccount(t, data, input)
	next := time.Now().Add(time.Hour).Unix()
	if err := data.SetAccountRuntime(ctx, account.ID, "waiting", "", &next); err != nil {
		t.Fatal(err)
	}

	if err := data.ReplaceInventory(ctx, "conn", []RemoteAccountInput{input}); err != nil {
		t.Fatal(err)
	}
	unchanged, err := data.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Missing || unchanged.RuntimeState != "waiting" || unchanged.NextActionAt == nil || *unchanged.NextActionAt != next {
		t.Fatalf("unchanged account=%#v", unchanged)
	}
}
