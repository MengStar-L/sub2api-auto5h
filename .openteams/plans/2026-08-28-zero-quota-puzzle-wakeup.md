# Zero-Quota Puzzle Wakeup Implementation Plan

**Goal:** Treat an exact 0% five-hour quota as immediately available, replace the `hi` activation prompt with the fixed candy puzzle, and persist/display whether the model replied exactly `21` without retrying wrong answers.

**Architecture:** Keep quota availability, transport success, quota verification, and puzzle-answer assessment as separate concepts. The sub2api client sends the fixed prompt and returns only the bounded text reconstructed from SSE content events; the scheduler accepts only the existing successful terminal event, stores the answer assessment atomically with the attempt, and requires positive five-hour usage for external/inferred/verified activation. A forward-only SQLite migration exposes the latest assessment on account rows and the historical assessment on attempt rows for the Vue panel.

**Tech Stack:** Go 1.27, `net/http`, SSE, `database/sql`, `modernc.org/sqlite`, Vue 3, TypeScript 6, Vitest, Playwright, GitHub Actions cloud compilation and Linux cross-compilation

---

### Task 1: Correct zero-usage quota semantics

**Files:**
- Modify: `internal/sub2api/types.go`
- Modify: `internal/sub2api/quota_test.go`
- Modify: `internal/scheduler/scheduler.go`
- Create: `internal/scheduler/wakeup_test.go`

- **Step 1: Add quota state tests**

Add table tests proving that an exact 0% window is idle even when its reset is in the future, while positive usage is active:

```go
func TestFiveHourStateUsesUsageNotOnlyFutureReset(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	quota := Quota{
		Allowed: true,
		FiveHour: &Window{UsedPercent: 0, LimitWindowSeconds: FiveHoursSeconds, ResetAt: now.Unix() + FiveHoursSeconds},
	}
	if quota.FiveActive(now) || !quota.KnownIdle(now) {
		t.Fatalf("zero usage should be idle: %#v", quota)
	}
	quota.FiveHour.UsedPercent = 0.01
	if !quota.FiveActive(now) || quota.KnownIdle(now) {
		t.Fatalf("positive usage should be active: %#v", quota)
	}
}
```

Add cases asserting that `allowed=false`, `limit_reached=true`, and future exhausted 7d quota are never idle.

- **Step 2: Add scheduler regressions for synthetic reset times**

In `internal/scheduler/wakeup_test.go`, define a scripted `Remote` that records test calls and returns configured quota/test results. Use `schedulerTestStore` and call `automation.Stop` from cleanup so the asynchronous verifier cannot outlive the store.

Use this concrete fake so sequential quota responses can cover preflight and reconciliation while remaining race-safe when verification starts:

```go
type scriptedRemote struct {
	mu         sync.Mutex
	quota      sub2api.Quota
	quotas     []sub2api.Quota
	quotaErr   error
	testResult sub2api.TestResult
	testErr    error
	testCalls  int
}

func (*scriptedRemote) Accounts(context.Context) ([]sub2api.Account, error) { return []sub2api.Account{}, nil }
func (*scriptedRemote) Models(context.Context, int64) ([]string, error)     { return []string{}, nil }
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
```

Add these tests:

```go
func TestProcessAccountDispatchesWhenFiveHourUsageIsZero(t *testing.T) {
	data, account := schedulerTestStore(t)
	now := time.Now().UTC()
	remote := &scriptedRemote{quota: sub2api.Quota{
		PlanType: "plus", AccountID: "workspace", FetchedAt: now.Unix(), Allowed: true,
		FiveHour: &sub2api.Window{UsedPercent: 0, LimitWindowSeconds: sub2api.FiveHoursSeconds, ResetAt: now.Add(5 * time.Hour).Unix()},
		SevenDay: &sub2api.Window{UsedPercent: 10, LimitWindowSeconds: sub2api.SevenDaysSeconds, ResetAt: now.Add(6 * 24 * time.Hour).Unix()},
	}}
	remote.testResult = sub2api.TestResult{HTTPStatus: 200, Success: true, Reply: "21"}
	automation := schedulerWithRemote(data, remote)
	t.Cleanup(automation.Stop)

	if err := automation.ProcessAccount(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	if remote.testCalls != 1 {
		t.Fatalf("test calls=%d", remote.testCalls)
	}
}
```

