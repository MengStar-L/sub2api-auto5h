# Account Reappearance Recovery Implementation Plan

**Goal:** Restore an OpenAI OAuth account from the stale `missing` runtime state when it is present in the latest sub2api inventory, while preserving its automation policy and existing safety rules.

**Architecture:** Keep `ReplaceInventory` transactional, but separate the temporary membership mark from the final runtime-state transition. Mark rows as unseen without overwriting their runtime state, upsert the current inventory, recover only returned rows whose prior runtime state was `missing`, and finally apply the missing state only to rows omitted from the completed inventory.

**Tech Stack:** Go 1.27, `database/sql`, SQLite via `modernc.org/sqlite`, existing store tests, GitHub Actions cloud verification and Linux cross-compilation

---

### Task 1: Add inventory-state regression coverage

**Files:**
- Modify: `internal/store/store_test.go`

- **Step 1: Add a reusable eligible account fixture**

Add a helper that supplies a stable remote identity and accepts the fields varied by each test:

```go
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
```

- **Step 2: Write the enabled-account reappearance test**

Create the account, enable its policy, remove it from one completed inventory, and then return the same identity:

```go
func TestReplaceInventoryRestoresEnabledAccountAfterReappearance(t *testing.T) {
	data := openTestStore(t)
	ctx := context.Background()
	input := eligibleInventoryAccount()
	if err := data.ReplaceInventory(ctx, "conn", []RemoteAccountInput{input}); err != nil {
		t.Fatal(err)
	}
	accounts, err := data.ListAccounts(ctx)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts=%d err=%v", len(accounts), err)
	}
	id := accounts[0].ID
	if err := data.SetPolicy(ctx, id, Policy{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := data.ReplaceInventory(ctx, "conn", []RemoteAccountInput{}); err != nil {
		t.Fatal(err)
	}
	missing, _ := data.GetAccount(ctx, id)
	if !missing.Missing || missing.RuntimeState != "missing" {
		t.Fatalf("missing account=%#v", missing)
	}

	before := time.Now().Unix()
	if err := data.ReplaceInventory(ctx, "conn", []RemoteAccountInput{input}); err != nil {
		t.Fatal(err)
	}
	restored, _ := data.GetAccount(ctx, id)
	if restored.Missing || !restored.Policy.Enabled || restored.RuntimeState != "pending_check" || restored.NextActionAt == nil || *restored.NextActionAt < before {
		t.Fatalf("restored account=%#v", restored)
	}
}
```

- **Step 3: Write the disabled-account reappearance test**

Use the same inventory sequence without enabling the policy. Assert the restored row has `Missing == false`, `Policy.Enabled == false`, `RuntimeState == "disabled"`, and `NextActionAt == nil`.

- **Step 4: Write the ineligible and identity-change tests**

For the ineligible case, reinsert the account with:

```go
input.Eligible = false
input.Schedulable = false
input.EligibilityReason = "sub2api 已关闭账号调度"
```

Assert `RuntimeState == "paused"`, `NextActionAt == nil`, and the policy is not silently enabled if it was disabled.

For the identity-change case, enable the original account, mark it missing, then set `input.IdentityHash = "identity-b"` before reinsertion. Assert `RuntimeState == "identity_changed"`, `Policy.Enabled == false`, and `IdentityGeneration == 2`.

- **Step 5: Protect normal synchronization from state resets**

Set an eligible account to a stable scheduled state, synchronize the same input again, and verify both fields survive:

```go
next := time.Now().Add(time.Hour).Unix()
if err := data.SetAccountRuntime(ctx, id, "waiting", "", &next); err != nil {
	t.Fatal(err)
}
if err := data.ReplaceInventory(ctx, "conn", []RemoteAccountInput{input}); err != nil {
	t.Fatal(err)
}
unchanged, _ := data.GetAccount(ctx, id)
if unchanged.RuntimeState != "waiting" || unchanged.NextActionAt == nil || *unchanged.NextActionAt != next {
	t.Fatalf("unchanged account=%#v", unchanged)
}
```

- **Step 6: Verify the tests describe a failure in the current source**

Do not compile locally. Inspect `ReplaceInventory` and confirm the current first `UPDATE` sets every row to `runtime_state='missing'`, while the eligible-account upsert preserves `remote_accounts.runtime_state`. Therefore the enabled reappearance and stable-state tests would fail before the fix.

### Task 2: Separate inventory membership marking from runtime transitions

**Files:**
- Modify: `internal/store/accounts.go:100-166`
- Test: `internal/store/store_test.go`

- **Step 1: Stop overwriting runtime state before the upserts**

Replace the opening inventory update with a membership-only mark:

```go
if _, err := tx.ExecContext(ctx, `UPDATE remote_accounts SET missing=1 WHERE connection_uuid=?`, connectionUUID); err != nil {
	return err
}
```

