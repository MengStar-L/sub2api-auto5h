# Direct Codex Wakeup Implementation Plan

**Goal:** Replace the ineffective sub2api account-test activation path with an opt-in, single-account, official ChatGPT Codex Responses request that sends the candy puzzle, records the actual bounded reply, uses response headers plus read-only quota checks as independent five-hour evidence, and always leaves verification in a terminal state within 60 seconds.

**Architecture:** Keep sub2api as the account inventory, quota, model, credential-export, and token-refresh authority. The scheduler keeps the existing preflight, cycle key, lease, retry, and reconciliation rules, but obtains one account's temporary OAuth/proxy material only after claiming a due cycle and passes it to a new `internal/codex` client. That client has a production-fixed upstream, strict proxy reuse, bounded SSE parsing, response-header quota parsing, and sanitized errors. SQLite stores only request/result metadata and bounded model text; it never stores OAuth tokens, proxy secrets, or raw SSE. Direct wakeup is globally disabled after both fresh setup and upgrade until the administrator explicitly acknowledges the risk in Settings.

**Tech Stack:** Go 1.27, `net/http`, `database/sql`, `modernc.org/sqlite`, Vue 3, TypeScript 6, Vitest, Playwright, GitHub Actions, Linux amd64/arm64 static releases

---

## Execution Constraints

- Do not run `go build`, `go test`, `go vet`, Vue type checking, Vitest, Vite, or Playwright on the local workstation.
- Local verification is limited to `gofmt`, `gofmt -l`, `git diff --check`, source inspection, and Git metadata commands.
- Use a Draft PR so GitHub Actions provides the red/green test cycle. Do not weaken a failing test to make the workflow green.
- Keep the release build free of configurable ChatGPT upstream URLs. A loopback endpoint override may exist only behind the compile-time `e2e` build tag and must not be present in release builds.
- Never print, persist, or include in an error: access token, refresh token, ID token, proxy password, proxy URL userinfo, raw exported account JSON, or raw SSE.

### Task 1: Establish the cloud-tested implementation branch

**Files:**

- Verify: `.github/workflows/ci.yml`
- Verify: `.openteams/specs/2026-08-29-direct-codex-wakeup-design.html`
- Verify: `.openteams/plans/2026-08-29-direct-codex-wakeup.md`

**Step 1: Publish the approved documentation commit**

The local `main` branch contains the approved design commit and this plan. After the user approves this plan:

```powershell
git status --short --branch
git push origin main
$docsRun = gh run list --branch main --workflow "CI and release" --limit 1 --json databaseId --jq '.[0].databaseId'
gh run watch $docsRun --exit-status
```

Expected: `main` is green before implementation begins.

**Step 2: Create a feature branch and Draft PR**

```powershell
git switch -c codex/direct-codex-wakeup
git push -u origin codex/direct-codex-wakeup
gh pr create --draft --base main --head codex/direct-codex-wakeup --title "feat: use official Codex wakeup" --body "Implements the approved C1 direct-wakeup design for v0.1.6."
```

Expected: the Draft PR starts the existing pull-request workflow without changing release behavior.

### Task 2: Add the opt-in and result metadata migration

**Files:**

- Create: `internal/store/migrations/003_direct_codex_wakeup.sql`
- Modify: `internal/store/models.go`
- Modify: `internal/store/store.go`
- Modify: `internal/store/accounts.go`
- Modify: `internal/store/cycles.go`
- Modify: `internal/store/store_test.go`

**Step 1: Write migration and store regressions first**

Add tests covering a fresh database and an upgrade-shaped database created from migrations 001 and 002. The upgrade test must insert:

- settings with no direct-wakeup column;
- an enabled account with `last_answer_status='normal'` and answer `21`;
- an accepted attempt with `answer_status='abnormal'` and answer `29`;
- one `success_unverified` cycle/account older than 60 seconds;
- one `success_unverified` cycle/account younger than 60 seconds.

After reopening through `store.Open`, assert:

```go
if settings.DirectWakeupEnabled {
	t.Fatal("direct wakeup must default off after upgrade")
}
if account.LastAnswerStatus != "legacy_invalid" || account.LastAnswerText != "21" {
	t.Fatalf("legacy answer was not preserved and invalidated: %#v", account)
}
if oldCycle.Status != "accepted_unverified" {
	t.Fatalf("old verification did not converge: %#v", oldCycle)
}
if youngCycle.Status != "verifying" {
	t.Fatalf("young verification should be recovered by the scheduler: %#v", youngCycle)
}
```

Add a `FinishAttempt` test proving the account and attempt receive the same safe metadata while the account's previous answer is not cleared by later failed attempts.

**Step 2: Commit and push the red store tests**

```powershell
gofmt -w internal/store/store_test.go
git diff --check
git add -- internal/store/store_test.go
git commit -m "test: define direct wakeup persistence"
git push
```

Watch the Draft PR workflow and confirm it fails because the new schema/model fields do not exist. Record the failing run URL in the PR, then implement the schema.

**Step 3: Add the forward-only migration**

Create `003_direct_codex_wakeup.sql` with additive columns:

```sql
ALTER TABLE settings ADD COLUMN direct_wakeup_enabled INTEGER NOT NULL DEFAULT 0;

ALTER TABLE attempts ADD COLUMN request_model TEXT NOT NULL DEFAULT '';
ALTER TABLE attempts ADD COLUMN transport_path TEXT NOT NULL DEFAULT '';
ALTER TABLE attempts ADD COLUMN answer_source TEXT NOT NULL DEFAULT '';
ALTER TABLE attempts ADD COLUMN quota_evidence TEXT NOT NULL DEFAULT '';
ALTER TABLE attempts ADD COLUMN terminal_summary TEXT NOT NULL DEFAULT '';

ALTER TABLE remote_accounts ADD COLUMN last_request_model TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN last_transport_path TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN last_answer_source TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN last_quota_evidence TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN last_terminal_summary TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN verification_deadline_at INTEGER;
```

Then migrate history without creating work:

```sql
UPDATE attempts
SET answer_status = 'legacy_invalid',
    answer_source = 'sub2api_test_v0.1.5',
    terminal_summary = 'v0.1.5 did not forward the configured puzzle'
WHERE answer_status <> '';

UPDATE remote_accounts
SET last_answer_status = 'legacy_invalid',
    last_answer_source = 'sub2api_test_v0.1.5',
    last_terminal_summary = 'v0.1.5 did not forward the configured puzzle'
WHERE last_answer_status <> '';

UPDATE cycles
SET status = 'accepted_unverified',
    lease_until = NULL,
    next_attempt_at = NULL,
    reason = '旧版请求成功，额度未确认',
    updated_at = unixepoch()
WHERE status = 'success_unverified'
  AND COALESCE(accepted_at, updated_at) <= unixepoch() - 60;

UPDATE cycles
SET status = 'verifying',
    lease_until = NULL,
    next_attempt_at = NULL,
    reason = '旧版请求成功，等待额度核验恢复',
    updated_at = unixepoch()
WHERE status = 'success_unverified';

UPDATE remote_accounts
SET runtime_state = 'direct_disabled',
    next_action_at = NULL,
    verification_deadline_at = NULL,
    last_error = '',
    updated_at = unixepoch()
WHERE id IN (SELECT account_id FROM account_policies WHERE enabled = 1)
  AND runtime_state <> 'dispatching';
```

Do not edit migrations 001 or 002. The cycle history remains `accepted_unverified`; the account surface becomes `direct_disabled` because the new global opt-in is off.

**Step 4: Extend store contracts and atomic writes**

Add `DirectWakeupEnabled bool` to `store.Settings`. Add these fields to `Attempt` and `AttemptResult` with matching JSON names:

```go
RequestModel           string `json:"request_model"`
TransportPath          string `json:"transport_path"`
AnswerSource           string `json:"answer_source"`
QuotaEvidence          string `json:"quota_evidence"`
TerminalSummary        string `json:"terminal_summary"`
```

For the account model use `LastRequestModel`, `LastTransportPath`, `LastAnswerSource`, `LastQuotaEvidence`, `LastTerminalSummary`, and `VerificationDeadlineAt *int64`. Extend all SELECT/Scan/INSERT statements in the same order.

Update `CompleteSetup`, `GetSettings`, and `UpdateSettings` to persist `direct_wakeup_enabled`. In `UpdateSettings`, compare old and new values inside the settings transaction:

- false to true: set eligible, policy-enabled, non-missing accounts to `pending_check` with `next_action_at=now`;
- true to false: set non-dispatching policy-enabled accounts to `direct_disabled` with `next_action_at=NULL`;
- never interrupt an HTTP request already in `dispatching`; its result may finish, but no subsequent request can start while disabled.

Update `FinishAttempt` to write safe metadata to the attempt and latest-account columns in the same transaction. Empty answer fields from failures must not overwrite the last explicit-success answer.

**Step 5: Add conditional verification finalization queries**

Add:

```go
func (s *Store) ListVerifyingCycles(ctx context.Context) ([]Cycle, error)
func (s *Store) FinalizeVerification(ctx context.Context, cycleID string, now, fallbackNext int64) (bool, error)
```

`FinalizeVerification` updates only `cycles.status='verifying'`, changes it to `accepted_unverified`, clears leases, and preserves the request-time fallback next action. It updates the account only when its current runtime state is still `verifying`; a later policy disable or identity change must win.

**Step 6: Format, push, and require the store job to turn green**

```powershell
gofmt -w internal/store/models.go internal/store/store.go internal/store/accounts.go internal/store/cycles.go internal/store/store_test.go
gofmt -l internal/store/models.go internal/store/store.go internal/store/accounts.go internal/store/cycles.go internal/store/store_test.go
git diff --check
git add -- internal/store/migrations/003_direct_codex_wakeup.sql internal/store/models.go internal/store/store.go internal/store/accounts.go internal/store/cycles.go internal/store/store_test.go
git commit -m "feat: persist direct wakeup state"
git push
```

Expected: the migration/store tests pass in GitHub Actions and `gofmt -l` prints nothing locally.

### Task 3: Export one account's temporary OAuth and proxy material

**Files:**

- Create: `internal/secure/redact.go`
- Create: `internal/secure/redact_test.go`
- Create: `internal/sub2api/credentials.go`
- Create: `internal/sub2api/credentials_test.go`
- Modify: `internal/sub2api/client.go`
- Modify: `internal/sub2api/types.go`

**Step 1: Define failing export, refresh, identity, and redaction tests**

Use `httptest.Server` to assert the exact management calls:

```text
GET /api/v1/admin/accounts/data?ids=7&include_proxies=true
POST /api/v1/admin/openai/accounts/7/refresh
```

Cover:

