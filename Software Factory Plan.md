# Software Factory Plan

Natural-language spec in, working Go project out. A Go runtime owns the graph; Claude Code runs the loop inside each agent node; a TypeScript UI shows the run live.

runtime · Go | UI · TypeScript + React | node worker · claude -p | sandbox · bubblewrap | first target · Go CLIs

> The HTML version has the two diagrams. This file has the same text.

## Ground rules

- **Tests and standards are frozen.** The architect writes the interfaces and the acceptance tests. The standards node writes `AGENTS.md`. You approve both. No later node may change them.
- **Tooling decides pass or fail.** Build, vet, gofmt and tests decide. The review agent can only open a fix node. It never passes or fails a run.
- **Two human gates.** You approve the contract (interfaces and tests). Then you approve the task DAG and its cost estimate.
- **Workers run sandboxed.** Each `claude` process runs in bubblewrap. It sees only its worktree and the Go caches.
- **The graph is a file.** The outer graph lives in `graphs/factory.yaml`, so the eval set can compare one graph version with another.
- **The event log is the truth.** The UI, crash recovery and replay all read the same append-only log. Phase 0's fake run is a recorded log.

## System architecture

Components inside `factory/` (Go): server (HTTP · SSE · MCP · gates), planner (JSON schema → DAG), graph (validate · versions), store (SQLite event log), runtime (scheduler · worker cap · budgets · timeouts · retries · resume), workspace (worktree · merge), verify (tests · frozen files), executor (Executor interface). Outside: Claude Code over MCP, the TypeScript UI over SSE (events out, approvals in), and the `claude --bare -p` workers inside a bwrap sandbox.

The runtime is the core. It owns the graph, picks nodes whose dependencies are done, and hands each one to the executor. Every agent node runs as a separate Claude Code process, sandboxed, in its own git worktree. Its stream-json events go into the store, and the UI reads them from there. Verification runs real tooling, never another agent. The runtime, not the agent, commits and merges.

## The factory graph

```
standards (AGENTS.md, no LLM) ─┐
spec ──────────────────────────┴→ architect → [approve contract] → plan → [approve DAG + cost]
     → generated task DAG (each task: agent · check · merge; dependents start after their deps merge)
     → verify (full suite, -race) → review (ponytail, advisory) → done

check fails                          → retry that node (≤ N)
verify fails / review must + should  → one bounded fix node, then re-verify
fix still failing                    → planner rewrites the subgraph (≤ M), back to DAG approval
```

You write the outer graph by hand, as a file. The standards node runs first and writes the same rules into every product, so the architect, the implementers, the fix node and the review all work from one file. The planner writes only the generated nodes, as JSON that the runtime validates before anything runs. Each task node is implemented, checked and merged on its own, so a failure points at one package. A dependent node starts from the integration branch after its dependencies merge, so it builds against real code. Retry edges have caps. Replanning runs only after retries and the fix node fail, and it always goes back to you.

## Node kinds

The runtime knows five kinds of node. Each kind has one executor. The factory graph is made from these and nothing else.

| kind | what it is | how it finishes |
|---|---|---|
| agent | Claude Code in a worktree | gets a brief, edits files, exits; done only when its check passes |
| tool | real tooling, no LLM | checks run `go build`, `go vet`, `gofmt -l`, `go fix -diff`, `go test`, frozen-file hashes; the exit code decides. The standards node is also a tool node |
| gate | waits for you | approve or reject from the UI or the MCP tool; a reject carries your notes back to the node before it |
| merge | runtime git | commits a node's diff and merges it into the integration branch in dependency order; a conflict counts as a failure of that node |
| plan | agent that emits graph | output must match a JSON schema and pass the graph rules before it becomes part of the graph |

## Rules that keep parallel work honest

- **Frozen tests.** When you approve the contract, the runtime records a hash of each test file and interface file. A check fails if any of those files changes, or if a new `t.Skip` appears.
- **Red before green.** The architect's tests must fail against the empty stubs. A test that passes on a stub tests nothing, so the contract gate rejects it.
- **Write scope.** Each task node owns a set of package directories. Nodes that can run at the same time must own different sets. The runtime rejects a diff that touches a file outside the node's scope. Because no two nodes write the same file, merges should not conflict.
- **The architect owns `go.mod`.** Use the standard library first. The architect may propose a module, with a one-line reason, and you accept or refuse it at the contract gate. Implementer nodes run with `-mod=readonly` and `GOPROXY=off`, so they cannot add dependencies. A node that needs a new dependency fails with that reason, and the planner handles it in a replan.
- **Check per node, verify per run.** A node's check runs its own package tests, a build of the whole module, and `go fix -diff` on its packages. The final verify runs the full suite with `-race`, and confirms that `go.mod` says `go 1.27` and lists only approved modules.
- **Agents may add tests, not change them.** An implementer may add new `_test.go` files inside its write scope, as ponytail asks for risky logic. The frozen tests stay as they are.
- **Agents don't run git.** The runtime commits after a check passes. Inside the sandbox, the worktree's `.git` link points outside, so git cannot work there anyway.

