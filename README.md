# Galvantula

A software factory: a spec in plain English goes in, and a working Go project
comes out. A Go runtime owns a graph of work. Claude Code runs each agent
node. A React UI shows each run live. The full plan is in
[Software Factory Plan.md](Software%20Factory%20Plan.md).

**Status: phase 0 (skeleton) is done.** There is no LLM yet. A fake executor
plays a script, so you can watch the runtime and the UI work.

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

## How it fits together

| path | what it is |
|---|---|
| `graphs/factory.yaml` | the outer factory graph |
| `fakes/factory.yaml` | the fake executor's script for that graph |
| `internal/graph` | graph types, validation, and filling a slot with a plan's nodes |
| `internal/event` | event types, the SQLite event log, and the fold from events to state |
| `internal/runner` | the runtime: scheduling, worker limit, retries, gates, resume |
| `internal/executor` | the executor interface and the fake executor |
| `internal/server` | HTTP API and the server-sent event stream |
| `cmd/factory` | `serve`, `start`, `validate` |
| `cmd/gen-ts` | writes `ui/src/gen/events.ts` from the Go types |
| `ui/` | Vite, React and React Flow |
| `evals/` | eval specs with hidden tests; see [evals/README.md](evals/README.md) |

The event log is the only state. Each event records the runtime's decisions,
such as whether a failed node will retry. So the UI's fold (`ui/src/state.ts`)
is a plain mapping with no rules of its own.

## Checks

```
go vet ./... && go test -race ./...
GOFLAGS=-buildvcs=false go fix -diff ./...   # modern Go 1.27, same check as the factory runs
go generate ./...                            # after changing event or graph types
cd ui && npm run build                       # type-checks, then builds
```

For UI work, run `npm run dev` in `ui/` and open http://localhost:5173. It
sends `/api` to the Go server on port 7070.