- exactly one OpenAI OAuth export with access token, expiry, ChatGPT account ID, email, optional user agent, and no proxy;
- `proxy_key` resolving to `http`, `https`, `socks5`, and `socks5h` proxy objects;
- zero accounts, multiple accounts, missing access token, missing ChatGPT account ID, unknown proxy key, invalid proxy port, and unsupported proxy protocol;
- identity hash mismatch when the local account already has a ChatGPT identity;
- normalized-email mismatch when the local identity hash is empty;
- a successful refresh followed by a second export;
- refresh 401/403/404/423 and malformed JSON classification;
- redaction of Bearer strings, JWT-shaped values, JSON token/password fields, and proxy URL userinfo.

The redaction unit test must assert every supported token/proxy representation is removed. The end-to-end durable-artifact scan belongs to Task 5, after the real scheduler error path exists.

**Step 2: Push the red credential tests**

```powershell
gofmt -w internal/secure/redact_test.go internal/sub2api/credentials_test.go
git diff --check
git add -- internal/secure/redact_test.go internal/sub2api/credentials_test.go
git commit -m "test: define temporary credential export"
git push
```

Expected cloud failure: missing redactor and credential-export APIs.

**Step 3: Add narrow activation-material types**

Add types that contain only what the direct request needs:

```go
type ActivationMaterial struct {
	AccessToken   string
	ExpiresAt     time.Time
	ChatGPTID     string
	Email         string
	IdentityHash  string
	UserAgent     string
	Proxy         *ActivationProxy
}

type ActivationProxy struct {
	Protocol string
	Host     string
	Port     int
	Username string
	Password string
}
```

Do not include refresh token or ID token in `ActivationMaterial`, even if present in the export response.

**Step 4: Implement single-ID export and identity validation**

Build the query with `url.Values`, require exactly one returned account, require `platform=openai` and `type=oauth`, and resolve only the referenced proxy. Validate identity as follows:

1. when local `IdentityHash` is non-empty, the exported ChatGPT identity hash must exist and match;
2. otherwise, both normalized local/exported emails must be non-empty and equal;
3. any mismatch is `ErrorCompliance` and must not expose identity values in the message.

Parse `expires_at` as RFC3339. Return `ErrorSchema` for missing or badly typed required fields. Keep `request` response limits and redirect rejection.

Implement `RefreshAccessToken(ctx, accountID)` through sub2api. The scheduler, not this client, decides when refresh is allowed; auto5h never consumes the exported refresh token itself.

**Step 5: Implement centralized sanitization**

`secure.Redact(string)` replaces recognized secrets with `[REDACTED]`, caps the result to 500 runes, and never echoes a full proxy URL. Apply it before all scheduler/store/audit error writes. Protocol errors in the sub2api and Codex packages should be safe by construction and contain only status, allowlisted error code, and a generic message.

**Step 6: Format, push, and require green credential tests**

```powershell
gofmt -w internal/secure/redact.go internal/secure/redact_test.go internal/sub2api/client.go internal/sub2api/types.go internal/sub2api/credentials.go internal/sub2api/credentials_test.go
git diff --check
git add -- internal/secure/redact.go internal/secure/redact_test.go internal/sub2api/client.go internal/sub2api/types.go internal/sub2api/credentials.go internal/sub2api/credentials_test.go
git commit -m "feat: export temporary activation credentials"
git push
```

Expected: GitHub unit/race tests pass and fixture tokens do not appear in durable artifacts.

### Task 4: Build the fixed official Codex client

**Files:**

- Create: `internal/codex/types.go`
- Create: `internal/codex/client.go`
- Create: `internal/codex/endpoint_prod.go`
- Create: `internal/codex/endpoint_e2e.go`
- Create: `internal/codex/sse.go`
- Create: `internal/codex/ratelimit.go`
- Create: `internal/codex/client_test.go`
- Create: `internal/codex/sse_test.go`
- Create: `internal/codex/ratelimit_test.go`
- Delete: `internal/sub2api/sse.go`
- Modify: `internal/sub2api/quota_test.go`
- Modify: `.github/workflows/ci.yml`

**Step 1: Write failing request, proxy, SSE, and quota-header tests**

Test the production constructor separately from the same-package test constructor. Assert the production endpoint is exactly:

```text
https://chatgpt.com/backend-api/codex/responses
```

Assert the JSON request contains the fixed candy question in `input[0].content[0].text`, the numeric-only instruction, configured model, `store=false`, and `stream=true`. Do not add a user-facing reasoning-effort setting.

Assert these request headers:

```text
Authorization: Bearer <temporary access token>
ChatGPT-Account-Id: <temporary account id>
Accept: text/event-stream
Content-Type: application/json
OpenAI-Beta: responses=experimental
Originator: codex-tui
User-Agent: exported value or the pinned official-compatible fallback
```

Pin the fallback pair from the reviewed cockpit-tools snapshot in source and cover it with a test, so later upstream changes are explicit code reviews rather than silent behavior changes.

Add proxy tests for direct, HTTP, HTTPS, SOCKS5, and SOCKS5H transports. A configured proxy that rejects or cannot connect must result in zero direct hits on the fake upstream. TLS tests may add a test root CA to the unexported test constructor; production must always use normal certificate validation.

Add rate-header table tests that swap primary and secondary and identify windows only by:

- `window-minutes=300` for five hours;
- `window-minutes=10080` for seven days.