Add a positive-usage case asserting zero test calls and `runtime_state="waiting"`. Add an ambiguous-attempt case where the test fails and a second quota response remains 0% with a newer future reset; assert the cycle is not `success_inferred`.

- **Step 3: Verify the new tests describe current failures without compiling locally**

Inspect the current code and confirm:

1. `FiveActive` returns true for any future reset regardless of usage.
2. `KnownIdle` rejects a 0% future window.
3. ambiguous reconciliation and `verify` accept reset advancement without requiring positive usage.

Per the project requirement, do not run Go compilation locally.

- **Step 4: Implement the corrected state helpers**

Change the quota helpers to:

```go
func (q Quota) FiveActive(now time.Time) bool {
	return q.FiveHour != nil && q.FiveHour.UsedPercent > 0 && q.FiveHour.ResetAt > now.Unix()
}

func (q Quota) KnownIdle(now time.Time) bool {
	fiveIdle := q.FiveHour == nil || q.FiveHour.UsedPercent == 0 || q.FiveHour.ResetAt <= now.Unix()
	return fiveIdle && q.Allowed && !q.LimitReached && !q.SevenExhausted(now)
}
```

Quota parsing already rejects negative percentages and malformed/duplicate windows, so equality with zero is unambiguous at this layer.

- **Step 5: Require positive usage in reconciliation and verification**

In `sendAttempt`, keep the source-reset comparison but replace the reset-only inference predicate with:

```go
quotaErr == nil && quota.FiveActive(time.Unix(ended, 0)) &&
	(claimed.SourceResetAt == nil || quota.FiveHour.ResetAt != *claimed.SourceResetAt)
```

In `verify`, replace the reset-only check with:

```go
if err != nil || !quota.FiveActive(time.Unix(acceptedAt, 0)) {
	continue
}
```

An explicitly successful attempt uses cycle and account runtime status `success_unverified`; `DueCycle` does not select that status, so it cannot be resent. Verification later changes the same cycle to `verified`. If all three reads stay at 0%, leave `success_unverified` unchanged and retain the request-time-derived next action.

In `RefreshQuota`, set enabled idle accounts to `due` at the current time, positive active accounts to `waiting` at reset + grace, and leave disabled accounts' runtime state/next action unchanged:

```go
now := s.clock.Now()
if account.Policy.Enabled && quota.KnownIdle(now) {
	value := now.Unix()
	next = &value
	state = "due"
} else if account.Policy.Enabled && quota.FiveActive(now) {
	value := quota.FiveHour.ResetAt + int64(effectiveGrace(account.Policy, settings))
	next = &value
	state = "waiting"
}
```

- **Step 6: Format, inspect, and commit the quota fix**

Run only the permitted formatter and static diff checks:

```powershell
gofmt -w internal/sub2api/types.go internal/sub2api/quota_test.go internal/scheduler/scheduler.go internal/scheduler/wakeup_test.go
gofmt -l internal/sub2api/types.go internal/sub2api/quota_test.go internal/scheduler/scheduler.go internal/scheduler/wakeup_test.go
git diff --check
```

Expected: `gofmt -l` and `git diff --check` print nothing. Commit:

```powershell
git add -- internal/sub2api/types.go internal/sub2api/quota_test.go internal/scheduler/scheduler.go internal/scheduler/wakeup_test.go
git commit -m "fix: treat zero five-hour usage as idle"
```

### Task 2: Send the candy puzzle and reconstruct the SSE reply

**Files:**
- Modify: `internal/sub2api/types.go`
- Modify: `internal/sub2api/sse.go`
- Modify: `internal/sub2api/quota_test.go`

- **Step 1: Replace the request-body assertion and add chunked reply tests**

Define the expected request assertion using an exported fixed prompt constant:

```go
if body["prompt"] != WakeupPrompt || body["mode"] != "default" || body["model_id"] != "gpt-text" {
	t.Fatalf("unexpected test body: %#v", body)
}
```

Make the fake SSE send two content chunks followed by the terminal event:

```go
_, _ = fmt.Fprint(w, "data: {\"type\":\"content\",\"text\":\"2\"}\n\n")
_, _ = fmt.Fprint(w, "data: {\"type\":\"content\",\"text\":\"1\"}\n\n")
_, _ = fmt.Fprint(w, "data: {\"type\":\"test_complete\",\"success\":true}\n\n")
```

Assert `result.Reply == "21"`. Add a response longer than 2000 Unicode characters and assert the returned reply contains exactly 2000 runes. Preserve the existing test that EOF without `test_complete` fails even after content events.

- **Step 2: Verify current source cannot satisfy the tests**

The current payload hard-codes `hi`, ignores `content.text`, and `TestResult` has no reply field. Do not compile locally.

- **Step 3: Add the fixed prompt and result field**

Add the complete prompt as a raw Go string in `sse.go`:

```go
const WakeupPrompt = `在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）

苹果味 桃子味 西瓜味
圆形 7 9 8
五角星形 7 6 4

只能返回一个阿拉伯数字，不要解释，不要添加标点或其他内容。`
```

Extend `TestResult`:

```go
type TestResult struct {
	HTTPStatus int
	Success    bool
	Message    string
	Reply      string
}
```

- **Step 4: Collect only bounded content text**

Add `Text string` to the decoded SSE event. Keep a `[]rune` reply buffer and append at most 2000 runes from events whose type is `content`. Ignore status, model, image, and raw event data. On successful terminal completion return:

```go
return TestResult{
	HTTPStatus: resp.StatusCode,
	Success:    true,
	Reply:      string(replyRunes),
}, nil
```

Do not weaken the 1 MiB total SSE cap, 128 KiB scanner-token cap, request timeout, or terminal-event requirement.

- **Step 5: Format and commit the transport change**

```powershell
gofmt -w internal/sub2api/types.go internal/sub2api/sse.go internal/sub2api/quota_test.go
git diff --check
git add -- internal/sub2api/types.go internal/sub2api/sse.go internal/sub2api/quota_test.go
git commit -m "feat: use puzzle prompt for activation"
```

Expected: the focused diff contains the exact Chinese puzzle, the output-only-number instruction, bounded text reconstruction, and no raw SSE persistence.

### Task 3: Persist answer assessments atomically

**Files:**
- Create: `internal/store/migrations/002_answer_assessment.sql`
- Modify: `internal/store/models.go`
- Modify: `internal/store/accounts.go`
- Modify: `internal/store/cycles.go`
- Modify: `internal/store/store_test.go`

- **Step 1: Add a migration and persistence regression test**

Open a fresh test store, query `PRAGMA table_info` for the new columns, create an eligible account and cycle, then finish an accepted attempt with an abnormal answer:

```go
result := AttemptResult{
	Outcome: "accepted", Status: "success_unverified", AnswerStatus: "abnormal", AnswerText: "答案是 29",
}
if err := data.FinishAttempt(ctx, cycle, started, ended, result); err != nil {
	t.Fatal(err)
}
updated, _ := data.GetAccount(ctx, account.ID)
if updated.LastAnswerStatus != "abnormal" || updated.LastAnswerText != "答案是 29" || updated.LastAnswerAt == nil || *updated.LastAnswerAt != ended {
	t.Fatalf("account answer=%#v", updated)
}
attempts, _ := data.ListAttempts(ctx, cycle.ID)
if len(attempts) != 1 || attempts[0].AnswerStatus != "abnormal" || attempts[0].AnswerText != "答案是 29" {
	t.Fatalf("attempts=%#v", attempts)
}
```

Finish a later failed attempt with empty answer fields and assert it does not clear the account's last successful assessment.

- **Step 2: Create the forward-only migration**

Create exactly these additive statements:

```sql
ALTER TABLE attempts ADD COLUMN answer_status TEXT NOT NULL DEFAULT '';
ALTER TABLE attempts ADD COLUMN answer_text TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN last_answer_status TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN last_answer_text TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN last_answer_at INTEGER;
```

Do not edit `001_initial.sql`; an existing installation must apply only the new transaction migration.

- **Step 3: Extend store models and account scanning**

Add to `Account`:

```go
LastAnswerStatus string `json:"last_answer_status"`
LastAnswerText   string `json:"last_answer_text"`
LastAnswerAt     *int64 `json:"last_answer_at,omitempty"`
```

