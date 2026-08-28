# Version-Only Setup Validation Implementation Plan

**Goal:** Let administrators complete setup after the sub2api Admin API Key and minimum version are verified, while moving account inventory and quota failures into the authenticated application.

**Architecture:** Narrow the shared connection probe to the version endpoint so setup and settings validation never call account or quota APIs. Keep inventory synchronization in the scheduler after setup, and centralize quota-error persistence so failures from one account update only that account while management API authentication failures still pause the whole connection.

**Tech Stack:** Go 1.27, `net/http`, `httptest`, SQLite store, existing Vue 3 panel, GitHub Actions cloud builds

---

### Task 1: Make the sub2api probe version-only

**Files:**
- Modify: `internal/sub2api/quota_test.go`
- Modify: `internal/sub2api/client.go`

- **Step 1: Write the failing probe test**

Add a test whose fake upstream implements only the version endpoint and counts every unexpected request:

```go
func TestProbeValidatesVersionWithoutLoadingAccounts(t *testing.T) {
	var unexpected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/system/version" {
			unexpected.Add(1)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		writeEnvelope(t, w, map[string]any{"version": MinimumVersion})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin-secret", true, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	version, err := client.Probe(context.Background())
	if err != nil || version != MinimumVersion {
		t.Fatalf("version=%q err=%v", version, err)
	}
	if unexpected.Load() != 0 {
		t.Fatalf("probe made %d non-version requests", unexpected.Load())
	}
}
```

Add table cases showing that an upstream 401 and a version below `0.1.183` are rejected.

- **Step 2: Verify the test fails in cloud-oriented source review**

The desired two-value `Probe` signature does not match the current three-value signature, and the current implementation requests accounts. Do not compile locally per project requirement.

- **Step 3: Implement the minimal probe**

Change the client probe to:

```go
func (c *Client) Probe(ctx context.Context) (string, error) {
	version, err := c.Version(ctx)
	if err != nil {
		return "", err
	}
	if !versionAtLeast(version, MinimumVersion) {
		return version, &APIError{Kind: ErrorSchema, Message: "sub2api " + version + " is older than required " + MinimumVersion}
	}
	return version, nil
}
```

Do not call `Accounts`, `Quota`, `Models`, or `TestAccount` from this method.

- **Step 4: Format the changed Go files**

Run:

```powershell
gofmt -w internal/sub2api/client.go internal/sub2api/quota_test.go
```

Expected: files format cleanly without a local build.

### Task 2: Use version-only validation for setup and connection settings

**Files:**
- Create: `internal/httpapi/setup_test.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/handlers.go`

- **Step 1: Write a setup handler regression test**

Create a real temporary store, set a setup token, and use an upstream that accepts only the version endpoint:

```go
func TestSetupCompletesWithoutLoadingAccountsOrQuota(t *testing.T) {
	var unexpected atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/system/version" {
			unexpected.Add(1)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		writeTestEnvelope(t, w, map[string]any{"version": sub2api.MinimumVersion})
	}))
	defer upstream.Close()

	data := openHTTPTestStore(t)
	const token = "setup-token"
	if err := data.SetSetupToken(context.Background(), secure.HashToken(token), time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	automation := scheduler.New(data, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := &Server{store: data, scheduler: automation, setupToken: token}
	body := fmt.Sprintf(`{"username":"admin","password":"long-test-password","base_url":%q,"api_key":"admin-secret","global_model":"gpt-text","sync_interval_seconds":300,"reset_grace_seconds":30,"max_retries":3,"retry_base_seconds":30,"request_timeout_seconds":90,"max_concurrency":4,"allow_private_http":true}`, upstream.URL)
	request := httptest.NewRequest(http.MethodPost, "/api/setup/complete", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Setup-Token", token)
	recorder := httptest.NewRecorder()

	server.setupComplete(recorder, request)
	if recorder.Code != http.StatusCreated || unexpected.Load() != 0 {
		t.Fatalf("status=%d unexpected=%d body=%s", recorder.Code, unexpected.Load(), recorder.Body.String())
	}
	complete, err := data.IsSetupComplete(context.Background())
	if err != nil || !complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
}
```

The test helper must use `secure.NewBox(make([]byte, 32))`, `store.Open`, and `t.TempDir`, matching existing store tests. Decode the success body and assert `data.version` exists while `data.accounts_found` does not.

- **Step 2: Narrow the HTTP API helper**

Change the shared helper to:

```go
func probe(ctx context.Context, settings store.Settings) (string, error) {
	client, err := sub2api.NewClient(settings.BaseURL, settings.APIKey, settings.AllowPrivateHTTP, time.Duration(settings.RequestTimeoutSeconds)*time.Second)
	if err != nil {
		return "", err
	}
	return client.Probe(ctx)
}
```

Remove the parent-account quota loop entirely.

- **Step 3: Update all probe callers**

In `setupComplete`, `putSettings`, and `testSettings`, consume only `(version, err)`. Setup and settings-test responses return:

```go
map[string]any{"version": version}
```

Settings save continues to validate before persistence, rotate the connection UUID when the URL changes, and wake the scheduler afterward.

- **Step 4: Format the HTTP API files**

Run:

```powershell
gofmt -w internal/httpapi/server.go internal/httpapi/handlers.go internal/httpapi/setup_test.go
```

