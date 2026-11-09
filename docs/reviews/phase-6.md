# Review: phase-6

Reviewer: claude-sonnet-5-5 at medium effort, in a new session. Range: 66f4939..713ab5e. Date: 2026-11-09. Cost: $0.21.

What this change does: It adds a trial build to the architect step. A second agent implements the architect's stubs in a throwaway copy, and the architect's attempt fails if its tests still fail on that code. It lets a task scope hold `.` (the repo root), so root files such as README.md get an owner. It also adds a Docker wrapper for hosts without bwrap, `FACTORY_GIT_NAME` and `FACTORY_GIT_EMAIL` for run commits, a backdate script, and the rotom eval (hidden tests, reference server, assumptions).

No Must fix or Should fix findings.

I read these parts and found no defect:
- **Oracle flow:** copy, `.git` removal, agent run, read-only test run, and cost accounting on failure. Infra errors are kept apart from architect errors.
- **Root scope:** the `.` handling in `plan.Validate` and `testTargets`, plus its tests.
- **Reference server:** the integer overflow checks, including `DECRBY` with the smallest int64, and the TTL rounding. They match what the hidden tests expect. Shutdown and the early-close cases also match.
- **Scripts:** the quoting in `backdate.sh`, and the Docker flags and port binding.

**Nice to have**

1. **`docker-factory.sh` exits silently if git has no identity** (`scripts/docker-factory.sh:L11-12`)
   - **What this is:** The script reads `git config user.name` and `user.email` under `set -eu`.
   - **Problem:** If either value is unset, `git config` exits 1. The script then stops with no message.
   - **Fix:** Append `|| true` to both lines. The `cmp.Or` fallback in `workspace.go` then supplies the defaults.
   - **If we skip it:** A user without a git identity sees the script stop and does not know why. Setting `git config user.name` and `user.email` fixes it.

Verdict: Ship.

Not checked: I did not run the tests, and I did not check the `uuid` import path or how `os.CopyFS` handles symlinks in an agent's worktree.
