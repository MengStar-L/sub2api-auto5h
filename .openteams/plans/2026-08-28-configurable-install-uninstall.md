# Configurable Installation and Complete Uninstall Implementation Plan

**Goal:** Install the service, configuration, SQLite data, backups, and uninstaller under a user-selected root directory, defaulting to `/opt/sub2apiauto5h`, with a default loopback port of `2555` and a safe complete-uninstall path.

**Architecture:** Keep both lifecycle scripts self-contained so `curl | sudo sh` works, but organize their behavior into POSIX-shell functions that can be sourced by a dependency-free test script. Generate the systemd unit from the packaged default template after validating the selected path, preserve or migrate existing encrypted state, and require an explicit destructive confirmation before removing an exact validated installation root.

**Tech Stack:** POSIX `sh`, systemd, standard Linux account/file utilities, Go configuration tests, GitHub Actions, existing Go/Vue/Playwright release pipeline.

---

### Task 1: Lock the new application defaults with Go tests

**Files:**
- Create: `internal/config/config_test.go`
- Modify: `internal/config/config.go`

- **Step 1: Write the failing default test**

```go
func TestLoadUsesPackagedDefaults(t *testing.T) {
	for _, key := range []string{EnvListen, EnvDBPath, EnvMasterKey, EnvCookieSecure} {
		t.Setenv(key, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:2555" {
		t.Fatalf("listen = %q", cfg.Listen)
	}
	if cfg.DBPath != "/opt/sub2apiauto5h/data/app.db" {
		t.Fatalf("db path = %q", cfg.DBPath)
	}
}
```

- **Step 2: Run the focused test in GitHub Actions and verify the old defaults fail**

Run: `go test ./internal/config`

Expected before implementation: FAIL showing `127.0.0.1:8090` or `/var/lib/sub2api-auto5h/app.db`.

- **Step 3: Change only the built-in defaults**

Set `Config.Listen` to `127.0.0.1:2555` and `Config.DBPath` to `/opt/sub2apiauto5h/data/app.db`. Retain all environment overrides and locked-secret behavior unchanged.

- **Step 4: Verify the package**

Run: `go test ./internal/config`

Expected: PASS.

- **Step 5: Commit the default change**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: use new packaged service defaults"
```

### Task 2: Add testable POSIX-shell validation and rendering

**Files:**
- Rewrite: `scripts/install.sh`
- Create: `scripts/lifecycle_test.sh`
- Modify: `packaging/sub2api-auto5h.service`
- Modify: `packaging/sub2api-auto5h.env.example`

- **Step 1: Add failing lifecycle assertions**

The test script must source `install.sh` with `SUB2API_AUTO5H_SOURCE_ONLY=1` and assert:

```sh
[ "$(validate_install_dir /opt/sub2apiauto5h)" = "/opt/sub2apiauto5h" ]
expect_failure validate_install_dir /
expect_failure validate_install_dir /opt
expect_failure validate_install_dir relative/path
expect_failure validate_install_dir /opt/../etc
[ "$(validate_port 1)" = 1 ]
[ "$(validate_port 2555)" = 2555 ]
[ "$(validate_port 65535)" = 65535 ]
expect_failure validate_port 0
expect_failure validate_port 65536
expect_failure validate_port abc
```

It must render the packaged unit into a temporary file and assert the custom root appears in the install marker, `EnvironmentFile`, `ExecStart`, and `ReadWritePaths`, while `/opt/sub2apiauto5h` no longer appears.

- **Step 2: Run the lifecycle test and verify missing functions fail**

Run: `sh scripts/lifecycle_test.sh`

Expected before implementation: FAIL because validation/rendering functions are undefined.

- **Step 3: Implement input selection and validation functions**

`install.sh` must:

- use the current unit marker as the upgrade default when one exists, otherwise `/opt/sub2apiauto5h`;
- use the current configured port as the upgrade default when valid, otherwise `2555`;
- read unset values from `/dev/tty` when available;
- accept `SUB2API_AUTO5H_INSTALL_DIR` and `SUB2API_AUTO5H_PORT` without prompting;
- reject non-absolute paths, unsafe characters, `.`/`..` path segments, repeated separators, trailing separators, root, and broad system directories;
- accept only numeric ports from 1 through 65535.

Terminate sourced test mode before root, network, account, or systemd operations:

```sh
if [ "${SUB2API_AUTO5H_SOURCE_ONLY:-0}" = "1" ]; then
  return 0 2>/dev/null || exit 0
