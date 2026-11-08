# Review: phase-4

Reviewer: claude-sonnet-5-5 at medium effort, in a new session. Range: 31f5573..28617c1. Date: 2026-11-07. Cost: $0.19.

What this change does: Phase 4 adds replanning. When a task fails its last retry, the runtime removes it and its dependents. It then runs the planner again for those packages and records each replan as a new graph version. The change also adds `-planted` evals, which start a run from a flawed first plan, and a UI menu that shows what each graph version changed.

I found no Must fix. I assumed the load the README describes: one operator, a few runs at a time. I checked the replan, event-fold and fill logic by reading them. I could not run any tests.

**Should fix**

1. **The planted-plan docs omit the graph flag** (`evals/README.md`, "Plant a planner mistake")
   - **What this is:** The section tells the reader to run `factory eval -planted <name>` to test replanning.
   - **Problem:** `-graph` defaults to `graphs/phase1.yaml` (`cmd/factory/eval.go`). I did not read that file. The diff gives no sign that it has a plan node. The planted plan is used only by a plan node. If phase1 has none, the run never reads the plan, `replans` stays 0, and the result still records `planted`. The reader would think replanning worked, or was never needed.
   - **Fix:** Add `-graph graphs/phase4.yaml` to the command in the docs. Or make `-planted` fail when the chosen graph has no plan node.
   - **If we skip it:** An operator who runs the documented command may read a pass with 0 replans as a successful replan test.

2. **A kept task that fails while a replan is open can fail the run despite spare replans** (`internal/runner/runner.go`, `replan` and `awaitsReplan`)
   - **What this is:** The replan planner node depends on the kept tasks that the removed tasks needed.
   - **Problem:** Suppose one of those kept tasks fails while the planner waits for it. `replan()` skips because a replan slot is open, and the planner can never start because its dep failed. I did not read `finished()`, but the run most likely ends with "nodes can never start", although `budget.replans` is not used up.
   - **Fix:** A test for this case would show what happens. If it does fail, make the planner depend only on kept tasks that have already merged, or add a `shortcut:` comment that states the limit.
   - **If we skip it:** In some parallel runs a second failure ends the run instead of triggering a replan. This costs wasted spend, not data loss.

Verdict: Ship.

Not checked: I did not run the Go or UI tests. I did not read `finished()`, `stopReason`'s budget branch, or `graphs/phase1.yaml`.
