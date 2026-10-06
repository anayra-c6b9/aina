# aina

A declarative, config-driven orchestration gateway for small microservice
systems, written in Go. Read docs/aina-v0.3.yaml (the frozen schema) and
docs/decisions.md before changing behavior.

## Stack rules

- Go standard library first. No web frameworks.
- Allowed dependencies: goccy/go-yaml, spf13/cobra,
  prometheus/client_golang. Ask before adding any other.
- Pure Go, no CGO. Linux/macOS supported; Linux is the production target.

## Layout and import direction

- cmd/aina is thin: parse flags, call internal/.
- internal/{config,expr,observe,client,health,breaker} import no other
  internal package. snapshot imports expr. compile imports config, expr,
  snapshot. Nothing imports runtime or cmd.
- If an import points against this, stop and ask. Do not work around it.
- snapshot/ holds data types only.
- Planned layout: see docs/structure.md

## Behavior rules (from the schema)

- Config is strict: unknown fields, duplicate keys, multiple documents,
  anchors, aliases and merge keys are errors, reported as file:line:col.
- Secrets are expanded AFTER parsing, never by text substitution.
- Fields that take names/keys accept one value or a list.
- proxy routes stream; steps routes buffer JSON (max_body).
- Undo runs on its own context and skips the health/breaker gate.
- Never log Authorization, Cookie, API keys or bodies.

## Not in v1 (do not add)

include, OpenAPI import, JWT verification, internal rate limiting,
parallel steps, event triggers, webhook alerts, mTLS.

## Quality rules

- Run scripts/check.sh before saying a task is done.
- Every package gets tests, next to the code. Broken configs live in
  testdata/ with their expected error messages.
- Wrap errors with context. context.Context is the first parameter.
  No global state. Small functions.
- Do not claim something works unless a test or command showed it.

## Workflow

- One small task at a time. Show the plan before large changes.
- Update docs/decisions.md when a design decision is made.