fi
main "$@"
```

- **Step 4: Update the default packaged templates**

The default unit must contain:

```ini
# SUB2API_AUTO5H_INSTALL_DIR=/opt/sub2apiauto5h
EnvironmentFile=/opt/sub2apiauto5h/config/sub2api-auto5h.env
ExecStart=/opt/sub2apiauto5h/sub2api-auto5h serve
ReadWritePaths=/opt/sub2apiauto5h/data /opt/sub2apiauto5h/backups
```

Keep the existing sandboxing directives, remove `StateDirectory` and `ConfigurationDirectory`, and retain the 30-second SIGTERM shutdown. The env example must use `127.0.0.1:2555` and `/opt/sub2apiauto5h/data/app.db`.

- **Step 5: Verify shell syntax and helper behavior**

Run:

```bash
sh -n scripts/install.sh scripts/lifecycle_test.sh
sh scripts/lifecycle_test.sh
```

Expected: both commands exit 0 and the test prints a success message.

- **Step 6: Commit the validation boundary**

```bash
git add scripts/install.sh scripts/lifecycle_test.sh packaging/sub2api-auto5h.service packaging/sub2api-auto5h.env.example
git commit -m "feat: configure install root and listen port"
```

### Task 3: Implement preserved upgrades and v0.1.0 migration

**Files:**
- Modify: `scripts/install.sh`
- Modify: `scripts/lifecycle_test.sh`

- **Step 1: Add failing environment-rewrite tests**

Create a temporary legacy env containing a known master key, old listen address, old DB path, secure-cookie flag, and an unrelated future setting. Call `write_environment` for a custom root/port and assert:

```sh
grep -qx 'SUB2API_AUTO5H_MASTER_KEY=known-key' "$new_env"
grep -qx 'SUB2API_AUTO5H_LISTEN=127.0.0.1:3000' "$new_env"
grep -qx "SUB2API_AUTO5H_DB_PATH=$custom_root/data/app.db" "$new_env"
grep -qx 'SUB2API_AUTO5H_COOKIE_SECURE=true' "$new_env"
grep -qx 'SUB2API_AUTO5H_FUTURE=value' "$new_env"
! grep -q '8090\|/var/lib/sub2api-auto5h/app.db' "$new_env"
```

- **Step 2: Run the test and verify migration helpers are absent**

Run: `sh scripts/lifecycle_test.sh`

Expected before implementation: FAIL at the environment rewrite section.

- **Step 3: Implement the installation transaction**

After download and checksum verification, the installer must:

1. stop the active service;
2. classify the current layout from the unit marker or exact v0.1.0 paths;
3. reject a target containing conflicting config/data;
4. create `<root>`, `config`, `data`, and `backups` with the approved owners and modes;
5. archive the current DB/WAL/SHM into `<root>/backups/backup-before-<version>-<UTC>.tar.gz`;
6. copy legacy DB/WAL/SHM and legacy backup archives when migrating;
7. preserve the existing master key, cookie flag, and unknown environment entries while replacing listen/DB values;
8. atomically install the binary and `uninstall.sh`, then render/install the unit;
9. update the dedicated system user's home to `<root>/data`;
10. reload, enable, and start systemd;
11. remove exact old project paths only after `systemctl is-active` succeeds.

If service activation fails, return nonzero and retain the old config/data paths for recovery.

- **Step 4: Verify the helper-level migration contract**

Run:

```bash
sh -n scripts/install.sh scripts/lifecycle_test.sh
sh scripts/lifecycle_test.sh
```

Expected: PASS, including preservation of the known master key and future env entry.

- **Step 5: Commit upgrade support**

```bash
git add scripts/install.sh scripts/lifecycle_test.sh
git commit -m "feat: migrate legacy installation state"
```

### Task 4: Add the guarded complete uninstaller

**Files:**
- Create: `scripts/uninstall.sh`
- Modify: `scripts/lifecycle_test.sh`

- **Step 1: Add failing uninstaller helper tests**

Source `uninstall.sh` in a separate subshell with `SUB2API_AUTO5H_SOURCE_ONLY=1`. Test that it discovers `/srv/sub2apiauto5h` from:

```ini
# SUB2API_AUTO5H_INSTALL_DIR=/srv/sub2apiauto5h
```

Assert it rejects a missing/duplicate marker and all unsafe roots. Create a temporary validated root containing `config`, `data`, and `backups`, call the exact-root removal helper, and assert only that root disappears while a sibling sentinel remains.

- **Step 2: Run the test and verify uninstaller functions are absent**

Run: `sh scripts/lifecycle_test.sh`

Expected before implementation: FAIL in the uninstall section.

- **Step 3: Implement explicit confirmation and cleanup**

The uninstaller must:

- require root;
- resolve the root from `SUB2API_AUTO5H_INSTALL_DIR` or the exact single unit marker;
- validate the resolved root before showing or deleting anything;
- list the unit, root, config, DB, backups, user, and group that will be removed;
- accept noninteractive deletion only when `SUB2API_AUTO5H_UNINSTALL_CONFIRM=yes`;
- otherwise read the exact phrase `REMOVE sub2api-auto5h` from `/dev/tty`, failing closed without a TTY;
- disable/stop the service, remove the unit, daemon-reload/reset-failed, remove the validated root and exact v0.1.0 legacy paths, then remove the dedicated user/group;
- report that deletion is irreversible.

Use no unresolved glob or computed broad path as an `rm -rf` target. Exact legacy cleanup targets may be constants only.

- **Step 4: Verify shell behavior**

Run:

```bash
sh -n scripts/install.sh scripts/uninstall.sh scripts/lifecycle_test.sh
sh scripts/lifecycle_test.sh
```

Expected: PASS; the sibling sentinel in the deletion test remains.

- **Step 5: Commit the uninstaller**

```bash
git add scripts/uninstall.sh scripts/lifecycle_test.sh
git commit -m "feat: add complete uninstall script"
```

### Task 5: Update documentation and cloud release packaging

**Files:**
- Modify: `README.md`
- Modify: `.github/workflows/ci.yml`

- **Step 1: Update installation and operations documentation**

Document:

- interactive defaults `/opt/sub2apiauto5h` and `2555`;
- environment-variable installation with the exact pipe command;
- new file layout and permissions;
- SSH tunnel, browser URL, health and readiness checks using port `2555`;
- custom-root upgrade behavior and v0.1.0 migration;
- installed and remote uninstaller commands;
- the exact confirmation phrase and noninteractive confirmation variable;
- an irreversible deletion warning covering the DB, master key, and backups.

- **Step 2: Extend CI and Release contents**

Before frontend/Go work, add:

```yaml
- name: Check lifecycle scripts
  run: |
    sh -n scripts/install.sh scripts/uninstall.sh scripts/lifecycle_test.sh
    sh scripts/lifecycle_test.sh
