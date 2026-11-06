# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..41cdb0a. Date: 2026-11-06. Cost: $0.62.

What this change does: Phase 1 adds one real agent node to the factory. The agent is Claude Code running in a bubblewrap sandbox (Linux tool that hides the host from a process) inside a git worktree. The runtime checks the agent's work with gofmt, vet, `go fix`, build and the agent's tests, then commits and merges it. The change also adds per-node and per-run cost limits, retry and resume rules, a usage-limit stop, and `factory eval`, which runs evals N times and scores each product with hidden tests. The last commit adds a usage-limit test for the eval and a repo-root guard. It also cuts log lines by rune and fixes some prose.

I assumed the expected load is one developer on a local machine. That means one run at a time, at most a few parallel runs in `factory eval`, and one agent node per run.

I checked these points:
- **Server guards:** the server checks the Host header, and its tests cover content types.
- **Node IDs:** IDs match `^[a-z0-9][a-z0-9-]*$`, so they cannot be path tricks.
- **UI types:** `ui/src/gen/events.ts` already has `cost_usd`, `limit` and `NodeSucceededData`, and a test guards it.
- **Budget and retry flow:** the budget stop, the resume and backoff rules, and `Decode` on old events with no data all hold up.

## Findings

### Should fix

1. **Two fixes from the last commit have no test** (`internal/claude/claude.go:207-212`, `cmd/factory/eval.go:60-65`)
   - **What this is:** `short` cuts each log line to 200 characters. The eval now stops with "run factory eval from the repo root" when `go.mod` or `evals/evalkit` is missing.
   - **Problem:** `TestRunLogsSteps` uses only short ASCII text, so the rune cut is never exercised. Every eval test moves into a temp repo that has these files, so the guard's error path never runs. A revert of either fix would pass all tests.
   - **Fix:** In `TestRunLogsSteps`, add a text of 201 runes, such as `strings.Repeat("é", 201)`. Assert that the logged line is valid UTF-8 and ends in `…`. In `eval_test.go`, add a case that calls `runEvals` from `t.TempDir()` and expects an error containing "repo root".
   - **If we skip it:** Both bugs can come back unseen. The guard matters most, because without it every run is paid for and none can be scored.

2. **README lists Go as the only host tool, but the server also needs `gofmt`** (`README.md`, "Run a real agent"; `cmd/factory/main.go:157-165`)
   - **What this is:** The README says what agents need on the host. `sandboxReady` refuses to start unless `/usr/bin/go` and `/usr/bin/gofmt` both exist.
   - **Problem:** The README names only "Go at `/usr/bin/go`". A reader who installs Go elsewhere, or only the Go binary, gets an error the README did not predict.
   - **Fix:** Write "Go at `/usr/bin/go` and `/usr/bin/gofmt`".
   - **If we skip it:** A new user wastes time on the startup error.

### Nice to have

3. **A crashed agent's cost is not counted** (`internal/executor/claude.go:~175-185`)
   - **What this is:** When an attempt times out, the code charges the full node budget because no result line reports its cost. An attempt that ends with `ErrNoResult` (claude killed or crashed after it spent money) charges `$0`.
   - **Problem:** Claude Code is killed by the OOM killer after spending $2, and no result line appears. It counts as an infra failure with cost 0. The node can do this up to 5 times (`maxInfraFailures`). At the default limits, up to about $15 can be spent without counting toward `run_usd`.
   - **Fix:** In the `ErrNoResult` case, set `spent.CostUSD = c.Rails.Budget.NodeUSD`, as the timeout case does. That is one line, and it needs a one-line comment. Or accept the gap and extend the existing `shortcut:` comment to say so.
   - **If we skip it:** The run budget can be exceeded after repeated crashes. The bound is small and finite.

4. **`tail` is copied in two packages** (`internal/executor/claude.go:321-324`, `cmd/evalgen/main.go:400-403`)
   - **What this is:** Both functions keep the last N lines of a text.
   - **Problem:** The bodies are identical, so a fix to one leaves the other broken.
   - **Fix:** Put one `Tail` next to `gotest.Count` or in a small shared package. Both callers already import such packages. This removes about 4 lines.
   - **If we skip it:** The two copies may drift apart.

Verdict: Ship. Fix 1 and 2 first if you can, because they are small. Nothing here changes behavior.

Lean: -4 lines possible.

Not checked: I did not run the code or the tests. I did not run `go fix -diff`, and I did not run a real `bwrap` or `claude`. The parallel-agent git races (concurrent `worktree add` and `merge` in one repo) do not apply to the current one-agent graph, but phase 3 will need them handled.