Add `AnswerStatus` and `AnswerText` to both `Attempt` and `AttemptResult`. Extend `accountSelect`, `scanAccount`, the attempts insert, and `ListAttempts` in the same column order. Scan `last_answer_at` through `sql.NullInt64`.

- **Step 4: Update latest assessment in the attempt transaction**

After the existing runtime-state update in `FinishAttempt`, conditionally update the account only when `result.AnswerStatus != ""`:

```go
if result.AnswerStatus != "" {
	if _, err := tx.ExecContext(ctx, `UPDATE remote_accounts
      SET last_answer_status=?, last_answer_text=?, last_answer_at=?, updated_at=? WHERE id=?`,
		result.AnswerStatus, limitRunes(result.AnswerText, 2000), endedAt, endedAt, cycle.AccountID); err != nil {
		return err
	}
}
```

Implement `limitRunes` without `TrimSpace`, so the stored text represents what the model returned while remaining valid UTF-8. The scheduler will use `TrimSpace` only for comparison.

- **Step 5: Format, inspect, and commit storage**

```powershell
gofmt -w internal/store/models.go internal/store/accounts.go internal/store/cycles.go internal/store/store_test.go
git diff --check
git add -- internal/store/migrations/002_answer_assessment.sql internal/store/models.go internal/store/accounts.go internal/store/cycles.go internal/store/store_test.go
git commit -m "feat: persist puzzle answer assessments"
```

Expected: all schema changes are additive, failed attempts preserve the previous account result, and full raw SSE data is never stored.

### Task 4: Classify replies without changing activation success

**Files:**
- Modify: `internal/scheduler/scheduler.go`
- Modify: `internal/scheduler/wakeup_test.go`

- **Step 1: Add normal, abnormal, and empty-reply scheduler tests**

Use the scripted remote to run three explicit successful terminal results with replies `" 21\n"`, `"29"`, and `""`. For each run, load the attempt and account. Assert:

```go
if attempt.Outcome != "accepted" || cycle.Status != "success_unverified" {
	t.Fatalf("wrong activation result: cycle=%#v attempt=%#v", cycle, attempt)
}
```

The first case must have `AnswerStatus == "normal"`; the other two must have `AnswerStatus == "abnormal"`. All cases must make exactly one test call and create no retry attempt. The abnormal cases must leave `LastError == ""`.

- **Step 2: Add the isolated classifier**

Implement:

```go
func answerStatus(reply string) string {
	if strings.TrimSpace(reply) == "21" {
		return "normal"
	}
	return "abnormal"
}
```

This intentionally marks `答案是21`, `21。`, other numbers, and empty replies abnormal.

- **Step 3: Store the assessment only on explicit success**

Populate the accepted result:

```go
assessment := answerStatus(result.Reply)
if err := s.store.FinishAttempt(ctx, claimed, now, ended, store.AttemptResult{
	Outcome: "accepted", HTTPStatus: &status, Status: "success_unverified", NextAt: &next, AcceptedAt: &accepted,
	Reason: "test_complete success", AnswerStatus: assessment, AnswerText: result.Reply,
}); err != nil {
	return err
}
```

Extend `FinishAttempt`'s account-state mapping to accept the explicit `success_unverified` status directly while retaining compatibility with any existing `Status: "success"` caller. A terminal success therefore cannot be selected for another attempt before or after restart.

Do not populate answer fields on rejected, ambiguous, inferred, retry, attention, or failed paths. Add an audit event whose message reports `智商检测正常` or `智商检测不正常`, but do not duplicate the reply in event metadata.

- **Step 4: Preserve no-retry semantics**

Keep the return immediately after the explicit-success branch. Quota verification still runs asynchronously, but no answer value can enter the retry logic. Review the control flow and assert there is no comparison to `21` outside the accepted branch.

- **Step 5: Format and commit scheduler assessment**

```powershell
gofmt -w internal/scheduler/scheduler.go internal/scheduler/wakeup_test.go
git diff --check
git add -- internal/scheduler/scheduler.go internal/scheduler/wakeup_test.go
git commit -m "feat: classify puzzle replies independently"
```

### Task 5: Display the latest and historical assessment in the panel

