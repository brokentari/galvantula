# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..09fb11e. Date: 2026-11-06. Cost: $0.54.

What this change does: It adds phase 1 of the factory. A Claude Code agent runs inside a bubblewrap sandbox in its own git worktree. The runtime checks the agent's work, commits it, and merges it into `main`. It also adds spending and time limits, a usage-limit stop, and `factory eval`, which runs evals N times and scores the products with hidden tests. I assumed one developer running this locally, with one eval run at a time by default. I did not run any code or tests.

I found no Must-fix problems. I checked these and found them sound:
- The git isolation in `workspace.git`.
- The runner stop and finish logic (`stopReason`, `finished`).
- The read-only mount for the check.
- The infra backoff.
- The generated TypeScript types, which are current (`ui/src/gen/events.ts` already has `limit` and `cost_usd`).

### Should fix

1. **`claude.Run` can hang after a cancel** (`internal/claude/claude.go`, `Run` and `read`)
   - **What this is:** `Run` now reads the output stream line by line to log each step. It calls `cmd.Wait()` only after the reading ends.
   - **Problem:** The old code used a buffer, so `cmd.WaitDelay = 10s` freed `Run` after a cancel. Now `read` waits for the pipe to close, and `WaitDelay` is not in effect until `Wait` runs. If a child process of `claude` outlives the kill and holds stdout open, `Run` blocks forever. The sandbox path is safe, because killing bwrap kills its whole process namespace. The unsandboxed callers (`cmd/evalgen`, `cmd/phasereview`) are not.
   - **Fix:** Make stdout an `io.Pipe` and run `cmd.Wait()` in a goroutine. The goroutine closes the pipe writer when `Wait` returns, and `Wait` then honors `WaitDelay`. This is about 5 lines.
   - **If we skip it:** A cancelled `evalgen` or `phasereview` run can hang with no error.

2. **Failed eval runs leave the pass rate** (`cmd/factory/eval.go`, `runEvals` and `evalOnce`)
   - **What this is:** Each eval run is recorded and counted in the pass-rate summary.
   - **Problem:** When `evalOnce` returns an error, `runEvals` prints it and drops the run. Examples are the new "logged nothing for N minutes" case and a failed `startRun`. The summary then divides by `len(rs)`. If 1 of 10 runs hangs and the other 9 pass, it prints `9/9`. Commit 0a0be7a says "the pass rate counts every run", and this path breaks that.
   - **Fix:** On error, record a result with `Pass: false`, `Status: "error"` and `Reason: err.Error()`. Keep the run id when you have one. This costs a few lines.
   - **If we skip it:** The "8 of 10 runs pass" gate for phase 1 can look met when a run was lost.

3. **The credential is on the bwrap command line** (`internal/sandbox/sandbox.go`, `Command`; `internal/executor/claude.go`, `agent`)
   - **What this is:** The sandbox passes each environment variable as `--setenv KEY VALUE` on the `bwrap` command line. The agent's `Auth` value (`CLAUDE_CODE_OAUTH_TOKEN=...`) goes this way.
   - **Problem:** Any user on the host can read the token with `ps` or `/proc/<pid>/cmdline`. This does not matter on a single-user laptop. It does matter on a shared dev box or CI host.
   - **Fix:** Pass the arguments with bwrap's `--args FD` option, reading them from a pipe. Or document that the factory must run on a single-user host.
   - **If we skip it:** The token leaks on a shared host. A leaked token buys usage on the plan.

4. **The timeout and usage-limit branches in `agent` have no test** (`internal/executor/claude.go`, `agent`; `claude_test.go`)
   - **What this is:** An attempt past `NodeMinutes` is charged the full `NodeUSD`. A usage-limit error becomes `LimitError`, and a missing result becomes `InfraError`.
   - **Problem:** These branches decide whether a run retries and how much budget it burns. Only the runner side of `LimitError` has a test, with a fake executor. The claude tests cover `ErrUsageLimit` itself. The mapping inside `agent` is untested. A swapped case would pass every test.
   - **Fix:** Add a fake-claude test that prints a usage-limit result, and assert `errors.As` to `*LimitError`. For the timeout, make the limit a `time.Duration` in `Claude`, so a test can use 100ms instead of 1 minute.
   - **If we skip it:** A wrong class means a retry that buys more usage, or a run that stops early.

### Prose

5. **Two wording errors about the budget** (`guardrails.yaml`, `README.md`)
   - **What this is:** The `budget` comment says "past this, the runtime starts no new node" for `run_usd`. The README says "`guardrails.yaml` sets each attempt's spending and time limits".
   - **Problem:** The code stops at `st.CostUSD >= budget`, which means "at this", not "past this". `run_usd` limits the whole run, not an attempt.
   - **Fix:** Write "at this, the runtime starts no new node". Write "sets the spending and time limits for each attempt and for the whole run".
   - **If we skip it:** A reader sets the budget one node too high, or believes `run_usd` caps one attempt.

Verdict: fix 1 and 2 first. Items 3 to 5 can follow.

Not checked: I did not run the tests or the sandbox, so bwrap behavior is read from code only. The agent and the check share one writable Go build cache (`.cache/go-build`). A hostile agent could plant cache entries, so the isolation is only as strong as that cache. The eval results were recorded before the third review fixes, so they do not show the final code.
