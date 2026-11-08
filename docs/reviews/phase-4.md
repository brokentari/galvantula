# Review: phase-4

Reviewer: claude-sonnet-5-5 at medium effort, in a new session. Range: 31f5573..3cb8a98. Date: 2026-11-07. Cost: $0.19.

What this change does: Phase 4 adds replanning. When a task fails its last retry with a task-class error, the runtime removes it and its dependents, inserts a `replan-N` plan node and a slot, and asks the planner to plan those packages again. This is capped by `budget.replans`. Each graph change gets a version number. Evals can plant a flawed first plan with `-planted`, and the UI has a version menu that shows what each version added, changed and removed.

I found no Must fix or Should fix items. I read the diff and traced these paths:

- **Graph edit:** `Graph.Replan` and `Fill` produce the right dependencies for dependents and for new tasks that depend on kept tasks.
- **Failure cases:**
  - Infra and limit failures do not trigger a replan.
  - A replan stops while an earlier replan slot is open.
  - It stops once the run budget or the replan budget is spent.
  - A kept task that fails while a replan waits gets its own replan.
- **Restart:** `drive` runs `replan` on its first loop, so a crash between a task's last failure and the replan loses nothing.
- **Event fold:** `Apply` removes replaced nodes and records them in `Retired`, and `fillSlot` rejects retired ids. This stops a new node from reusing an old worktree, branch or session.
- **Planted plans:** the server validates a planted plan with `plan.Parse`, and `planReport` validates it again. It is used only for the first plan node and not for replans. Invalid input fails with a clear error before any run starts.
- **UI:** the diff and ghost nodes handle a missing version and a missing node state.
- **Tests:** replan, retired ids, bounds, limit failures, restart, kept-task failure, planted plans and the event fold all have tests.

Verdict: Ship.

Not checked: I could not run the tests. I did not read `internal/plan/plan.go` in full, so I assumed `plan.Validate` accepts a dependency on a kept task, as `TestReplanPlansOnlyTheFreePackages` implies.
