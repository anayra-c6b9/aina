aina/
├── CLAUDE.md                     
├── README.md                     
├── .gitignore                    
├── go.mod / go.sum                (go mod init)
├── .github/workflows/ci.yml      
├── scripts/
│   ├── check.sh                  
│   ├── build-all.sh               cross-compile
│   ├── demo-up.sh                 start the demo stack
│   ├── chaos.sh                   fault switches for the demo
│   ├── bench.sh                   k6 runs, results to CSV
│   └── demo-script.sh             repeatable live demo
│
├── cmd/aina/                     ← thin: flags in, calls internal/
│   ├── main.go                   ← first task (or the empty placeholder)
│   ├── root.go                   cobra root, --config, --admin
│   ├── init.go
│   ├── validate.go               --no-env-check, --allow-plaintext, --check-services
│   ├── run.go
│   ├── status.go                 status / logs / metrics / reload
│   └── test.go                   dry-run (real flow engine + fake client)
│
├── internal/
│   ├── config/                   structural checks only
│   │   ├── types.go              structs, one-or-many helper
│   │   ├── load.go               strict load, single document, no anchors/aliases/merge keys
│   │   ├── positions.go          path -> line:col index from the parsed tree
│   │   └── expand.go             ${ENV} and ${file:...} after parsing
│   ├── expr/                     lexer, parser, compiled expressions
│   ├── snapshot/                 compiled types (data only; may import expr)
│   ├── compile/
│   │   ├── compile.go            groups, guard sets, defaults -> snapshot
│   │   └── validate.go           semantic checks (step refs, guard order, reserved ids)
│   ├── security/                 IP allowlist, header stripping, caller ID, constant-time key check
│   ├── health/                   background checker, state keyed by service name
│   ├── breaker/                  circuit breaker, state keyed by service name
│   ├── router/
│   │   └── matcher.go            own path matcher (segments, *, {param})
│   ├── proxy/                    streaming proxy routes
│   ├── flow/
│   │   ├── flow.go               per-request context, step loop
│   │   ├── gate.go               health DOWN -> breaker OPEN -> send
│   │   ├── retry.go
│   │   ├── undo.go               own context, skips the gate
│   │   └── cache.go              guard-result cache
│   ├── client/                   interface + http.go (real) + fake.go (dry-run/tests)
│   ├── respond/                  respond blocks + fixed error format
│   ├── observe/                  trace IDs, slog, ring buffer, metrics, redact.go
│   ├── admin/                    /status /logs /metrics /reload /healthz
│   └── runtime/                  owns the snapshot pointer, listeners, reload, shutdown
│
├── demo/
│   ├── services/                 auth, inventory, orders, notifications (+ chaos switch)
│   ├── glue/                     hand-written Go version of the flow (benchmark baseline)
│   ├── nginx/nginx.conf
│   ├── aina.yaml                 the demo config
│   └── docker-compose.yml
│
├── bench/
│   ├── k6/                       one script per scenario
│   └── results/                  CSV and charts (git-ignored)
│
├── docs/
│   ├── aina-v0.3.yaml             (frozen schema)
│   ├── decisions.md               (seeded)
│   ├── config-reference.md       
│   ├── who-verifies-what.md      
│   ├── key-rotation.md           
│   └── limitations.md            
│
├── testdata/
│   ├── valid/                    sample configs that must pass
│   └── invalid/                  broken configs, each with its expected error text
│
└── Dockerfile                    