**Files:**
- Create: `web/src/components/IntelligenceBadge.vue`
- Create: `web/src/components/IntelligenceBadge.test.ts`
- Modify: `web/src/types.ts`
- Modify: `web/src/views/AccountsView.vue`
- Modify: `web/src/components/AccountDrawer.vue`
- Modify: `web/src/styles.css`

- **Step 1: Write the badge component test**

Mount the component for `normal`, `abnormal`, and empty states. Assert the rendered text and semantic classes:

```ts
expect(mount(IntelligenceBadge, { props: { status: 'normal' } }).text()).toContain('智商正常')
expect(mount(IntelligenceBadge, { props: { status: 'abnormal' } }).classes()).toContain('danger')
expect(mount(IntelligenceBadge, { props: { status: '' } }).text()).toContain('未测试')
```

- **Step 2: Define the component and API fields**

Implement the badge as a compact `status-badge` with labels:

```ts
const label = computed(() => props.status === 'normal' ? '智商正常' : props.status === 'abnormal' ? '智商不正常' : '未测试')
const tone = computed(() => props.status === 'normal' ? 'success' : props.status === 'abnormal' ? 'danger' : '')
```

Add to `Account`:

```ts
last_answer_status: string
last_answer_text: string
last_answer_at?: number
```

Add `answer_status` and `answer_text` to `Attempt`.

- **Step 3: Add the table column and repair responsive indices**

Insert “智商” before “自动化”, render `IntelligenceBadge`, and change the empty-state colspan from 7 to 8. Increase the desktop minimum width only as needed.

In the mobile CSS, keep reset and next-action hidden, place the new seventh cell on row 3 column 2, and move automation to the eighth cell on row 2 column 3:

```css
.account-table td:nth-child(5), .account-table td:nth-child(6) { display: none; }
.account-table td:nth-child(7) { grid-column: 2; grid-row: 3; }
.account-table td:nth-child(8) { grid-column: 3; grid-row: 2; }
```

- **Step 4: Add latest and historical reply details**

In the drawer, add a “智商检测” section showing the badge, latest time, and:

```vue
<pre class="answer-text">{{ account.last_answer_text || '空回复' }}</pre>
```

Only show `空回复` when `last_answer_status` is non-empty; show “尚未进行检测” otherwise. In each attempt row, render the badge and reply only when `attempt.answer_status` is non-empty. Use `white-space: pre-wrap`, `overflow-wrap: anywhere`, and a bounded scroll area so a 2000-character reply does not overflow.

Change the manual-run confirmation to say that a candy puzzle will be sent only after the quota preflight; remove the stale reference to `hi`.

- **Step 5: Review source-level type consistency and commit**

Do not run Node compilation locally. Verify every added JSON name exactly matches the Go tags, then run:

```powershell
git diff --check
git add -- web/src/components/IntelligenceBadge.vue web/src/components/IntelligenceBadge.test.ts web/src/types.ts web/src/views/AccountsView.vue web/src/components/AccountDrawer.vue web/src/styles.css
git commit -m "feat: show puzzle assessment in panel"
```

### Task 6: Update end-to-end fixtures and documentation

**Files:**
- Modify: `web/tests/fake-sub2api.mjs`
- Modify: `web/tests/panel.spec.ts`
- Modify: `README.md`

- **Step 1: Make the fake upstream expose the zero-usage edge case**

Track an `activated` boolean. Before the test request, return 0% with a future five-hour reset; after the test request, return positive usage. Parse the POST body and reject any request that does not contain the fixed puzzle marker and numeric-only instruction:

```js
if (!body.prompt?.includes('最少取出多少个糖果') || !body.prompt?.includes('只能返回一个阿拉伯数字')) {
  return send(response, null, 400)
}
response.writeHead(200, { 'content-type': 'text/event-stream' })
response.end('data: {"type":"content","text":"2"}\n\ndata: {"type":"content","text":"1"}\n\ndata: {"type":"test_complete","success":true}\n\n')
activated = true
```

- **Step 2: Extend Playwright acceptance**

After enabling the account, poll the authenticated accounts API from the page until `last_answer_status === 'normal'`, reload, and assert both the table badge and drawer response are visible:

