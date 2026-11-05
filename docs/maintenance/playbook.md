# Modernization Playbook

How common-go is kept current. Run it **once a quarter**, and out-of-band whenever a reachable vulnerability shows up. Each run is a *cycle*: scan → review → triage → plan → execute → verify → adopt downstream → release → retro.

The goal is for common-go to be the team's **standard Go service foundation**: new services start from `examples/`, and existing services get improvements by bumping one module.

---

## 1. What must never change (the invariants)

A cycle may change *how* these work, but never break them:

1. **Proto-first, one implementation, many protocols.** One gRPC service implementation is served as native gRPC, grpc-gateway REST, ConnectRPC (`RegisterConnectService`, no extra codegen) and auto-HTTP, all from the `grpcmux.Server`.
2. **URI-driven dependencies.** `name: scheme://host/path?opts` in config, plus typed struct fields, wired by `dependencies.Init`. Backends self-register by scheme, closers register automatically, and unknown query parameters are ignored.
3. **`async.Future`.** Typed, errgroup-backed fan-out/await, used for dependency init and server start.
4. **Defaults on, options to tune.** Logging, metrics, tracing hooks, health, auth, CORS and graceful shutdown work out of the box and are tuned through `grpcmux.With*`, `grpcmux.Config` fields and URI query parameters.
5. **Source compatibility for the reference consumer.** `~/Workspace/projects/auth/backend` must build, vet and test green against every release (§6).

## 2. Policies

| Topic | Policy |
|---|---|
| Go version | `go.mod` `go` directive = latest stable **minor** once it has had one patch release. CI and the scan use the latest **patch**. Stdlib security fixes ship by bumping the patch version, so they never wait for a cycle. |
| Dependencies | All direct dependencies at their latest release at the end of each cycle. Minor/major jumps require reading the release notes (§3.1). Indirect dependencies follow `go mod tidy`. A dependency that only exists for docs or examples must live in the `examples/` module, never in the library module. |
| Vulnerabilities | A *reachable* `govulncheck` finding is P0. It gets an out-of-band patch release within a week, and the reference consumer is bumped. Non-reachable findings are fixed in the next cycle. |
| API compatibility | No exported identifier used by the reference consumer is removed or changes signature. Replacements are added alongside, and the old one gets `// Deprecated: use X (since vA.B.C).` Deprecated identifiers can be removed after **two cycles**, and only once auth no longer uses them. |
| Behavior changes | Every observable behavior change (defaults, retries, timeouts, shutdown, wire format) is listed in `docs/upgrades/<cycle>.md` with a migration note. A risky change ships behind an opt-in option for one cycle before it becomes the default. |
| Versioning | Semver tags `v0.MINOR.PATCH`: one minor per cycle, patches for out-of-band fixes. Consumers pin tags, not pseudo-versions. |
| Language | All code comments, Markdown, proto comments and example sources are in **English**. Enforced by scan §7 and CI. |
| Layout | Library packages keep their import paths. Docs live in `docs/` (guides, upgrades, maintenance). Runnable samples live in the separate `examples/` module. |

## 3. The cycle

### 3.1 Prepare (≈1h)
- Bump the pinned tool versions at the top of `scripts/modernize-scan.sh`.
- Skim the upstream changes made since the last cycle and note anything relevant in the cycle plan:
  - Go release notes and the "Go 1.N" blog post (stdlib APIs, `go fix` modernizers, runtime/net/http changes).
  - Release notes for grpc-go, connect-go, grpc-gateway, golang.org/x/net, opentelemetry-go (and contrib), mongo-go-driver, IBM/sarama, rueidis, prometheus/client_golang, and buf/protovalidate.
  - The Kubernetes/Gateway API docs on graceful termination and probes, and the Connect protocol spec.
- Re-read §5 (idioms) and add anything that became standard practice this quarter.

### 3.2 Scan (≈15 min, read-only)
```bash
scripts/modernize-scan.sh docs/maintenance/scans/$(date +%F).md
```
The report covers build/vet, `go fix -diff` modernizers, dependency updates, reachable vulnerabilities, lint, race tests, non-English text, repository hygiene, and the auth build/vet/test against the local checkout, plus auth's usage of the common-go API. Commit the report. Diffing it against the previous scan *is* the quarter's to-do list.

