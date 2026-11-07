# Review: phase-2

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 0fc5ce8..a0c0356. Date: 2026-11-07. Cost: $0.34.

What this change does: It adds the phase-2 pipeline. The architect writes stubs, tests and `CONTRACT.md`. A gate approves the contract, and then an implementer, a read-only reviewer, a fixer and a `-race` verify node run in turn. Advisory nodes are abandoned when they fail for good, and runs can auto-approve gates. The check now fails on any symbolic link in an agent's worktree. The `phasereview` instructions and `AGENTS.md` now ask for a lenient review.

Looks good. Ship.

I assumed the README's load: one person running one run at a time, plus an eval batch of five runs.

- **Symbolic links:** `noLinks` walks the whole worktree and skips only `.git`. It runs before `noSkips`, so nothing reads a link target. `removeBinaries` already skips links. The runtime writes `.factory/review.md` only on main, and a link can't reach main because the check refuses it first.
- **Test counting:** `gotest.Count` handles `go test -json` output, including non-JSON build errors and skipped tests. `testsPass` keeps the exit status. `red` is right to ignore the exit status, because it counts failures instead.
- **Advisory and limit failures:** A usage-limit failure returns early in `runner.fail`, so it is never abandoned and it still fails the run, as intended. An abandoned review leaves no report, so the fix node skips itself.
- **Auto-approve:** A gate decided in the loop triggers `continue`, so newly ready nodes start. The crash-resume path decides only gates that are still waiting. Both paths are tested.
- **Frozen files:** The implementer and fixer can edit stubs and add tests, but they can't change or delete existing tests, `testdata`, `CONTRACT.md` or the standards files. Tests cover this.
- **Prose:** The README and plan text match the code. The recorded result of 4 of 5 evals, with `calc` failing on a wrong architect test, matches the results file.

Verdict: Ship.

Not checked: I did not run any tests or the evals. I did not read `internal/sandbox` or `internal/claude`, so I assumed the sandbox enforces its read-only mounts as the code intends.