## Product standards

The standards node runs before the architect. It is a tool node: it uses no LLM and never fetches anything at run time. It builds the rules for the product from pinned copies of three skills, and writes them into the product repo. You see the result at the contract gate, and from then on the files are frozen like the tests.

| source | what it covers | how it is enforced |
|---|---|---|
| [ponytail](https://github.com/DietrichGebert/ponytail) (MIT) | **Writing:** the "lazy senior dev" rules go into `AGENTS.md` for the architect, the implementers and the fix node. **Review:** [`ponytail-review`](https://github.com/DietrichGebert/ponytail/blob/main/skills/ponytail-review/SKILL.md) is the review node's prompt and output format: numbered findings in Must fix, Should fix and Nice to have, then a verdict and `Lean: -N lines`. | Must fix and Should fix findings open the fix node. A `shortcut:` comment that names its limit is a decision, not a finding. |
| [go-modern-guidelines](https://github.com/JetBrains/go-modern-guidelines) (Apache-2.0) | Modern Go 1.27. 25 of the 54 guidelines are `go fix` modernizers. The other 29 (for example `encoding/json/v2`, stdlib `uuid`, `cmp.Or`, `errors.Join`) are rendered from the pinned `guidelines.json` into `AGENTS.md`. | The check runs `go fix -diff`, which fails when it suggests any change. The review reports a missed guidance rule as Should fix. The factory never runs JetBrains' CLI, so nothing gets installed inside the sandbox. |
| [orwell-writing](https://github.com/tamdogood/builder-essential-skills/blob/main/skills/orwell-writing/SKILL.md) (MIT) | Plain English for the README, doc comments, CLI help and usage text, error messages and `NOTES.md`. Code, identifiers and Go conventions stay as they are: error strings start in lower case and have no final period. | A separate Prose group in the review. Its findings count as Should fix. They stay apart from the code findings, because ponytail-review leaves out style. |

Files the node writes:

```
AGENTS.md              rules for anyone working on the product
CLAUDE.md              one line: @AGENTS.md
.factory/standards/
  review.md            ponytail-review + the Prose group
  lock.json            repo, commit, sha256 for each source
```

Workers run with `--bare`, which skips `CLAUDE.md`. So the executor passes `--append-system-prompt-file AGENTS.md` to every writing node, and the review node gets `review.md`. `CLAUDE.md` is for later: when you open the finished product in Claude Code, you get the same rules.

`AGENTS.md` ships with the product, so it holds only product rules. Factory rules (write scope, frozen files) go in each node's brief.

When rules disagree, the first one wins:

1. The frozen tests and the contract.
2. Ponytail's never-cut list: validation at trust boundaries, error handling that prevents data loss, security.
3. The Go guidelines. Skip one only if it would not compile or would change behavior, and say so in `NOTES.md`.
4. Ponytail's "smallest complete change".
5. Orwell, for prose only.

**Updates:** the sources are kept in `standards/vendor/` in the factory repo, at the commits below. `factory standards bump` moves a pin and shows the diff. Run the eval set before you accept a bump. Each run records its standards version, as it records its graph version.

```yaml
# guardrails.yaml: the settings you chose, in one file
go:
  version: "1.27"              # go directive in go.mod; guidelines filtered to it
  fix_check: true              # go fix -diff must be empty
deps:
  policy: stdlib-first         # a new module needs a reason and your approval at the contract gate
  approved: []                 # filled in from gate approvals
review:
  fix_on: [must, should]       # nice-to-have is reported only
  prose: true                  # Orwell group; counts as should
tests:
  agent_tests: in-scope        # new _test.go files in the node's own packages
models:                        # one model per role; the eval author must build nothing
  architect:   { model: claude-sonnet-5-5, effort: high }
  planner:     { model: claude-sonnet-5-5, effort: high }
  implementer: { model: claude-sonnet-5-5, effort: medium }
  reviewer:    { model: claude-sonnet-5-5, effort: high }
  eval_author: { model: claude-opus-5-5,   effort: xhigh }
sources:
  ponytail:  { repo: DietrichGebert/ponytail,             commit: 96608e078b2d5ea607c5c3b8b5a54d1c1fba6846 }
  go-modern: { repo: JetBrains/go-modern-guidelines,      commit: 155dc7ca10da5e1f6c841503086957b1b37f5815 }
  orwell:    { repo: tamdogood/builder-essential-skills,  commit: 1be9984bdafbb68eaff400bdd82773a1d41ee7ec }
```

The runtime reads this file at the start of each run and stores it with the run, so the eval set can compare one set of guardrails with another.

## Planner and replanning

A DAG is accepted only if:

- it has no cycles, and every dependency names a node that exists
- each package in the contract has exactly one owner node
- nodes that can run at the same time have separate write scopes
- it is within the size limits (at most K nodes, depth at most D)

The DAG gate shows the estimated cost: the number of nodes times the per-node budget, plus the expected retries.

A rewrite may only:

- replace failed nodes and the nodes after them that have not started; finished nodes cannot change
- happen M times per run at most; each rewrite is a new graph version
- keep the contract as it is; if the contract is wrong, the run goes back to the contract gate

The planner gets a short summary of each failure: the test output and the agent's notes. It does not get full transcripts.

## What an agent node gets

In (`brief.md`):

- the spec and the approved contract
- its task, write scope, and check command
- `go doc` output for the packages it depends on
- on a retry: the failing output, trimmed

Out:

- the diff, which the runtime captures; the agent does not commit
- `NOTES.md`: what it did and any assumptions, in a few lines
- the `result` event: cost, turns, session id

Raw transcripts go to disk for each attempt. They are never given to later nodes.

## Two ways to connect Claude Code

### factory → Claude Code: Claude Code runs each agent node

`ClaudeCodeExecutor` starts one headless process per node, inside the sandbox, in that node's worktree. `--bare` keeps runs reproducible. Together with `--strict-mcp-config`, it stops a worker from loading your factory plugin and calling the factory again.

```
claude --bare -p "$(cat brief.md)" \
  --session-id "$NODE_SESSION" \
  --output-format stream-json --verbose \
  --model sonnet --max-budget-usd 2 \
  --append-system-prompt-file AGENTS.md \
  --tools "Bash,Read,Edit,Write,Glob,Grep" \
  --allowedTools "Bash(go build *),Bash(go test *),Bash(go vet *),Bash(go fix *),Bash(gofmt *)" \
  --permission-mode acceptEdits \
  --permission-prompts none \
  --strict-mcp-config
```

**Retries:** the first retry resumes the session with `--resume "$NODE_SESSION"` and the failing output, which is cheap because the context is already there. The second retry starts a new session, so the node can try a different approach.

The planner uses `--json-schema`, so Claude Code checks the DAG's shape before the runtime's own validator runs. Each role's model and effort come from `guardrails.yaml`.

Bare mode uses `ANTHROPIC_API_KEY`, so runs bill at API rates.

### Claude Code → factory: the factory is a tool inside Claude Code

The server's MCP mode, built on the official Go MCP SDK, exposes a small tool set. Package it as a plugin with a `/factory` skill. You describe what to build, approve the contract and the plan at the gates, and it runs.

```
factory_plan(spec)                   → run_id, first gate
factory_approve(gate_id, ok, notes)  → next gate or running
factory_status(run_id)               → node states, cost
factory_logs(node_id)                → transcript
```

The MCP server refuses every call when `FACTORY_RUN_ID` is set in its environment. This is a second guard against a worker calling the factory.

Build this last. Once the runtime works, it needs little code.

## Sandbox and runtime limits

### One sandbox per worker

```
bwrap --unshare-all --share-net --die-with-parent --clearenv \
  --ro-bind /usr /usr --ro-bind /etc /etc \
  --symlink usr/bin /bin --symlink usr/lib /lib \
  --symlink usr/lib64 /lib64 \
  --proc /proc --dev /dev --tmpfs /tmp \
  --ro-bind "$CLAUDE_INSTALL" "$CLAUDE_INSTALL" \
  --ro-bind "$GOMODCACHE" /go/mod --bind "$GOCACHE" /go/cache \
  --bind "$NODE_HOME" /home/agent \
  --bind "$WORKTREE" /work --chdir /work \
  --setenv HOME /home/agent --setenv PATH "/usr/bin:$CLAUDE_BIN" \
  --setenv GOMODCACHE /go/mod --setenv GOCACHE /go/cache \
  --setenv GOFLAGS "-mod=readonly -buildvcs=false" \
  --setenv GOPROXY off --setenv GOTOOLCHAIN local \
  --setenv ANTHROPIC_API_KEY "$FACTORY_KEY" \
  --setenv FACTORY_RUN_ID "$RUN" \
  claude --bare -p …
```

`NODE_HOME` persists for each node, so `--resume` can find the session on a retry. Your home directory, SSH keys and other repos are not visible inside the sandbox.

### Limits the runtime enforces

- **Run budget:** the runtime adds up `total_cost_usd` from each result event. At the cap, it starts no new nodes.
- **Worker cap:** a semaphore limits parallel workers (start with 3) to stay under rate limits.
- **Wall-clock timeout** for each node: the runtime kills the whole process group.
- **Failure classes:** infrastructure failures (rate limit, crash, timeout with no progress) retry with backoff and do not count toward N. Only task failures count.
- **Resume:** node state comes from replaying the event log. After a crash, any node that was running resets its worktree to its base commit and runs again.
- **One event schema:** the event types are defined once in Go, and the TypeScript types are generated from them. The UI reconnects over SSE with `Last-Event-ID`, so a reconnect and a replay run the same code.

## Phases

Each phase ends with a standards review. `phasereview` asks the reviewer model, in a new session, to review the phase's code and prose against ponytail-review, the modern Go guidelines and the Orwell rules. The phase is done when the verdict is "Ship." and the report is committed in `docs/reviews/`.

0. **skeleton — Graph, event log, scheduler.** Graph types loaded from `graphs/*.yaml`, a validator, the scheduler, the SQLite event log, gates, and a FakeExecutor. A minimal UI: the graph, node states, a log tail, and approve buttons. Write the first 3 eval specs and their hidden tests.
   **Done when** a fake run with fan-out, a retry and a gate plays back in the UI. If you kill the runtime in the middle of a run, it resumes from the log. No LLM yet.
1. **loop — One sandboxed agent node.** ClaudeCodeExecutor in bubblewrap, in one worktree. The standards node and `guardrails.yaml`. The check is build, vet, `go fix -diff` and test against your tests, plus the frozen-file hashes. Add the retry policy, budgets and timeouts.
   **Done when** at least 8 of 10 runs pass on each of 3 eval tasks, under a median cost you set after the first runs. Also, a planted edit to a frozen test fails the check, and so does a planted `for i := 0; i < n; i++`.
2. **fixed graph — Hand-written factory pipeline.** standards → architect → contract gate → implement → check → verify → review → fix. One implement node. The red-before-green check runs at the contract gate. The review node uses ponytail-review with the Prose group.
   **Done when** 3 of 5 eval specs pass their hidden tests end to end.
3. **generated graph — Planner emits the task DAG.** A schema-checked DAG, the DAG gate with a cost estimate, write scopes, parallel sandboxed worktrees, and merges into the integration branch after each node.
   **Done when** a multi-package Go CLI builds from one paragraph, with 2 or more nodes running at the same time and no merge conflicts.
4. **replanning — Bounded graph rewrites.** After N failed retries and a failed fix, the planner rewrites the failing subgraph. Each rewrite is a new graph version, and the UI shows the difference between versions.
   **Done when** you plant planner mistakes (a wrong scope, a missing node), and the factory recovers from them within M replans.
5. **Claude Code — MCP server and plugin.** Plan, approve, status and logs as tools, with a `/factory` skill.
   **Done when** you can run the factory from inside Claude Code, including both gates.
6. **stretch — Your own loop, a second target.** An APIExecutor on the Anthropic Go SDK, Rust as a target language, or cached node results so you can rerun from one node.
   **Done when** you've learned what you wanted from it.

## Keep an eval set from phase 0 on

Five to ten specs in `evals/<name>/spec.md`, each with hidden acceptance tests and a reference answer. `evalgen` writes them with the `eval_author` model, which the factory never builds with, and keeps them only if they pass on the reference and fail on a program that does nothing. The factory never sees these tests. They are separate from the architect's tests, and they check whether the architect understood the spec.

- For every run, record: pass on hidden tests, cost, wall-clock time, retries, replans, the graph version, the standards version, and the review findings in each group with the `Lean: -N` line count.
- `factory eval --graph graphs/v2.yaml` runs the set and compares it with the last baseline, so you can tell whether a change to the graph helped.

## Known gaps

- bubblewrap cannot filter the network by host. Workers share the network for now. To fix this later, run workers in their own network namespace behind a proxy that allows only the API.
- Code that `go test` runs can read `ANTHROPIC_API_KEY`. Give the factory its own workspace key with a low spend limit.
- The architect's tests can be wrong too. The contract gate, the red-before-green check and the hidden eval tests are the three defenses.
- Only 25 of the Go guidelines are tool-checked. The other 29, the ponytail rules and the Orwell rules depend on the review agent's judgment. Watch the eval numbers to see whether the review catches them.