### 3.3 Review (走查) (≈½–1 day)
Read the code against the checklist below. Every finding needs **evidence**: a failing test, a repro program, or a quoted line. Hypotheses go in a separate "to verify" list. Put one-off repro programs in a temp dir with a `replace` to the local checkout, never inside the repo.

| Area | Questions to answer |
|---|---|
| Lifecycle (`graceful`, `grpcmux.Server.Close`) | Does SIGTERM actually drain (repro: send SIGTERM to a real binary and assert the closers ran)? Is the order health NOT_SERVING → PRE_STOP → HTTP Shutdown + gRPC GracefulStop → closers? Is there one overall deadline? Is the registry race-free? |
| HTTP server | Are HTTP/2 and h2c configured once, through stdlib (`http.Server.Protocols`/`HTTP2`)? Are timeouts and limits configurable, with streaming-safe defaults? Is the handler layering (middleware once, WebSocket dispatch, otelhttp) still right? |
| gRPC server | Keepalive params and enforcement policy compatible with clients? Interceptor chain order (recovery outermost, then logging, metrics, auth, validator)? Is the in-memory `Conn()` usable for user services? |
| ConnectRPC / auto-HTTP bridges | Is it spec-compliant (compression, `Connect-Timeout-Ms`, protocol version header, GET, error details)? Do the bridges run the same interceptors as native gRPC? Any per-request reflection or allocation on the hot path? Is there a conformance test using a real connect-go client? |
| gRPC client | Client keepalive, retries/backoff, `waitForReady`, TLS defaults, load-balancing policy, tracing. Are URI query parameters documented and tested? |
| HTTP client (`dependencies/http`) | Per-client tuned transport (no mutation of `http.DefaultTransport`)? Pool sizes, HTTP/2, proxy handling without a transport per request, retry policy (idempotency, backoff, 5xx), tracing actually wired? |
| DI (`dependencies`, `config`) | Reflection paths covered by tests? Error messages actionable? Concurrent init safe? Closers registered for every resource? |
| `async` | Still the smallest correct API? Any new stdlib primitive (`sync.WaitGroup.Go`, `errgroup` features) that makes part of it redundant? |
| Data adapters (sql, mongo, redis, kafka, mqlru) | Driver API changes, deprecated options, context propagation, pool settings exposed through the URI. |
| Observability | Log schema stable (`action`, `request_id`, low-cardinality action)? OTel semantic conventions current? Metric names stable? |
| Security | govulncheck, TLS minimums, header/body limits, panic recovery, secrets never logged (`logBody` masking). |
| Docs & examples | Does every doc command run? Do import paths exist? Is there any stale route/file reference? Do examples build in CI? |

### 3.4 Triage
| Severity | Meaning | Handling |
|---|---|---|
| **P0** | Reachable vulnerability; data loss; production behavior is wrong (for example, a broken drain) | Fix this cycle, first. A patch release is allowed out-of-band. |
| **P1** | Latent bug, resource leak, global-state mutation, wrong default | Fix this cycle. |
| **P2** | Missing modern capability, inconsistency between protocol paths, performance | Fix this cycle if it fits, otherwise next. |
| **P3** | Idiom refresh, docs, hygiene | Batch into the cycle. |

Tag each finding with a category: `security` / `correctness` / `network` / `modernization` / `hygiene` / `docs`.

### 3.5 Plan
Copy `docs/maintenance/cycles/TEMPLATE.md` to `docs/maintenance/cycles/<YYYY>-Q<n>.md` and fill in: the baseline scan link, findings with evidence, the work breakdown, the auth adaptation, behavior changes, decisions needed, and the schedule. Get the owner's sign-off before executing.

### 3.6 Execute
- Use a single branch, `chore/modernize-<YYYY>-q<n>`, with one PR per workstream so each can be bisected and reverted.
- Order: **safety net (tests, CI) → P0 → dependency bump (its own commit) → P1 → P2 → docs/examples → release notes**.
- A behavior change lands together with its test and its `docs/upgrades/` entry, in the same commit.
- Write the tests that reproduce a bug *before* writing the fix.