Expected: no formatting diff remains.

### Task 3: Persist quota failures as account-level state

**Files:**
- Create: `internal/scheduler/quota_error_test.go`
- Modify: `internal/scheduler/scheduler.go`

- **Step 1: Add a fake remote and temporary scheduler store**

Implement the existing `Remote` interface with configurable `accountsErr` and `quotaErr`. Create store settings with `CompleteSetup`, insert one eligible OpenAI OAuth account with `ReplaceInventory`, and enable it with `SetPolicy`.

- **Step 2: Write account-level authentication tests**

For `ProcessAccount`, return:

```go
&sub2api.APIError{Kind: sub2api.ErrorAuth, StatusCode: http.StatusUnauthorized, Message: "upstream OAuth token expired"}
```

Assert:

```go
if automation.Status().GlobalPause {
	t.Fatal("single-account quota auth failure paused the whole scheduler")
}
updated, _ := data.GetAccount(ctx, account.ID)
if updated.RuntimeState != "paused" || !strings.Contains(updated.LastError, "OAuth token expired") {
	t.Fatalf("account=%#v", updated)
}
```

Add a `RefreshQuota` schema-error case asserting `runtime_state="attention"` and a persisted `last_error`, even though the HTTP operation returns the remote error.

- **Step 3: Preserve connection-level global pause**

Make the fake `Accounts` call return an `ErrorAuth`, call `Sync`, and assert `Status().GlobalPause` is true. This protects the distinction between management API authentication and one account's quota authentication.

- **Step 4: Centralize quota-error persistence**

Replace `handleQuotaError` with a helper used by `ProcessAccount`, `RefreshQuota`, and ambiguous-attempt reconciliation:

```go
func (s *Scheduler) recordQuotaError(ctx context.Context, account store.Account, quotaErr error, now time.Time) error {
	var apiErr *sub2api.APIError
	if errors.As(quotaErr, &apiErr) {
		switch apiErr.Kind {
		case sub2api.ErrorAuth, sub2api.ErrorCompliance:
			return s.store.SetAccountRuntime(ctx, account.ID, "paused", quotaErr.Error(), nil)
		case sub2api.ErrorNotFound:
			return s.store.SetAccountRuntime(ctx, account.ID, "missing", quotaErr.Error(), nil)
		}
	}
	if sub2api.IsTransient(quotaErr) {
		next := now.Add(15 * time.Second).Unix()
		return s.store.SetAccountRuntime(ctx, account.ID, "quota_retry", quotaErr.Error(), &next)
	}
	return s.store.SetAccountRuntime(ctx, account.ID, "attention", quotaErr.Error(), nil)
}
```

If persistence succeeds, `RefreshQuota` returns the original remote error so the drawer also displays immediate feedback. Remove quota-path calls to `handleGlobalError`; it remains used by inventory synchronization.

- **Step 5: Format the scheduler files**

Run:

```powershell
gofmt -w internal/scheduler/scheduler.go internal/scheduler/quota_error_test.go
```

Expected: files format cleanly.

### Task 4: Update documentation and run local non-compiling checks

**Files:**
- Modify: `README.md`

- **Step 1: Correct the compatibility and setup wording**

State that first setup and settings connection tests validate only the Admin API Key through the version endpoint. Explain that account inventory sync happens after entry and quota capability/errors are checked per account inside the panel. Keep quota capability listed as mandatory for automatic activation, not for completing setup.

- **Step 2: Search for stale behavior claims**

Run:

```powershell
rg -n "首次设置|quota|accounts_found|探测版本|能力" README.md internal web
```

Expected: no current documentation claims that setup reads quota or requires a healthy OAuth account.

- **Step 3: Run permitted local checks**

Run:

```powershell
git diff --check
git status --short
```

Do not run Go, Vue, or Playwright compilation locally. Review every changed line against the approved design.

- **Step 4: Commit the implementation**

```bash
git add README.md internal/httpapi internal/scheduler internal/sub2api
git commit -m "fix: defer account quota checks until after setup"
```

### Task 5: Cloud verification and release

**Files:**
- No source changes unless CI exposes a defect

- **Step 1: Push main**

```bash
git push origin main
```

- **Step 2: Monitor GitHub Actions**

Wait for frontend typecheck/Vitest/Vite, Go format/module/vet/unit/race, production binary Playwright, and lifecycle checks. If any step fails, inspect logs, make the smallest corrective change, commit, push, and repeat until `main` is green.

- **Step 3: Publish the next patch tag**

After `main` is green, create annotated tag `v0.1.3`, push it, and wait for both `verify` and `release` jobs.

- **Step 4: Verify public artifacts**

Download `SHA256SUMS`, `sub2api-auto5h-linux-amd64.tar.gz`, and `sub2api-auto5h-linux-arm64.tar.gz` from the public release. Verify both archive hashes and confirm the archives contain the binary, systemd unit, install/uninstall scripts, env example, README, LICENSE, SECURITY, and third-party notices.

- **Step 5: Acceptance result**

Report the public release URL and upgrade command. State that initialization now succeeds with a valid Admin API Key even when an OAuth account's quota returns 401, while that account's error appears inside the panel and does not pause unrelated accounts.
