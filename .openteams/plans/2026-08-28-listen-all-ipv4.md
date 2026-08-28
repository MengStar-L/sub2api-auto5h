# Listen on All IPv4 Interfaces Implementation Plan

**Goal:** Make fresh installs, upgrades, and direct binary startup listen on `0.0.0.0:<port>` by default so Docker-hosted reverse proxies can reach the panel without host-network mode.

**Architecture:** Change the Go fallback and installer-generated environment value together to prevent divergent behavior. Parse the port independently from any valid existing host portion during upgrades, normalize the generated host to `0.0.0.0`, and lock both fresh and migrated cases with existing dependency-free tests.

**Tech Stack:** Go configuration loader/tests, POSIX `sh` lifecycle scripts/tests, systemd environment file, Markdown documentation, GitHub Actions and Linux amd64/arm64 Release packaging.

---

### Task 1: Lock and change the Go fallback

**Files:**
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/config.go`

- **Step 1: Change the regression expectation first**

Update `TestLoadUsesPackagedDefaults` to require:

```go
if cfg.Listen != "0.0.0.0:2555" {
	t.Fatalf("listen = %q", cfg.Listen)
}
```

- **Step 2: Verify the old source would fail in cloud tests**

Run: `go test ./internal/config`

Expected before implementation: FAIL with `listen = "127.0.0.1:2555"`.

- **Step 3: Change the minimal fallback value**

In `config.Load`, set:

```go
Listen: "0.0.0.0:2555",
```

Do not change environment overrides, database defaults, secret locking, or cookie behavior.

- **Step 4: Format and verify in GitHub Actions**

Run: `gofmt -w internal/config/config.go internal/config/config_test.go`.

Expected cloud test: `go test ./internal/config` passes.

### Task 2: Normalize installer output while preserving upgrade ports

**Files:**
- Modify: `scripts/install.sh`
- Modify: `scripts/lifecycle_test.sh`
- Modify: `packaging/sub2api-auto5h.env.example`

- **Step 1: Add failing lifecycle expectations**

Change fresh and rewritten environment assertions to:

```sh
grep -qx 'SUB2API_AUTO5H_LISTEN=0.0.0.0:3000' "$new_env"
grep -qx 'SUB2API_AUTO5H_LISTEN=0.0.0.0:2555' "$fresh_env"
```

Add a Docker bridge fixture:

```sh
bridge_env="$test_root/bridge.env"
echo 'SUB2API_AUTO5H_LISTEN=172.19.0.1:4321' >"$bridge_env"
[ "$(read_configured_port "$bridge_env")" = "4321" ] || fail "bridge listen port was not preserved"
```

- **Step 2: Run the lifecycle test and observe the old host mismatch**

Run: `sh scripts/lifecycle_test.sh`.

Expected before implementation: FAIL because `write_environment` still writes `127.0.0.1` and `read_configured_port` accepts only that host.

- **Step 3: Generalize existing-port parsing**

Replace the loopback-specific expression with one that accepts a nonempty address portion and captures only the final numeric port:

```sh
value=$(sed -n 's/^SUB2API_AUTO5H_LISTEN=.*:\([0-9][0-9]*\)$/\1/p' "$source_env" | tail -n 1)
```

The existing `validate_port` remains the authority for range and leading-zero checks.

- **Step 4: Generate all-interface values**

In `write_environment`, emit:

```sh
printf 'SUB2API_AUTO5H_LISTEN=0.0.0.0:%s\n' "$listen_port"
```

Change `packaging/sub2api-auto5h.env.example` to `SUB2API_AUTO5H_LISTEN=0.0.0.0:2555`.

- **Step 5: Verify shell syntax and behavior**

Run:

```bash
sh -n scripts/install.sh scripts/uninstall.sh scripts/lifecycle_test.sh
sh scripts/lifecycle_test.sh
```

Expected: `Lifecycle script tests passed.`

### Task 3: Update network documentation and publish v0.1.2

**Files:**
- Modify: `README.md`
- Verify: `.github/workflows/ci.yml`

- **Step 1: Replace loopback-only deployment statements**

Document that the service listens on `0.0.0.0:<selected port>`, Docker Nginx may proxy to its host gateway and selected port, and direct public access must be blocked by the cloud security group or host firewall. Retain the HTTPS reverse-proxy and `SUB2API_AUTO5H_COOKIE_SECURE=true` requirements.

- **Step 2: Add a Docker reverse-proxy example**

Include:

```yaml
extra_hosts:
  - "host.docker.internal:host-gateway"
```

and an Nginx upstream example using `http://host.docker.internal:2555` with the original Host header preserved.

- **Step 3: Audit stale defaults**

Run:

```bash
rg -n '127\.0\.0\.1:2555|SUB2API_AUTO5H_LISTEN=' README.md internal/config packaging scripts
```

Expected: `127.0.0.1` remains only in deliberate historical migration fixtures or SSH client-side examples; current defaults and generated values use `0.0.0.0`.

- **Step 4: Run source-only checks and commit**

Run `git diff --check`, shell syntax tests, lifecycle tests, and `gofmt`. Commit:

```bash
git add README.md internal/config packaging scripts
git commit -m "feat: listen on all IPv4 interfaces by default"
```

- **Step 5: Push main and wait for the full cloud pipeline**

```bash
git push origin main
gh run watch <main-run-id> --exit-status
```

Expected: lifecycle shell tests, frontend typecheck/Vitest/Vite, gofmt/tidy/vet/unit/race, production Playwright, and diagnostics upload all pass.

- **Step 6: Tag and verify v0.1.2**

```bash
git tag -a v0.1.2 -m "v0.1.2"
git push origin v0.1.2
gh run watch <tag-run-id> --exit-status
gh release view v0.1.2 --json url,assets
```

Expected: amd64 and arm64 archives plus `SHA256SUMS`; packaged env example contains `0.0.0.0:2555` and both archive checksums validate.

### Self-review result

The plan covers Go startup, fresh installation, upgrades from loopback and Docker bridge bindings, port preservation, packaged defaults, Docker Nginx documentation, exposure warnings, regression tests, and cloud-only release production. It introduces no new network option or IPv6 behavior, reuses existing validation functions, and contains no deferred implementation step.