Do not clear `next_action_at`, change `runtime_state`, or update timestamps in this provisional statement. The transaction prevents other readers from seeing this temporary membership state.

- **Step 2: Keep the existing upsert behavior for returned rows**

Retain `missing=0` in the conflict update. Preserve the existing precedence:

1. An identity change produces `identity_changed` and disables the policy.
2. An ineligible account produces `paused` and clears its next action.
3. Other returned accounts preserve their current runtime state and next action.

This leaves a previously missing, now returned eligible account with `missing=0` and its prior `runtime_state='missing'`, making it distinguishable from both continuously present and still omitted rows.

- **Step 3: Recover returned accounts according to their saved policy**

After all upserts and policy-row creation complete, add two transaction updates. Recover enabled rows first:

```go
if _, err := tx.ExecContext(ctx, `UPDATE remote_accounts
  SET runtime_state='pending_check', next_action_at=?, last_error='', updated_at=?
  WHERE connection_uuid=? AND missing=0 AND eligible=1 AND runtime_state='missing'
    AND EXISTS (
      SELECT 1 FROM account_policies p
      WHERE p.account_id=remote_accounts.id AND p.enabled=1
    )`, now, now, connectionUUID); err != nil {
	return err
}
```

Then recover disabled rows without scheduling work:

```go
if _, err := tx.ExecContext(ctx, `UPDATE remote_accounts
  SET runtime_state='disabled', next_action_at=NULL, last_error='', updated_at=?
  WHERE connection_uuid=? AND missing=0 AND eligible=1 AND runtime_state='missing'
    AND NOT EXISTS (
      SELECT 1 FROM account_policies p
      WHERE p.account_id=remote_accounts.id AND p.enabled=1
    )`, now, connectionUUID); err != nil {
	return err
}
```

- **Step 4: Finalize rows omitted from the completed inventory**

After recovery, apply the missing runtime state only to rows whose provisional membership mark was not cleared by an upsert:

```go
if _, err := tx.ExecContext(ctx, `UPDATE remote_accounts
  SET next_action_at=NULL, runtime_state='missing', updated_at=?
  WHERE connection_uuid=? AND missing=1`, now, connectionUUID); err != nil {
	return err
}
```

Commit only after all four phases succeed. Any SQL failure rolls back the membership marks and runtime-state updates together.

- **Step 5: Format the changed Go files**

Run only the non-compiling formatter locally:

```powershell
gofmt -w internal/store/accounts.go internal/store/store_test.go
```

Expected: `gofmt -l internal/store/accounts.go internal/store/store_test.go` prints no paths.

- **Step 6: Review the focused diff and commit**

Run:

```powershell
git diff --check
git diff -- internal/store/accounts.go internal/store/store_test.go
```

Expected: no whitespace errors; every production change belongs to `ReplaceInventory`, and tests cover reappearance, identity safety, ineligible accounts, and normal-state preservation.

Commit:

```powershell
git add -- internal/store/accounts.go internal/store/store_test.go
git commit -m "fix: restore reappearing account state"
```

### Task 3: Verify in GitHub Actions and publish the patch

**Files:**
- No additional source files
- Verify: `.github/workflows/ci.yml`

- **Step 1: Push the implementation and inspect the cloud run**

Run:

```powershell
git push origin main
gh run list --branch main --limit 3
```

Expected: a new `CI and release` run appears for the implementation commit.

- **Step 2: Wait for the complete cloud test matrix**

Run:

```powershell
gh run watch <run-id> --exit-status
```

Expected: lifecycle shell tests, frontend type check/Vitest/Vite, Go formatting/tidy/vet/unit/race tests, production binary build, and Playwright all pass. If any job fails, inspect it with `gh run view <run-id> --log-failed`, patch only the failure, push, and repeat this step.

- **Step 3: Tag the green commit as v0.1.4**

After the main-branch run is green:

```powershell
git tag -a v0.1.4 -m "v0.1.4"
git push origin v0.1.4
```

Expected: the tag-triggered `CI and release` workflow starts only after the verified main commit is tagged.

- **Step 4: Verify release assets**

Wait for the tag workflow, then run:

```powershell
gh run list --branch v0.1.4 --limit 3
gh release view v0.1.4 --json tagName,isDraft,isPrerelease,assets,url
```

Expected: a public non-draft `v0.1.4` release contains Linux `amd64` and `arm64` tarballs plus `SHA256SUMS`. The cloud workflow has already started the amd64 binary and checked arm64 binary metadata.

- **Step 5: Report the deployment effect**

Tell the administrator to upgrade to `v0.1.4` and click the account synchronization button once. Expected panel behavior: the five currently stale rows immediately stop displaying “已移除”; enabled rows enter “待检查”, while disabled rows display “已关闭”.