```ts
await expect.poll(() => page.evaluate(async () => {
  const payload = await fetch('/api/accounts').then((response) => response.json())
  return payload.data?.[0]?.last_answer_status
}), { timeout: 20_000 }).toBe('normal')
await page.reload()
await expect(page.getByText('智商正常').first()).toBeVisible()
```

Open the drawer and assert the displayed reply is `21`. Preserve the body-width assertion and explicitly exercise the configured 1440×900 desktop and 390×844 mobile projects with no horizontal body overflow.

- **Step 3: Replace stale `hi` documentation**

Update the request-body example to use `<固定糖果题与只返回数字约束>`. Document:

1. exact 0% is immediately available even if upstream supplies a future reset;
2. positive usage is required for external/inferred/verified activation;
3. terminal success remains activation success regardless of the answer;
4. trimmed exact `21` is “智商正常”, all other explicit-success replies are “智商不正常” and are not retried;
5. only bounded model text, not raw SSE, is stored.

Replace the deployment acceptance wording “真实 `hi` 请求” with “真实糖果题激活请求”.

- **Step 4: Run permitted local review and commit**

```powershell
rg -n '"hi"|发送 hi|真实 `hi`|prompt.*hi' README.md internal web -g '!internal/webui/dist/**'
git diff --check
git status --short
```

Expected: no active behavior or user-facing text still claims activation sends `hi`; historical design documents may retain the old term. Commit:

```powershell
git add -- web/tests/fake-sub2api.mjs web/tests/panel.spec.ts README.md
git commit -m "test: cover zero-quota puzzle wakeup"
```

### Task 7: Run cloud verification and release v0.1.5

**Files:**
- Verify: `.github/workflows/ci.yml`
- No additional source files unless cloud failures expose a defect

- **Step 1: Perform the final local non-compiling review**

```powershell
gofmt -w internal/sub2api/types.go internal/sub2api/sse.go internal/sub2api/quota_test.go internal/scheduler/scheduler.go internal/scheduler/wakeup_test.go internal/store/models.go internal/store/accounts.go internal/store/cycles.go internal/store/store_test.go
gofmt -l internal/sub2api/types.go internal/sub2api/sse.go internal/sub2api/quota_test.go internal/scheduler/scheduler.go internal/scheduler/wakeup_test.go internal/store/models.go internal/store/accounts.go internal/store/cycles.go internal/store/store_test.go
git diff --check
git status --short
```

Expected: formatting and whitespace checks print no errors, and the worktree is clean after the planned commits. Do not run Go, Vue, Vite, Vitest, or Playwright compilation locally.

- **Step 2: Push main and wait for GitHub Actions**

```powershell
git push origin main
$runId = gh run list --branch main --workflow "CI and release" --limit 1 --json databaseId --jq '.[0].databaseId'
gh run watch $runId --exit-status
```

Expected: lifecycle scripts, frontend type check, Vitest, Vite build, Go formatting/tidy/vet/unit/race, production binary build, and Playwright all pass.

- **Step 3: Diagnose cloud failures without bypassing tests**

For a failed run:

```powershell
gh run view $runId --log-failed
```

Patch the smallest root cause, use `gofmt` when Go files change, commit, push, and wait for the replacement run. Do not weaken SSE terminal checks, quota fail-closed behavior, migration assertions, or UI acceptance to make CI green.

- **Step 4: Tag and publish v0.1.5 only after main is green**

```powershell
git tag -a v0.1.5 -m "v0.1.5"
git push origin v0.1.5
$releaseRunId = gh run list --workflow "CI and release" --limit 10 --json databaseId,headBranch --jq '.[] | select(.headBranch == "v0.1.5") | .databaseId' | Select-Object -First 1
gh run watch $releaseRunId --exit-status
```

Wait for the tag workflow and verify:

```powershell
gh release view v0.1.5 --json tagName,isDraft,isPrerelease,assets,url
```

Expected: a public non-draft release contains Linux amd64 and arm64 tarballs plus `SHA256SUMS`; the workflow starts the amd64 binary and checks arm64 metadata.

- **Step 5: Report the upgrade behavior**

Tell the administrator to upgrade to v0.1.5. On first start, migration `002_answer_assessment.sql` runs automatically. After account synchronization, enabled 0% accounts become due and send one puzzle; a successful answer appears as “智商正常” or “智商不正常” without changing the activation retry policy.