### 3.7 Verify (gates; all must pass)
1. `go build ./...`, `go vet ./...`, `go test -race ./...` in the library and in `examples/`.
2. `govulncheck ./...` reports no reachable vulnerabilities.
3. `golangci-lint run` is clean with the repo's `.golangci.yml`.
4. `go fix -diff ./...` is empty, or the remaining items are justified in the cycle plan.
5. No CJK text (scan §7 is empty).
6. Auth builds, vets and tests green against the local checkout (scan §9).
7. A new scan is committed, and diffing it against the baseline shows every planned item closed.

### 3.8 Adopt in the reference consumer (auth)
1. Develop against the local checkout with a throwaway workspace (never commit a `go.work` into auth):
   ```bash
   cd ~/Workspace/projects/auth/backend
   W=$(mktemp -d)/go.work
   printf 'go 1.27\n\nuse (\n\t%s\n\t%s\n)\n' "$PWD" "$HOME/Workspace/projects/common-go" > "$W"
   GOWORK=$W go build ./... && GOWORK=$W go vet ./... && GOWORK=$W go test ./...
   ```
2. Adjust auth's code **only** where common-go deprecated something, changed behavior, or now provides what auth hand-rolled (list each change in the cycle plan). Follow auth's own `CLAUDE.md` conventions.
3. After the common-go tag exists, run `go get github.com/ti/common-go@vX.Y.Z && go mod tidy` in auth and run auth's checks. Committing in auth is a separate step with the owner's approval. Deploying is out of scope for this playbook.

### 3.9 Release
Update `CHANGELOG.md` and `docs/upgrades/<cycle>.md`, refresh `CLAUDE.md` if the architecture changed, then propose the tag `v0.N.0`. Tags are pushed only with the owner's approval.

### 3.10 Retro (≈15 min)
Update this playbook. Add new checklist questions for anything the review missed, and remove ones that turned into tests or CI gates.

## 4. Out-of-band security patch (any time)
Triggered when a reachable `govulncheck` finding appears (CI, a scan, or an advisory):
bump the affected module or Go patch version → run the §3.7 gates → tag `v0.N.(P+1)` → bump auth → done. Nothing else rides along with it.

## 5. "Current Go" idioms to apply (refresh every cycle)
Run `go fix -diff ./...` first. It applies the toolchain's own modernizers. Beyond that, prefer:

- **Stdlib over dependencies:** `log/slog`, `encoding/json/v2`, `uuid`, `math/rand/v2`, `maps`/`slices`/`iter`, `net/http` method+wildcard routing, `http.Protocols` + `http.HTTP2Config` for HTTP/2 and h2c, `os.Root` for confined file access.
- **Concurrency:** `signal.NotifyContext`, `context.AfterFunc`, `context.WithoutCancel`, `sync.WaitGroup.Go`, `sync.OnceValue(s)`, `errors.Join`, generic methods (Go 1.27) instead of reflection where the types are static.
- **Testing:** `testing/synctest` for time-dependent code (keepalive, retries, shutdown), `b.Loop()` benchmarks, `t.Context()`, and subprocess tests for signal handling.
- **Networking:** a paired gRPC client keepalive and server enforcement policy, `MaxConnectionAge` + `MaxConnectionAgeGrace`, per-client tuned `http.Transport`, retries with jittered backoff restricted to idempotent calls, and a drain sequence aligned with Kubernetes `terminationGracePeriodSeconds`.
- **Protocols:** Connect protocol compliance (compression, timeouts, GET, error details), `protovalidate` instead of the deprecated `protoc-gen-validate` in new protos, buf v2 configs.
- **Observability:** current OpenTelemetry semantic conventions, low-cardinality span/log/metric names.

## 6. Reference consumer
`~/Workspace/projects/auth/backend` (module `github.com/ti/authentication`) is the compatibility target. It uses `log`, `database`/`query`/`mongo`/`codecs`, `grpcmux` (`NewServer`, `RegisterConnectService` ×27, CORS/auth/camelCase/pre-HTTP middleware options), `grpcmux/mux` (`WriteHTTPErrorResponse` ×52, `AuthInfo*`, error codes), `dependencies/http` (including `WithProxy`), `redis`, `broker`/`kafka`, `mqlru`, `config` and `tools/{routerlimit,ip}`. Scan §9 regenerates this list every cycle, so watch it for new usage.
