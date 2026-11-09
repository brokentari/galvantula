# Galvantula

A software factory: a spec in plain English goes in, and a working Go project
comes out. A Go runtime owns a graph of work. Claude Code runs each agent
node. A React UI shows each run live. The full plan is in
[Software Factory Plan.html](Software%20Factory%20Plan.html).

**Status: phase 3 (the planner and parallel tasks) is built and waits for its
review and its eval runs.** Phase 2 (the pipeline), phase 1 (one sandboxed
agent) and phase 0 passed their standards reviews (`docs/reviews/`). The
latest results are in `evals/results/`. The costs in them are Claude Code's
estimates at API prices, not charges on a plan.

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

Agents need Linux with bubblewrap (`bwrap`), and Go at `/usr/bin/go` and
`/usr/bin/gofmt`. The sandbox shows only `/usr`, `/etc` and the agent's own
folders, and finds commands only in `/usr/bin`. The sandbox cannot see your
home folder, so agents need their own credential.
Create one with `claude setup-token` (it uses your Claude subscription), or
use an API key:

```
export CLAUDE_CODE_OAUTH_TOKEN=...   # or ANTHROPIC_API_KEY=...
go run ./cmd/factory serve
go run ./cmd/factory start -spec evals/wordfreq/spec.md graphs/phase1.yaml
```

On macOS, or any host without `bwrap`, run the server in a Linux container
instead. Put the token from `claude setup-token` in a file named `claude-token`
(git ignores it), then run `scripts/docker-factory.sh` in place of `go run
./cmd/factory serve`. `docker/Dockerfile` has bwrap, Go and `claude`. The
script gives the container the three Docker security options that bwrap
needs and publishes port 7070 to your machine only. Run `factory start` from
the host as usual.

`graphs/phase2.yaml` is the full pipeline: an architect writes the interfaces,
the acceptance tests and `CONTRACT.md`; you approve the contract at a gate
(the log shows it, and the file is in the run's repo); an implementer fills in
the stubs and may not change the tests; a read-only review and a fix answer
its findings; verify runs the whole suite with the race detector. Add
`-auto-approve` to `factory start` to skip the gate, as the evals do.

`graphs/phase3.yaml` adds a planner after the contract gate. It splits the
work into tasks, one per group of packages. You approve the task graph at a
second gate (evals skip it with `-auto-approve`). Tasks that do not depend on
each other run at the same time, each in its own worktree, and the runtime
merges each into main when its check passes.

`graphs/phase4.yaml` has the same nodes and adds replanning. When a task
fails its last retry, the runtime takes it out, with the tasks that depend on
it, and runs the planner again for their packages. The tasks that stay are not
touched, and a new task may depend on one of them. `budget.replans` in
`guardrails.yaml` limits how many times a run may do this. Past the limit, a
failed task fails the run. Each replan is a new version of the graph. The UI
has a graph version menu that shows what each version added, changed and
replaced.

At the contract gate you may reject with notes. The runtime then sends the
contract back to the architect: main goes back to the standards commit, and
the architect gets your notes and the contract you rejected. `budget.revisions`
in `guardrails.yaml` limits this. A rejection with no notes, or past the limit,
ends the run.

## Run the factory from Claude Code

`plugin/` is a Claude Code plugin. It has an MCP server (`factory mcp`) and a
`/factory` skill. The server runs on your machine and calls the factory's HTTP
API, so Claude Code and the UI show the same runs. Its tools:

| tool | what it does |
|---|---|
| `factory_start` | starts a run from a spec |
| `factory_runs` | lists the runs |
| `factory_status` | shows a run, and for each waiting gate what you must read to decide |
| `factory_logs` | shows the last log lines of a node, or of every node |
| `factory_approve` | approves or rejects a gate, with notes |

Install the program once. The plugin runs `factory mcp` from your `PATH`, and
the graphs are built into it:

```
go install ./cmd/factory
```

Start the server, then start Claude Code in any folder with the plugin:

```
CLAUDE_CODE_OAUTH_TOKEN=... factory serve
claude --plugin-dir /path/to/galvantula/plugin
```

Then type `/factory` and describe the program. `factory serve` still needs to
run from this repo, where `guardrails.yaml` and `ui/dist` are.

Each run's product is a git repo in `runs/<run>/repo`. The agent works in
`runs/<run>/nodes/<node>`; the runtime commits and merges its work after the
check passes. `guardrails.yaml` sets the spending and time limits for each
attempt and for the whole run. Restart the server after you change it.

The server's API has no login. Anyone who can reach it can start agents
with your credential, so run the server only on a machine that you alone use.

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

`guardrails.yaml` sets the target Go version, the model and effort for each
role, and `concise_output`, which tells every agent to keep its replies short. Sonnet 5.5 builds. Opus 5.5 writes the eval tests, and it is never a
builder.

| command | what it does |
|---|---|
| `go run ./cmd/factory standards -o AGENTS.md` | rewrites the standards block |
| `go run ./cmd/factory mcp` | the MCP server for Claude Code; the plugin starts it |
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
