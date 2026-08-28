package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

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
