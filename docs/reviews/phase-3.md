# Review: phase-3

Reviewer: claude-sonnet-5-5 at medium effort, in a new session. Range: d8d2875..fd64623. Date: 2026-11-07. Cost: $0.23.

What this change does: It adds a planner node. The planner reads the contract and writes a task graph as JSON. `internal/plan` checks that graph. The runtime then runs tasks that don't depend on each other at the same time, each in its own git worktree. Each task is limited to its own package directories. Merges run one at a time. The change also adds the `logq` eval and a parallelism measure for eval results.

I traced the main flows and found no defect that can happen at the load the README describes. That load is one person running a few tasks at once, at most 6.

- **Worktrees:** creating and merging them is serialized by a per-repo lock.
- **`Changed`:** it compares against the merge-base, so a node is not blamed for other nodes' merges.
- **Scope check:** it covers changed files and new files, and it rejects subfolders.
- **Plan validation:** it rejects plans with an unowned package, a package with two owners, or a scope outside the contract. Tests cover each case.
- **Merge conflicts:** the merge aborts and names the node. A test covers this.

**Should fix**

1. **Planner brief has a garbled rule** (`internal/executor/scope.go`, `plannerBrief`, the last "Do not use the ids…" bullet)
   - **What this is:** This text goes to the planner agent. It lists ids the planner must not use.
   - **Problem:** The phrase "any id that is a package directory name of a gate" has no clear meaning. The planner may ignore the rule or guess at it. A task named `plan` or `verify` clashes with an existing node, and `graph.Fill` rejects it later.
   - **Fix:** Cut the phrase. Write "Do not use the ids standards, architect, plan, review, fix, verify, tasks or an id that starts with approve-."
   - **If we skip it:** The planner sometimes picks a clashing id. The run then spends a retry on a plan that graph validation rejects.

2. **`TestScopeViolations` has a dead check** (`internal/executor/phase3_test.go`, the end of the loop)
   - **What this is:** After a refused run, the test checks that the work did not reach main.
   - **Problem:** The `!strings.Contains(tt.name, "stub")` condition is always true, because no case name contains "stub". The check is also nested in an `err == nil` branch, so it is hard to read.
   - **Fix:** Drop the `stub` condition and read the file directly.
   - **If we skip it:** The test still passes, but it gives less proof than it appears to.

Verdict: Ship.

Not checked: I did not run the tests. I did not read the `graph.Fill` rejection paths or `CommitAll` under concurrent use.
