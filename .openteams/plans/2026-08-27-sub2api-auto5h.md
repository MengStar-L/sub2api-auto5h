# sub2api-auto5h Implementation Plan

**Goal:** Build and publish a single-binary Linux sidecar that safely starts eligible sub2api accounts' new five-hour windows and exposes a Chinese management panel.

**Architecture:** A Go process owns an embedded Vue SPA, a same-origin JSON API, a persistent scheduler, and a pure-Go SQLite database. A narrow sub2api client supplies inventory, passive quota snapshots, model suggestions, and exact-account SSE test requests; all scheduling decisions are persisted before network calls.

**Tech Stack:** Go 1.27, net/http, modernc.org/sqlite, x/crypto Argon2id, Vue 3, TypeScript 6, Vite 8, Vitest, Playwright, GitHub Actions, systemd.

---

### Task 1: Repository and build boundaries

**Files:** `go.mod`, `package.json`, `web/`, `internal/webui/`, `.gitignore`

1. Define the Go module and frontend package with pinned lock files.
2. Configure Vite to emit into `internal/webui/dist` and retain a tracked placeholder so Go package loading has a matching embed path.
3. Route `/api` and health endpoints before SPA fallback; cache hashed assets and never turn API errors into HTML.
4. Verify in GitHub Actions that frontend build completes before any Go package build.

### Task 2: Configuration, cryptography, and persistence

**Files:** `internal/config/`, `internal/secure/`, `internal/store/`

1. Parse listen address, DB path, base64 32-byte master key, secure-cookie switch, and logging from environment.
2. Implement AES-256-GCM records with random nonces and record/field/version AAD; test round trips, tampering, and wrong keys.
3. Open SQLite with WAL, foreign keys, busy timeout, 0600 permissions, a lifetime flock, and transactional embedded migrations.
4. Implement typed repositories for setup, settings, sessions, accounts, policies, cycles, attempts, and audit events.
5. Enforce unique cycle keys, lease transitions, 90-day detail retention, and account identity replacement semantics.

### Task 3: sub2api protocol client

**Files:** `internal/sub2api/client.go`, `internal/sub2api/quota.go`, `internal/sub2api/sse.go`, matching tests

1. Validate absolute base origins, private-HTTP confirmation, no redirects, strict TLS, timeouts, and response size caps.
2. Implement `x-api-key` response-envelope handling, version checks, full account pagination without the status filter, quota and model reads.
3. Parse window presence explicitly; allow only unique 18000/604800 windows and distinguish known idle from missing or incompatible schemas.
4. Implement exact-account test with `{model_id,prompt:"hi",mode:"default"}` and accept only a terminal successful SSE event.
5. Cover authentication/compliance responses, pagination, reversed slots, malformed data, fragmented SSE, error events, EOF, overflow, and timeout with `httptest.Server`.

### Task 4: Scheduler state machine

**Files:** `internal/scheduler/`, scheduler tests with a fake clock and fake client

1. Sync raw account inventory, preserve history, classify eligibility locally, and pause missing/replaced/ineligible identities.
2. On enable, preflight quota and create either a bootstrap cycle, a five-hour reset cycle, a seven-day block, or an attention state.
3. Implement one timer loop with a wake channel, global semaphore, per-account claims, persisted leases, startup catch-up, and graceful cancellation.
4. Before dispatch, detect externally advanced resets; otherwise persist dispatching and call the exact-account test.
5. On explicit success, verify quota at 10/30/60 seconds without resending; on failure reconcile before exponential retries.
6. Permit the two-minute fail-open only for transient transport/429/5xx after a previously valid schema; fail closed for auth, compliance, not-found, identity, or schema failures.
7. Test active/idle/expired windows, seven-day blocking, external races, retry totals, ambiguous responses, crash recovery, concurrent claims, disable-while-queued, and identity replacement.

### Task 5: Web API and authentication

**Files:** `internal/httpapi/`, API and security tests

1. Generate a 32-byte setup token on unconfigured startup, retain only its hash for 30 minutes, consume it atomically, and disable setup afterward.
2. Create the sole administrator with Argon2id and implement login/logout/session rotation, hashed opaque sessions, idle/absolute expiry, CSRF/Origin checks, and login throttling.
3. Expose setup, connection testing, accounts, policy updates, batch enable/disable, synchronization, quota refresh, guarded manual execution, cycles, events, settings, health, and readiness handlers.
4. Validate all setting ranges, reject media models, atomically test connection changes before saving, and never return encrypted or plaintext API keys.
5. Test JSON envelopes, method/content-type limits, unauthorized/CSRF flows, session fixation, redaction, locked-key readiness, and scheduler wakeups.

### Task 6: Vue management panel

**Files:** `web/src/`, frontend unit and Playwright tests

1. Build setup and login screens, then a dense accounts-first application shell with summary metrics, search, filters, selection, and batch policy actions.
2. Build the account detail drawer with quota, next action, effective settings, cycles, attempts, refresh, and guarded run controls.
3. Build event and settings views with connection validation, model suggestions/manual entry, global defaults, warnings, and secret write-only behavior.
4. Use restrained white/gray styling, limited blue actions, semantic statuses, Lucide icons/tooltips, stable control dimensions, and responsive table/card transformations.
5. Test components and forms with Vitest; test setup, login, account configuration, events, error recovery, deep-link fallback, and 404 behavior in Playwright at 1440x900 and 390x844.

### Task 7: Packaging and operations

**Files:** `packaging/`, `scripts/install.sh`, `README.md`, `SECURITY.md`, `LICENSE`, `THIRD_PARTY_NOTICES.md`

1. Add a hardened systemd unit using a dedicated user, StateDirectory, root-owned EnvironmentFile, time/network dependencies, and a 30-second stop timeout.
2. Add `keygen`, `serve`, and `version` commands; make installation generate the master key, preserve configuration, and start the unit.
3. Make upgrades stop the service, archive the state directory, atomically replace the binary, reload systemd, and retain rollback instructions.
4. Document installation, SSH/HTTPS access, first-run token retrieval, settings, observable states, backup/restore, upgrade, request side effects, and real-account smoke testing.

### Task 8: Cloud-only validation and release

**Files:** `.github/workflows/ci.yml`

1. Pin official checkout/setup-node/setup-go actions by immutable SHA and use Node 24 plus the Go version from `go.mod`.
2. Run frontend type checks/unit tests/build, Go formatting/vet/tests/race, production-binary Playwright tests, and secret scans on GitHub-hosted Ubuntu.
3. Cross-compile with `CGO_ENABLED=0` for linux/amd64 and linux/arm64, inspect both binaries, smoke-run amd64, package systemd/install/docs, and generate SHA256SUMS.
4. Create public `MengStar-L/sub2api-auto5h`, push `main`, monitor Actions, and fix failures only through source changes and repeated cloud runs.
5. After `main` is green, push tag `v0.1.0`; publish both archives and checksums with the GitHub CLI, then verify release assets and checksums through the GitHub API.

### Self-review result

The tasks cover every approved subsystem and acceptance condition. Public protocol names, window durations, defaults, identity fields, retry semantics, security boundaries, deployment paths, cloud-only compilation, and release targets are consistent; there are no deferred placeholders.
