package store

import (
	"context"
	"database/sql"
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

func TestUpgradeMigrationDisablesDirectWakeupAndInvalidatesLegacyResults(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	box, err := secure.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"001_initial.sql", "002_answer_assessment.sql"} {
		contents, readErr := migrationFiles.ReadFile("migrations/" + version)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err := db.ExecContext(ctx, string(contents)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	ciphertext, err := box.Seal([]byte("admin-secret"), settingsAAD)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if _, err := db.ExecContext(ctx, `UPDATE app_meta SET setup_complete=1, updated_at=? WHERE id=1`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO settings (
      id, connection_uuid, base_url, api_key_cipher, global_model, sync_interval_seconds,
      reset_grace_seconds, max_retries, retry_base_seconds, request_timeout_seconds,
      max_concurrency, allow_private_http, updated_at
    ) VALUES (1, 'connection', 'https://sub2api.example.com', ?, 'gpt-text', 300, 30, 3, 30, 90, 4, 0, ?)`, ciphertext, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO remote_accounts (
      id, connection_uuid, remote_id, remote_created_at, identity_hash, name, email, plan_type,
      platform, account_type, status, schedulable, missing, eligible, quota_state, runtime_state,
      last_seen_at, created_at, updated_at, last_answer_status, last_answer_text, last_answer_at
    ) VALUES ('account', 'connection', 7, '2026-08-27T00:00:00Z', 'identity', 'plus',
      'plus@example.com', 'plus', 'openai', 'oauth', 'active', 1, 0, 1, 'valid',
      'success_unverified', ?, ?, ?, 'normal', '21', ?)`, now, now, now, now-30); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO account_policies (account_id, enabled, enable_generation, updated_at) VALUES ('account', 1, 1, ?)`, now); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id       string
		accepted int64
	}{{"old-cycle", now - 120}, {"young-cycle", now - 30}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO cycles (
        id, account_id, identity_generation, cycle_key, kind, due_at, status, attempt_count,
        accepted_at, reason, created_at, updated_at
      ) VALUES (?, 'account', 1, ?, 'bootstrap', ?, 'success_unverified', 1, ?, '', ?, ?)`,
			item.id, item.id, item.accepted, item.accepted, item.accepted, item.accepted); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO attempts (
      id, cycle_id, attempt_number, started_at, ended_at, outcome, answer_status, answer_text
    ) VALUES ('attempt', 'old-cycle', 1, ?, ?, 'accepted', 'abnormal', '29')`, now-121, now-120); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := Open(ctx, path, box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	settings, err := data.GetSettings(ctx)
	if err != nil || settings.DirectWakeupEnabled {
		t.Fatalf("settings=%#v err=%v", settings, err)
	}
	account, err := data.GetAccount(ctx, "account")
	if err != nil || account.LastAnswerStatus != "legacy_invalid" || account.LastAnswerText != "21" || account.RuntimeState != "direct_disabled" {
		t.Fatalf("account=%#v err=%v", account, err)
	}
	cycles, err := data.ListCycles(ctx, "account", 10)
	if err != nil {
		t.Fatal(err)
	}
	cycleStatus := make(map[string]string, len(cycles))
	for _, cycle := range cycles {
		cycleStatus[cycle.ID] = cycle.Status
	}
	if cycleStatus["old-cycle"] != "accepted_unverified" || cycleStatus["young-cycle"] != "verifying" {
		t.Fatalf("cycle statuses=%#v", cycleStatus)
	}
	attempts, err := data.ListAttempts(ctx, "old-cycle")
	if err != nil || len(attempts) != 1 || attempts[0].AnswerStatus != "legacy_invalid" || attempts[0].AnswerText != "29" {
		t.Fatalf("attempts=%#v err=%v", attempts, err)
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
	dispatching, err := data.GetAccount(ctx, account.ID)
	if err != nil || dispatching.RuntimeState != "dispatching" || dispatching.NextActionAt != nil {
		t.Fatalf("dispatching account=%#v err=%v", dispatching, err)
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

func TestPolicyDisableWinsOverInFlightAttempt(t *testing.T) {
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
	if err := data.SetPolicy(ctx, account.ID, Policy{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	ended := started + 1
	deadline := ended + 60
	if err := data.FinishAttempt(ctx, claimed, started, ended, AttemptResult{
		Outcome: "accepted", Status: "verifying", AnswerStatus: "normal", AnswerText: "21",
		RequestModel: "gpt-text", TransportPath: "direct", AnswerSource: "official_codex_sse",
		VerificationDeadlineAt: &deadline,
	}); err != nil {
		t.Fatal(err)
	}

	updated, err := data.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Policy.Enabled || updated.RuntimeState != "disabled" || updated.NextActionAt != nil || updated.VerificationDeadlineAt != nil {
		t.Fatalf("disabled account was overwritten=%#v", updated)
	}
	if updated.LastAnswerStatus != "normal" || updated.LastAnswerText != "21" {
		t.Fatalf("completed request result was not retained=%#v", updated)
	}
	cycles, err := data.ListCycles(ctx, account.ID, 10)
	if err != nil || len(cycles) != 1 || cycles[0].Status != "verifying" {
		t.Fatalf("cycles=%#v err=%v", cycles, err)
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