Reject missing members, duplicate recognized windows, negative/out-of-range percentages, invalid reset seconds, bad numeric types, and unknown-only structures.

Add SSE tests for:

- split `response.output_text.delta` events;
- `response.output_text.done` text;
- full output text in `response.completed`;
- `response.done` equivalent success;
- explicit error terminal;
- successful terminal with empty reply;
- malformed JSON;
- 1 MiB total limit and 2000-rune saved-reply limit;
- EOF or `[DONE]` without a successful terminal.

**Step 2: Push the red Codex tests**

```powershell
gofmt -w internal/codex/client_test.go internal/codex/sse_test.go internal/codex/ratelimit_test.go
git diff --check
git add -- internal/codex/client_test.go internal/codex/sse_test.go internal/codex/ratelimit_test.go
git commit -m "test: define official Codex transport"
git push
```

Expected cloud failure: package implementation is missing.

**Step 3: Implement production-fixed and e2e-only endpoints**

`endpoint_prod.go` has `//go:build !e2e` and returns only the fixed HTTPS URL. `endpoint_e2e.go` has `//go:build e2e`, reads `SUB2API_AUTO5H_E2E_CODEX_URL`, and rejects anything except an absolute loopback HTTP origin. The release workflow must never build with `e2e`.

Change only the CI browser-test binary command to:

```yaml
run: go build -tags=e2e -trimpath -ldflags "-s -w -X main.version=e2e -X main.commit=${GITHUB_SHA}" -o build/e2e/sub2api-auto5h ./cmd/sub2api-auto5h
```

Normal unit, race, and release builds keep the production endpoint.

**Step 4: Implement strict transport construction**

Create one `http.Transport` per activation from the temporary proxy material. Use `http.ProxyURL`; Go's standard transport supports `http`, `https`, `socks5`, and `socks5h`. Set bounded dial, TLS handshake, response-header, idle-connection, and total request timeouts. Reject redirects and leave TLS verification enabled.

Record only `direct`, `proxy:http`, `proxy:https`, `proxy:socks5`, or `proxy:socks5h` as `TransportPath`. Never record proxy host, port, username, or password.

**Step 5: Implement request, terminal, error, and reply parsing**

Define a result independent from activation success assessment:

```go
type Result struct {
	HTTPStatus     int
	Reply          string
	Terminal       string
	TransportPath  string
	RateLimits     RateLimits
}
```

Only HTTP 2xx plus `response.completed` or equivalent `response.done` is explicit request success. Select reply text in this order: completed response output, output-text done, accumulated deltas. Empty reply remains a successful result. Bound raw SSE read to 1 MiB and stored reply to 2000 Unicode characters.

For non-2xx responses, extract only allowlisted error `code` and a sanitized bounded message. Return typed kinds for 401, 403, 404, 429, transient transport/5xx, and schema/terminal failures. Attach parsed rate limits to the typed 429 error without treating the request as accepted.

**Step 6: Remove the ineffective sub2api test transport**

Delete `internal/sub2api/sse.go`, remove `TestResult`, `TestAccount`, and `/test` assertions, and keep account pagination/quota/model tests in `quota_test.go`. There must be no runtime call to `/api/v1/admin/accounts/:id/test` after this task.

**Step 7: Format, push, and require the Codex package to turn green**

```powershell
$codexGo = rg --files internal/codex -g '*.go'
gofmt -w $codexGo internal/sub2api/types.go internal/sub2api/quota_test.go
gofmt -l $codexGo internal/sub2api/types.go internal/sub2api/quota_test.go
git diff --check
git add -- .github/workflows/ci.yml internal/codex internal/sub2api/sse.go internal/sub2api/types.go internal/sub2api/quota_test.go
git commit -m "feat: add official Codex activation client"
git push
```

Expected: request/proxy/SSE/header tests pass under normal and race jobs; release compilation still uses the fixed production endpoint.

### Task 5: Route scheduler attempts through temporary credentials and Codex

**Files:**

- Create: `internal/scheduler/activation.go`
- Modify: `internal/scheduler/scheduler.go`
- Modify: `internal/scheduler/wakeup_test.go`
- Modify: `internal/scheduler/quota_error_test.go`
- Modify: `cmd/sub2api-auto5h/main.go`

**Step 1: Replace fake `/test` tests with failing direct-activation state tests**

Extend `Remote` with:

```go
ActivationMaterial(context.Context, int64, string, string) (sub2api.ActivationMaterial, error)
RefreshAccessToken(context.Context, int64) error
```

Introduce an injectable scheduler boundary:

```go
type Activator interface {
	Activate(context.Context, codex.Request) (codex.Result, error)
}
```

`New` uses the real Codex service; an unexported `newWithActivator` supplies a scripted fake in scheduler tests.

Add tests for:

- global direct wakeup disabled: quota may be read, but export, refresh, and activation call counts are all zero;
- a due idle account exports exactly its own ID and activates once;
- exported identity mismatch enters `attention` before any official request;
- token expiring within two minutes refreshes through sub2api once, re-exports once, then activates;
- first official 401 refreshes/re-exports/retries once inside the same leased attempt;
- second 401 is `attention` and does not enter ordinary backoff;
- explicit success with replies `21`, `29`, `答案是21`, and empty records `normal`, `abnormal`, `abnormal`, and `no_answer`, while all remain accepted with one cycle success;
- explicit success never retries even if quota evidence is absent;
- 429 with a valid 5h/7d header waits for the matching reset; malformed 429 evidence is `attention`;
- timeout/connection failure checks sub2api quota before the existing 30/60/120-second retry policy;
- proxy/credential/schema errors fail closed;
- two concurrent `ProcessAccount` calls result in one lease and one official request.
- a secret-bearing export/proxy/upstream failure leaves its sentinel absent from the SQLite database, WAL file, captured logger, attempts, and audit events.

