# Decision log

Format: ID, decision, reason, alternatives rejected. Newest at the bottom.
Entries D01-D20 were made during planning (before 2026-10-06).

## D01 Product shape

Decision: aina is a config-driven orchestration gateway (router + translator

- process coordinator), mesh-inspired but not a service mesh. One Go binary
  plus CLI, behind nginx, for small systems (target 3-10 services, no
  Kubernetes). The range is a design target, not a limit.
  Why: fits a 2-credit, 3-month project; closes the gap between hand-written
  glue and a full mesh.
  Rejected: sidecar-style mesh (too large), CI/CD pipeline project (less
  original).

## D02 Placement and listeners

Decision: sits behind nginx. Three listeners: edge (nginx), internal
(services, identified by API key), admin. Each route lives on exactly one
listener; need both -> write two routes.
Why: nginx already does TLS, rate limiting, CORS; internal traffic never
passes nginx.
Rejected: replacing nginx; `listener: both` (ambiguous caller identity).

## D03 Auth model

Decision: user tokens are passed through untouched (auth: passthrough).
Services identify by API key (api_key: one key or a list, for rotation).
Client-supplied identity headers are stripped in nginx and again in aina.
Why: keeps aina out of auth territory; verification is the developer's
choice (nginx auth_request, a guard step, or the services).
Rejected: built-in JWT verification (future work).

## D04 Guards and undo

Decision: guard steps (read-only checks) run before any action step. Failed
actions trigger undo steps in reverse order (saga pattern, best-effort).
Why: fail before side effects; partial failure is the core problem.
Limits: no real transactions; steps should be idempotent; undo runs on its
own context and skips the health/breaker gate.

## D05 Scope of v1

Decision: core + undo, guard-result cache, circuit breaker, dry-run command.
Rejected / future work: OpenAPI import, `include`, parallel steps, event
triggers, internal rate limiting, JWT verification, mTLS, webhook alerts,
automatic key rotation, persisted rollback journal.

## D06 Body handling

Decision: proxy routes stream any body; steps routes buffer JSON only, with
max_body (default 1MB, 413 when exceeded).
Why: mapping needs the whole JSON; streaming keeps memory flat for files.
Pattern for file + data: two requests.

## D07 Expression engine

Decision: write our own small engine (lookups + == != < > <= >= contains
exists and or not), compiled once at load.
Why: tiny grammar, structured errors, defensible in the report.
Rejected: expr-lang/expr (too powerful, weaker "small safe language" story).

## D08 Router

Decision: own path matcher (segments, \*, {param}).
Why: structured NO_ROUTE errors; ServeMux emits its own 404/405, redirects
unclean paths, and panics on conflicts.

## D09 Config format and strictness

Decision: one YAML file, strict parsing. Rejected: unknown fields, duplicate
keys, multiple documents, anchors/aliases/merge keys, wrong types.
Secrets: "${ENV}" or "${file:/path}", expanded AFTER parsing. Always quote
${...} and {param} paths inside { } maps.
Rejected: `include` (small target systems; complicates line numbers).

## D10 One-or-many rule

Decision: api_key, callers, use_guards, forward, retry_on accept one value
or a list. `any` is reserved (callers: any).

## D11 Architecture

Decision: control plane compiles YAML into an immutable Snapshot; requests
use the current snapshot; reload swaps it atomically. Health and breaker
state are keyed by service name and survive reloads; guard cache is cleared.
Listeners set only ReadHeaderTimeout and IdleTimeout; deadlines are per
route via context.

## D12 Project layout and import direction

Decision: see CLAUDE.md. config, expr, observe, client, health, breaker
import no other internal package; snapshot imports expr; nothing imports
runtime or cmd.
Semantic validation lives in compile (needs expanded groups/guard sets).

## D13 Dependencies

Decision: Go standard library plus goccy/go-yaml, spf13/cobra,
prometheus/client_golang (three direct; the last brings transitive modules).
Why: goccy/go-yaml verified by experiment: strict fields, duplicate keys,
line/column positions, paths into list items, document count, anchor
detection (2 test rounds, 2026-10-06).

## D14 Platforms

Decision: pure Go, no CGO. Linux is the supported production target; macOS
for development. CI runs on ubuntu-latest.

## D15 Tooling and demo

Decision: demo = 4 small Go services (auth, inventory, orders,
notifications) with chaos switches, behind nginx via docker-compose.
Benchmarks with k6. Docker is for demo/benchmark only; develop with
go run / go test.

## D16 Evaluation (as submitted in the proposal)

Decision: compare against direct calls and a hand-written glue service (plus
plain nginx proxy_pass for overhead). Other tools (Istio, Dapr, KrakenD) are
compared qualitatively. Glue baseline: define the feature checklist first;
consider a minimal and a complete version; state line-count rules.
Open: KrakenD stretch scenario, scalability test, break-even comparison.

## D17 Resilience details

Decision: retry only timeout/502/503/504 with doubling backoff and an
Idempotency-Key (<trace_id>-<step_id>); call gate order: health DOWN ->
breaker OPEN -> send; downstream 4xx propagated, 5xx mapped to 502/503/504.

## D18 Logging and security hygiene

Decision: never log Authorization, Cookie, API keys, or bodies. Trace ID
reuses X-Request-Id from nginx when present.

## D19 Error format

Decision: one fixed JSON error (code, message, route, failed_step,
completed_steps, undone_steps, undo_failed_steps, downstream_status,
trace_id). Config errors print as file:line:col with hint and source line.

## D20 Schedule

Decision: 12 weeks + final 15 days; sponsor checkpoints at weeks 1, 6, 12;
build demo services and a first benchmark around week 4.