```

Add `scripts/uninstall.sh` to each staged Release archive alongside `install.sh`.

- **Step 3: Audit stale defaults and package references**

Run:

```bash
rg -n '/usr/local/bin/sub2api-auto5h|127\.0\.0\.1:8090|/var/lib/sub2api-auto5h/app\.db' README.md packaging internal/config scripts .github
```

Expected: matches occur only in explicitly labeled v0.1.0 migration fixtures/documentation or Playwright's unrelated test port remains absent from this pattern.

- **Step 4: Commit documentation and CI**

```bash
git add README.md .github/workflows/ci.yml
git commit -m "docs: explain configurable lifecycle scripts"
```

### Task 6: Cloud validation and v0.1.1 release

**Files:**
- Verify all changed files

- **Step 1: Perform source-only checks without producing release binaries locally**

Run `git diff --check`, `sh -n` and `sh scripts/lifecycle_test.sh` where POSIX sh is available. Do not use local artifacts for release.

- **Step 2: Push main and monitor the full GitHub Actions workflow**

Run:

```bash
git push origin main
gh run watch <main-run-id> --exit-status
```

Expected: shell lifecycle tests, frontend typecheck/Vitest/Vite, gofmt/tidy/vet/unit/race, production-binary Playwright, and diagnostics upload all pass.

- **Step 3: Fix failures through source changes and repeat cloud validation**

For any failed job, inspect the exact job log or artifact, add a focused regression assertion, commit the smallest correction, push, and wait for a green main run.

- **Step 4: Publish the next release**

After main is green:

```bash
git tag -a v0.1.1 -m "v0.1.1"
git push origin v0.1.1
gh run watch <tag-run-id> --exit-status
gh release view v0.1.1 --json url,assets
```

Expected Release assets: `sub2api-auto5h-linux-amd64.tar.gz`, `sub2api-auto5h-linux-arm64.tar.gz`, and `SHA256SUMS`; both archives contain `install.sh` and `uninstall.sh`.

### Self-review result

Every approved requirement maps to a task: one-root layout and permissions, TTY/environment input, safe validation, dynamic unit rendering, preserved upgrades, v0.1.0 migration, destructive confirmation, exact-root deletion, default Go configuration, documentation, CI syntax/helper tests, and dual-architecture cloud publishing. Function names and environment variables are consistent across tasks, no implementation placeholder remains, and the work stays within lifecycle packaging rather than changing scheduler behavior.