**Step 2: Push the red scheduler tests**

```powershell
gofmt -w internal/scheduler/wakeup_test.go internal/scheduler/quota_error_test.go
git diff --check
git add -- internal/scheduler/wakeup_test.go internal/scheduler/quota_error_test.go
git commit -m "test: define direct activation state machine"
git push
```

Expected cloud failure: scheduler still depends on the deleted sub2api test method.

**Step 3: Split activation dispatch from quota scheduling**

Move `sendAttempt`, answer classification, typed activation error handling, and post-error reconciliation into `activation.go`. Preserve `scheduler.go` for inventory, eligibility, due-cycle creation, concurrency, and read-only quota logic.

At the top of `sendAttempt`, before `StartAttempt` and again before credential export, require `settings.DirectWakeupEnabled`. When false, set `direct_disabled` without creating an attempt.

After claiming the lease:

1. export only the target account's material;
2. refresh/re-export once if expiry is at or before `now+120s`;
3. build the direct request with the effective model and fixed prompt;
4. on one 401 only, ask sub2api to refresh, re-export, and retry in the same attempt;
5. drop material references when the call returns; do not cache across attempts.

**Step 4: Separate request success, answer assessment, and quota evidence**

Implement:

```go
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
```

For explicit success:

- set `Outcome="accepted"`, `AnswerSource="official_codex_sse"`, request model, transport path, and safe terminal summary;
- if valid 5h response headers exist, set cycle/account `verified`, use `reset_at + grace`, and ensure the next `reset:<reset_at>` cycle;
- otherwise set `verifying`, `verification_deadline_at=ended+60`, and fallback next action `ended+18000+grace`;
- never route any reply status into retry logic.

For ambiguous failures, retain existing quota reconciliation. Require positive five-hour usage for inferred success. Apply `secure.Redact` before `FinishAttempt` or `AddEvent` receives an error.

**Step 5: Record safe audit stages**

Write events for credential export success/failure, token refresh, official request terminal, header verification, quota reconciliation, and final attention. Metadata may contain account local ID, remote numeric ID, model, transport category, status code, event type, and evidence source. It must not contain exported material, proxy endpoint, request headers, response body, or raw SSE.

**Step 6: Format, push, and require the scheduler tests to turn green**

```powershell
gofmt -w internal/scheduler/activation.go internal/scheduler/scheduler.go internal/scheduler/wakeup_test.go internal/scheduler/quota_error_test.go cmd/sub2api-auto5h/main.go
gofmt -l internal/scheduler/activation.go internal/scheduler/scheduler.go internal/scheduler/wakeup_test.go internal/scheduler/quota_error_test.go cmd/sub2api-auto5h/main.go
git diff --check
git add -- internal/scheduler/activation.go internal/scheduler/scheduler.go internal/scheduler/wakeup_test.go internal/scheduler/quota_error_test.go cmd/sub2api-auto5h/main.go
git commit -m "feat: dispatch wakeups through official Codex"
git push
```

Expected: all direct-disabled, identity, refresh, answer, retry, and lease tests pass in GitHub Actions.

### Task 6: Make verification converge within 60 seconds and recover after restart

**Files:**

- Create: `internal/scheduler/verification.go`
- Modify: `internal/scheduler/scheduler.go`
- Modify: `internal/scheduler/wakeup_test.go`
- Modify: `internal/store/cycles.go`
- Modify: `internal/store/store_test.go`

**Step 1: Add deterministic failing verification tests with a controllable clock**

Replace the current clock fake whose `After` never fires with a manual clock that can advance 10, 30, and 60 seconds. Cover:

- valid five-hour quota at 10 seconds becomes `verified` immediately;
- transient quota errors at all checks become `accepted_unverified` at the 60-second deadline;
- valid but still-idle quota at all checks becomes `accepted_unverified`;
- schema/auth quota failure cannot leave `verifying` indefinitely and records the safe reason;
- restart at 20 seconds resumes the same cycle without a second official request;
- restart after 60 seconds finalizes immediately without export or dispatch;
- disabling an account during verification finalizes the cycle but leaves account runtime `disabled`;
- identity replacement during verification cannot overwrite `identity_changed` or enable a request;
- shutdown cancels goroutines cleanly and startup recovery owns the remaining transition.

**Step 2: Push the red verification tests**

```powershell
gofmt -w internal/scheduler/wakeup_test.go internal/store/store_test.go
git diff --check
git add -- internal/scheduler/wakeup_test.go internal/store/store_test.go
git commit -m "test: require bounded verification recovery"
git push
```

Expected cloud failure: the current verifier exits silently and startup does not resume it.

**Step 3: Implement one verification worker per accepted cycle**

Move verification to `verification.go`. Schedule checks at absolute offsets 10, 30, and 60 seconds from `accepted_at`, not cumulative sleeps. At each check:

- reload account and settings;
- read only sub2api quota;
- if a positive future five-hour window is present, atomically mark the cycle/account `verified`, clear `verification_deadline_at`, persist `quota_evidence="sub2api_quota"`, and ensure the next reset cycle;
- otherwise continue until the absolute deadline.

