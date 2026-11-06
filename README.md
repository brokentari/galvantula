# Galvantula

A software factory: a spec in plain English goes in, and a working Go project
comes out. A Go runtime owns a graph of work. Claude Code runs each agent
node. A React UI shows each run live. The full plan is in
[Software Factory Plan.html](Software%20Factory%20Plan.html).

**Status: phase 1 (one sandboxed agent) is built. Its first eval runs
passed the hidden tests in 31 of 31 runs: one trial run of wordfreq, then
10 runs of each eval (`evals/results/2026-11-06.jsonl`).** Phase 0 passed its standards review
(`docs/reviews/phase-0.md`).

## Run the demo

You need Go 1.27 and Node 22 or later.

```
cd ui && npm ci && npm run build && cd ..
go run ./cmd/factory serve
```

In a second terminal:

```
go run ./cmd/factory start -fake fakes/factory.yaml graphs/factory.yaml
```

Open http://localhost:7070. The run stops at two gates. Approve each one in
the side panel. After the second gate, `pkg-store` and `pkg-api` run at the
same time, and `pkg-api` fails once and retries.

To see crash recovery, stop the server while the packages run (Ctrl-C or
`kill -9`), then start it again. The run continues from its event log. Work
that finished does not run again.

Use **Replay** and the slider to step through any run, event by event.

## Run a real agent

Agents run Claude Code inside a bubblewrap sandbox, which cannot see your
home folder, so they need their own credential. Create one with
`claude setup-token` (it uses your Claude subscription), or use an API key:

```
export CLAUDE_CODE_OAUTH_TOKEN=...   # or ANTHROPIC_API_KEY=...
go run ./cmd/factory serve
go run ./cmd/factory start -spec evals/wordfreq/spec.md graphs/phase1.yaml
```

Each run's product is a git repo in `runs/<run>/repo`. The agent works in
`runs/<run>/nodes/<node>`; the runtime commits and merges its work after the
check passes. `guardrails.yaml` sets each attempt's spending and time limits.

To score the factory, run the evals against a running server:

```
go run ./cmd/factory eval -n 10 evals/wordfreq evals/todo evals/csvq
```

It records one line per run in `evals/results/<date>.jsonl` and prints the
pass rate, median cost and median time for each eval.

## How the parts connect

| path | what it is |
|---|---|
| `graphs/factory.yaml` | the outer factory graph |
| `fakes/factory.yaml` | the fake executor's script for that graph |
| `internal/graph` | graph types, validation, and filling a slot with a plan's nodes |
| `internal/event` | event types, the SQLite event log, and the code that rebuilds a run's state from its events |
| `internal/runner` | the runtime: scheduling, worker limit, retries, gates, resume |
| `internal/executor` | the executor interface, the fake executor, and the Claude Code executor with its check |
| `internal/sandbox`, `internal/workspace` | the bubblewrap sandbox; the product repo and its worktrees |
| `internal/server` | HTTP API and the server-sent event stream |
| `cmd/factory` | `serve`, `start`, `eval`, `validate`, `standards` |
| `cmd/gen-ts` | writes `ui/src/gen/events.ts` from the Go types |
| `cmd/evalgen`, `cmd/phasereview` | eval test authoring and phase reviews, through headless Claude Code (`internal/claude`) |
| `standards/` | the pinned skills and the renderer for the standards block |
| `ui/` | Vite, React and React Flow |
| `evals/` | eval specs with hidden tests; see [evals/README.md](evals/README.md) |

The event log is the only state. Each event records the runtime's decisions,
such as whether a failed node will retry. So the UI code that rebuilds state
from events (`ui/src/state.ts`) is a plain mapping with no rules of its own.

## Standards and models

`AGENTS.md` holds the rules every agent follows here, and `CLAUDE.md`
imports it. Its standards block comes from pinned copies of three skills in
`standards/vendor/`: ponytail (least code), go-modern-guidelines (modern Go)
and orwell-writing (plain English). Phase 1's standards node writes the same
block into every product.

`guardrails.yaml` sets the target Go version and the model and effort for each
role. Sonnet 5.5 builds. Opus 5.5 writes the eval tests, and it is never a
builder.

| command | what it does |
|---|---|
| `go run ./cmd/factory standards -o AGENTS.md` | rewrites the standards block |
| `go run ./cmd/evalgen evals/<name>` | writes an eval's hidden tests with the eval author model, then checks them |
| `go run ./cmd/phasereview -phase phase-N -base <ref>` | a new session with the reviewer model reviews a phase; see `AGENTS.md` |

## Checks

```
go vet ./... && go test -race ./...
GOFLAGS=-buildvcs=false go fix -diff ./...   # modern Go 1.27, same check as the factory runs
go generate ./...                            # after changing event or graph types
cd ui && npm run build                       # type-checks, then builds
```

For UI work, run `npm run dev` in `ui/` and open http://localhost:5173. It
sends `/api` to the Go server on port 7070.
