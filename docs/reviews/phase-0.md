# Review: phase-0

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: start..7a87216. Date: 2026-11-06. Cost: $0.77.

What this change does: this is the whole phase-0 skeleton, plus five rounds of review fixes. It has a Go runtime with a SQLite event log, a fake executor, an HTTP/SSE server and a React UI. It also has the eval generator (`evalgen`), the phase reviewer (`phasereview`) and the vendored standards. I assumed one developer on localhost, with no real load. I read the diff only and did not run anything.

## Must fix

None. I traced the scheduler, resume and retry logic, the plan-node event, the gate flow and the SSE reconnect, and found no bug. I also checked the Host and Content-Type guards, the `evalgen` install and restore, and the `countTests` and `verdict` parsers. All of them hold.

## Should fix

1. **The `%w` fix in `claude.Run` does not let callers tell a cancel from a crash** (`internal/claude/claude.go`, the error paths after `cmd.Run()`)
   - **What this is:** The last commit says the error is now wrapped "so callers can tell a cancel from a crash".
   - **Problem:** `exec.CommandContext` kills the child on cancel. `Wait` then returns `signal: killed`, not `context.Canceled`. Go swaps in the context error only when the process exited cleanly. So `errors.Is(err, context.Canceled)` is false on a real cancel, and no test covers it. Today both callers pass `context.Background()`, so nothing breaks yet.
   - **Fix:** Check the context before parsing the output: `if ctx.Err() != nil { return r, fmt.Errorf("claude -p: %w", context.Cause(ctx)) }`. Add one test with a fake `claude` script on `PATH` and a cancelled context.
   - **If we skip it:** The next caller with a real cancel (the phase 1 executor) will treat a cancel as a crash and retry it.

2. **The "bind the port before resuming runs" guard has no test** (`cmd/factory/main.go`, `serve`)
   - **What this is:** A second `factory serve` must stop before it resumes runs that the first server is driving. Otherwise two processes append events to the same runs.
   - **Problem:** The guard depends only on the order of two statements, and nothing fails if someone swaps them. Running two drivers on one run causes double-started nodes and a corrupt log.
   - **Fix:** `serve(args []string) error` is already callable. Seed a temp DB with an unfinished run and hold a listener on a port. Call `serve([]string{"-addr", ln.Addr().String(), "-db", db, "-ui", ""})`. Assert it returns an error and that the DB has no new events.
   - **If we skip it:** A tidy-up refactor can silently bring the double-driver bug back.

## Nice to have

3. **Two copies of the plan** (`Software Factory Plan.md`, `Software Factory Plan.html`)
   - **What this is:** The `.md` file says it has "the same text" as the HTML. That is 263 lines of prose to keep in step with 868 lines of HTML.
   - **Problem:** The two files must change together, and they have already drifted. One says "Retry edges have caps" and the other says "Dashed edges have caps".
   - **Fix:** Keep only the HTML, which has the diagrams. Point the README link at it.
   - **If we skip it:** Each plan edit needs two changes, and the files will disagree over time.

## Prose

4. **The same thing has two names, and "infra" is jargon**
   - **What this is:** The last commit says "task graph" in the graph titles and explains "DAG" at first use.
   - **Problem:** `graphs/factory.yaml` still titles the slot "Generated task DAG". Both plan files use "DAG" and "task graph" for the same thing: "approve DAG + cost", "emit DAG", "back to DAG approval", "A DAG is accepted only if". The UI shows "infra" as a bare word (`ui/src/NodePanel.tsx`, `· N infra`; `ui/src/state.ts`, `infra failure`). The plan says "infrastructure failures". The Orwell and STE rules ask for one term per thing and everyday words.
   - **Fix:** Pick "task graph". Change the slot title, the figure labels and the plan text to it. Keep "DAG" only in the one explaining sentence. Write "infrastructure" in the UI text, or "not the node's fault".
   - **If we skip it:** Readers must guess that "DAG" and "task graph" are the same thing.

Verdict: fix 1, 2 and 4 first.
Lean: -263 lines possible.
Not checked: I did not run the tests or the `go fix` check. `evalgen` runs the model's code with your full environment and no sandbox, so keep `ANTHROPIC_API_KEY` in mind when you run it.