After the final check, always call `FinalizeVerification`. Set the reason to `请求成功，60 秒内未获得 5h 额度证据`, keep the fallback next action, clear the deadline, and emit `activation_accepted_unverified`.

**Step 4: Resume unfinished verification at scheduler start**

Before the normal due loop begins, call `ListVerifyingCycles`. For every cycle:

- if `accepted_at+60 <= now`, finalize immediately;
- otherwise start the same verifier at the remaining absolute offsets;
- do not create or claim a new attempt;
- use the scheduler wait group so SIGTERM waits for cancellation and no goroutine touches a closed store.

Migration 003 converts younger legacy `success_unverified` rows to `verifying`; startup recovery applies the same deadline logic without resending.

**Step 5: Format, push, and require restart/terminal tests to turn green**

```powershell
gofmt -w internal/scheduler/verification.go internal/scheduler/scheduler.go internal/scheduler/wakeup_test.go internal/store/cycles.go internal/store/store_test.go
git diff --check
git add -- internal/scheduler/verification.go internal/scheduler/scheduler.go internal/scheduler/wakeup_test.go internal/store/cycles.go internal/store/store_test.go
git commit -m "fix: converge activation verification"
git push
```

Expected: no test can observe `verifying` after its 60-second deadline, and restart never produces a duplicate activation.

### Task 7: Expose explicit global consent through the API

**Files:**

- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/handlers.go`
- Modify: `internal/httpapi/setup_test.go`
- Modify: `internal/httpapi/handlers_test.go`

**Step 1: Add failing API consent and non-activation tests**

Cover:

- setup always stores `direct_wakeup_enabled=false` and performs only version/admin-key capability probing;
- GET settings returns the new flag without any secret;
- false-to-true PUT without `direct_wakeup_acknowledged=true` returns HTTP 409 and `DIRECT_WAKEUP_ACK_REQUIRED`;
- false-to-true PUT with acknowledgment succeeds and wakes the scheduler;
- already-enabled settings can be edited without repeating acknowledgment;
- disabling requires no acknowledgment and stops new due work;
- connection test never exports credentials, refreshes token, queries account quota, or calls ChatGPT;
- manual run while the global switch is off returns HTTP 409 and `DIRECT_WAKEUP_DISABLED` before the state machine.

**Step 2: Push the red handler tests**

```powershell
gofmt -w internal/httpapi/setup_test.go internal/httpapi/handlers_test.go
git diff --check
git add -- internal/httpapi/setup_test.go internal/httpapi/handlers_test.go
git commit -m "test: require direct wakeup consent"
git push
```

Expected cloud failure: settings JSON and validation do not contain the new fields.

**Step 3: Extend settings request/view without changing setup behavior**

Add:

```go
DirectWakeupEnabled      bool `json:"direct_wakeup_enabled"`
DirectWakeupAcknowledged bool `json:"direct_wakeup_acknowledged"`
```

The acknowledgment is request-only and is never persisted. `settingsView` returns `direct_wakeup_enabled` and `direct_wakeup_available=true` when normal settings can be decrypted. Setup constructs `store.Settings` with the zero-value false and must continue probing only `/api/v1/admin/system/version`.

In `putSettings`, require acknowledgment only on a false-to-true transition. Log `direct_wakeup_enabled` or `direct_wakeup_disabled` without credential details, save the setting transactionally, and call `scheduler.Wake()`.

**Step 4: Enforce the switch for manual runs**

Check current settings in `runAccount` before the existing state whitelist. The manual endpoint remains a controlled preflight and cannot bypass global consent, account eligibility, cycle uniqueness, or quota checks.

**Step 5: Format, push, and require API tests to turn green**

```powershell
gofmt -w internal/httpapi/server.go internal/httpapi/handlers.go internal/httpapi/setup_test.go internal/httpapi/handlers_test.go
git diff --check
git add -- internal/httpapi/server.go internal/httpapi/handlers.go internal/httpapi/setup_test.go internal/httpapi/handlers_test.go
git commit -m "feat: require consent for direct wakeup"
git push
```

### Task 8: Update the Vue panel for consent, terminal states, and real replies

**Files:**

- Modify: `web/src/types.ts`
- Modify: `web/src/views/SettingsView.vue`
- Create: `web/src/views/SettingsView.test.ts`
- Modify: `web/src/views/AccountsView.vue`
- Modify: `web/src/components/AccountDrawer.vue`
- Modify: `web/src/components/IntelligenceBadge.vue`
- Modify: `web/src/components/IntelligenceBadge.test.ts`
- Modify: `web/src/components/StatusBadge.vue`
- Modify: `web/src/components/StatusBadge.test.ts`
- Modify: `web/src/styles.css`

**Step 1: Write failing component tests**

Extend `IntelligenceBadge.test.ts` to assert:

```text
normal         -> 智商正常
abnormal       -> 智商不正常
no_answer      -> 无有效回答
legacy_invalid -> 旧版结果无效
empty          -> 未测试
```

Extend `StatusBadge.test.ts` to assert:

```text
direct_disabled     -> 直连未启用
verifying           -> 请求成功·核验中
accepted_unverified -> 请求成功·额度未确认
```

Add a Settings view test that attempts to enable direct wakeup, verifies the risk acknowledgment is required, and verifies the saved request includes both booleans.

**Step 2: Push the red frontend tests**

```powershell
git diff --check
git add -- web/src/components/IntelligenceBadge.test.ts web/src/components/StatusBadge.test.ts web/src/views/SettingsView.test.ts
git commit -m "test: define direct wakeup panel states"
git push
```

Expected cloud failure: new labels, fields, and settings test target do not exist.

**Step 3: Add the explicit settings control**

Add a full-width settings band titled `官方 Codex 直连唤醒` with:

- a checkbox/toggle for `启用直连唤醒`;
- concise text that one account's OAuth access token and bound proxy are read from sub2api into process memory for each request and are not persisted;
- a warning that the request goes directly to ChatGPT rather than through sub2api;
- a separate acknowledgment checkbox shown only when changing from off to on;
- current state `已关闭` or `已启用`.

Disable Save for a false-to-true transition until acknowledgment is checked. Do not expose an upstream URL, TLS bypass, proxy override, prompt editor, or reasoning-effort setting.

**Step 4: Show answer summaries and safe request evidence**

Extend TypeScript `Settings`, `Account`, and `Attempt` with exact backend JSON fields. In the account table render:

- `智商不正常 · 返回 29` for bounded abnormal replies;
- `无有效回答` for explicit empty success;
- `旧版结果无效` for v0.1.5 history;
- truncate table summaries without removing the complete bounded text from the drawer.

In the drawer show the full saved reply plus request model, answer time/source, transport category, quota evidence, and safe terminal summary. For attempts, show the same metadata under each expanded cycle. Never render absent values as secret-looking placeholders.

**Step 5: Add a real 60-second countdown and repair responsive layout**

When `runtime_state='verifying'` and `verification_deadline_at` exists, render the remaining seconds using one local interval and stop it on unmount. At zero, display `正在收敛终态` until the next API poll returns `verified` or `accepted_unverified`.

Update desktop table widths and mobile grid indices so 1440×900 and 390×844 have no body overflow, clipped controls, nested cards, or overlapping text. Use `overflow-wrap:anywhere` and bounded scrolling for the 2000-rune reply.

**Step 6: Commit the implementation and let cloud frontend checks validate it**

```powershell
git diff --check
git add -- web/src/types.ts web/src/views/SettingsView.vue web/src/views/SettingsView.test.ts web/src/views/AccountsView.vue web/src/components/AccountDrawer.vue web/src/components/IntelligenceBadge.vue web/src/components/IntelligenceBadge.test.ts web/src/components/StatusBadge.vue web/src/components/StatusBadge.test.ts web/src/styles.css
git commit -m "feat: show direct wakeup results in panel"
git push
```

Expected: TypeScript, Vitest, and Vite jobs pass in GitHub Actions.

### Task 9: Add production-binary E2E fixtures and document the security boundary

**Files:**

- Create: `web/tests/fake-codex.mjs`
- Modify: `web/tests/fake-sub2api.mjs`
- Modify: `web/playwright.config.ts`
- Modify: `web/tests/panel.spec.ts`
- Modify: `README.md`
- Modify: `SECURITY.md`
- Modify: `THIRD_PARTY_NOTICES.md`

**Step 1: Extend the fake sub2api without reintroducing `/test`**

Implement the single-account export and refresh endpoints in `fake-sub2api.mjs`. The export fixture includes a unique access-token sentinel, ChatGPT account ID, expiry, and no proxy. Count calls and expose only test-safe counters through a fake-only status endpoint. Remove `/api/v1/admin/accounts/7/test`; any call to it must return 404.

Before direct completion, quota returns a valid idle 0% five-hour window. After completion, it returns positive use with the same reset observed by the fake Codex headers.

**Step 2: Add the e2e-only fake Codex server**

`fake-codex.mjs` listens on `127.0.0.1:18082`, verifies the Bearer token, ChatGPT account ID, official headers, model, fixed puzzle, `store=false`, and `stream=true`, then returns split output events, `response.completed`, and primary/secondary headers in reversed order. It records the request count but never prints the token.

Add it to Playwright `webServer` and add:

```text
SUB2API_AUTO5H_E2E_CODEX_URL=http://127.0.0.1:18082
```

to the e2e-tagged app command.

**Step 3: Extend Playwright acceptance**

For both desktop 1440×900 and mobile 390×844:

1. initialize and confirm setup performs no export or Codex request;
2. log in and sync the account;
3. enable the account policy while global direct mode is off and confirm zero export/request calls;
4. open Settings, check direct wakeup plus risk acknowledgment, and save;
5. wait for `last_answer_status='normal'` and `runtime_state='verified'`;
6. assert table summary, drawer answer `21`, model, `official_codex_sse`, `direct`, and `official_headers`;
7. assert exactly one export and one Codex request;
8. reload/restart the app fixture where practical and assert no duplicate;
9. preserve the SPA deep-link/API-404 checks and body-width assertions.

The empty-reply and `accepted_unverified` paths remain covered by the deterministic scheduler tests plus badge/status component tests; do not shorten the production 10/30/60 schedule solely for Playwright.

**Step 4: Replace obsolete `/test` and v0.1.5 claims in documentation**

README must document:

- the fixed official endpoint and fixed candy puzzle;
- global direct mode default-off after install and upgrade;
- temporary single-account export and sub2api-owned refresh;
- strict reuse of account proxy and no direct fallback;
- explicit-success answer semantics (`21`, non-21, empty, legacy invalid);
- official-header evidence, quota fallback, and 60-second terminal behavior;
- at-least-once ambiguity for transport timeout plus unavailable quota;
- the fact that v0.1.5 IQ results are invalid because sub2api did not forward the prompt;
- upgrade instructions and how to enable direct wakeup after reading the warning.

SECURITY.md must state that Go cannot guarantee physical string zeroization and that the promise is no persistence/no logging/short lifetime. It must list admin-key compromise as equivalent to credential-export authority. No documentation may claim exactly-once delivery.

THIRD_PARTY_NOTICES does not need a new dependency entry if the implementation remains standard-library only; verify rather than adding unrelated notices.

**Step 5: Push and require production-binary Playwright to pass**

```powershell
git diff --check
git add -- web/tests/fake-codex.mjs web/tests/fake-sub2api.mjs web/playwright.config.ts web/tests/panel.spec.ts README.md SECURITY.md THIRD_PARTY_NOTICES.md
git commit -m "test: cover official wakeup end to end"
git push
```

Expected: the e2e-tagged browser-test binary talks only to loopback fixtures; normal Go tests and the release build retain the fixed ChatGPT URL.

### Task 10: Perform security/source review, merge, and release v0.1.6

**Files:**

- Verify: all modified files
- Verify: `.github/workflows/ci.yml`
- No new source files unless a cloud failure identifies a defect

**Step 1: Run final local non-compiling checks**

```powershell
$goFiles = rg --files cmd internal -g '*.go'
gofmt -w $goFiles
gofmt -l $goFiles
git diff --check
rg -n "/api/v1/admin/accounts/.*/test|TestAccount|test_complete|sub2api_test_v0.1.5" cmd internal web README.md SECURITY.md
rg -n "access_token|refresh_token|id_token|proxy.*password|Authorization" internal web -g '!**/*_test.go'
git status --short
```

Expected:

- `gofmt -l` and `git diff --check` print nothing;
- no active scheduler/client path calls the sub2api test endpoint;
- `sub2api_test_v0.1.5` appears only as a historical source marker/migration explanation;
- secret-field occurrences are limited to temporary parsing/request construction and explicit redaction logic;
- no token/proxy secret field exists in a store model or migration.

**Step 2: Review the complete branch diff against the design**

```powershell
git diff --stat main...HEAD
git diff main...HEAD -- . ':!.openteams/specs/*.html'
```

Check every design requirement: opt-in off by default, one-account export, identity validation, sub2api-owned refresh, fixed upstream, four proxy protocols, no fallback, bounded SSE, header window identification, orthogonal answer status, 60-second convergence, restart recovery, no duplicate after explicit success, safe audit fields, and responsive UI.

**Step 3: Wait for the final Draft PR workflow**

```powershell
$pr = gh pr view --json number --jq '.number'
$headSha = git rev-parse HEAD
$runId = gh run list --branch codex/direct-codex-wakeup --workflow "CI and release" --limit 1 --json databaseId,headSha --jq ".[] | select(.headSha == \"$headSha\") | .databaseId"
gh run watch $runId --exit-status
gh pr checks $pr
```

Expected green coverage: lifecycle scripts, npm lock install, TypeScript, Vitest, Vite, Go formatting/tidy/vet/unit/race, e2e-tagged production server, Playwright desktop/mobile, and artifact diagnostics.

For any failure:

```powershell
gh run view $runId --log-failed
```

Patch only the root cause, format locally if Go changed, commit, push, and wait for the replacement run. Do not add insecure TLS, direct proxy fallback, secret logging, arbitrary upstream configuration, or a fail-open quota branch.

**Step 4: Mark ready and squash-merge after all checks pass**

```powershell
gh pr ready $pr
gh pr merge $pr --squash --delete-branch
git switch main
git pull --ff-only origin main
$mainRun = gh run list --branch main --workflow "CI and release" --limit 1 --json databaseId --jq '.[0].databaseId'
gh run watch $mainRun --exit-status
```

Expected: remote `main` contains one reviewed feature change and remains green.

**Step 5: Tag and verify v0.1.6**

```powershell
git tag -a v0.1.6 -m "v0.1.6"
git push origin v0.1.6
$releaseRun = gh run list --workflow "CI and release" --limit 10 --json databaseId,headBranch --jq '.[] | select(.headBranch == "v0.1.6") | .databaseId' | Select-Object -First 1
gh run watch $releaseRun --exit-status
gh release view v0.1.6 --json tagName,isDraft,isPrerelease,assets,url
```

Expected release assets:

```text
sub2api-auto5h-linux-amd64.tar.gz
sub2api-auto5h-linux-arm64.tar.gz
SHA256SUMS
```

The release workflow must use `CGO_ENABLED=0`, build without the `e2e` tag, start-check amd64, inspect arm64 metadata, package install/upgrade/uninstall assets, and publish a non-draft public release.

**Step 6: Report upgrade behavior and first real acceptance**

Tell the administrator:

1. upgrade using the existing upgrade mode of `install.sh`;
2. open Settings and explicitly enable `官方 Codex 直连唤醒` after acknowledging the warning;
3. run read-only quota refresh first;
4. select one Plus and one Team/Business account for the first controlled real activation;
5. verify one request, the actual answer, request model, transport category, quota evidence, and either `verified` or terminal `accepted_unverified` within 60 seconds;
6. confirm v0.1.5 answers display `旧版结果无效` and do not trigger migration-time requests.

Do not put real OAuth credentials or real ChatGPT calls in public CI.
