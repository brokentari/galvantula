# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..33d7ef6. Date: 2026-11-06. Cost: $0.71.

I checked the server's request guards, the graph ID rules, the generated TypeScript types and the SQLite settings. All of them hold. Findings below.

**What this change does:** Phase 1 adds one real agent node. Claude Code runs inside a bubblewrap sandbox, in its own git worktree. The runtime checks its work, then commits and merges it. The change also adds spend and time limits, a usage-limit stop and `factory eval`. `factory eval` runs the specs N times and scores each product with hidden tests. The last four commits fix earlier review findings. I assumed one person running a local server, with eval runs one at a time.

### Must fix
None.

### Should fix

1. **A restart without the credential kills in-flight runs for good** (`internal/runner/runner.go:97-101`, `:152-164`, `:223-227`)
   - **What this is:** After a restart, `ResumeAll` drives every unfinished run. `drive` asks `fromState` for the executor. For a `claude` run, `fromState` fails if `r.Auth` is empty.
   - **Problem:** When `fromState` fails, `drive` writes a `run_finished` event with status failed. Suppose you run `factory serve` again in a new shell and forget to export the token. The server prints a warning, then marks every half-done Claude run as failed. A failed run cannot be resumed.
   - **Fix:** In `ResumeAll`, skip runs whose executor is `claude` while `r.Auth == ""`. Log a warning and leave them running. They resume at the next start that has a token. Add a small test.
   - **If we skip it:** One forgotten `export` loses a run that has already spent money.

2. **File names with leading white space break the new `-z` handling** (`internal/workspace/workspace.go:98-106`, `:84-87`)
   - **What this is:** The `-z` option makes git print raw file names, separated by NUL bytes. The commit says this fixes odd names.
   - **Problem:** `git()` ends with `strings.TrimSpace` on the whole output. That cuts leading white space from the first name. Suppose the agent creates a file named `" notes.txt"` and it sorts first. `NewFiles` returns `"notes.txt"`. Then `removeBinaries` calls `Lstat` on a path that does not exist, and the node fails as an infra error. It retries five times, and the run fails. `TestNodeWorkflow` does not cover this case.
   - **Fix:** Do not trim output that comes from `-z`. For example, make `git()` return the raw output and trim in the callers that need it. Add a name with a leading space to the test.
   - **If we skip it:** A rare file name ends a run with a confusing error.

3. **No test for how eval counts lost runs** (`cmd/factory/eval.go:111-125`, `evalOnce`)
   - **What this is:** The fourth commit says a run the eval loses is recorded as a failed run with status `error`. The pass rate is the factory's main score.
   - **Problem:** The only new eval test is `TestHiddenTestsScoreAProduct`. Nothing tests the error branch, the usage-limit stop or the quiet-run cutoff. A later edit could drop failed runs from the count, and every test would still pass. The pass rate would then read too high.
   - **Fix:** Add one test. Start a fake-executor run through `runner` and a test server, and make `evalOnce` fail. Check that the recorded line has `Pass=false` and `Status="error"`. The loop body that builds the failed record may need to move into a small function so the test can call it. Keep it small.
   - **If we skip it:** The pass rate can quietly overstate success.

### Nice to have

4. **The "first retry resumes" rule counts infra attempts** (`internal/runner/runner.go:~265`)
   - **What this is:** `task.Resume = task.Attempt == 2 && ns.LastClass == event.Task`.
   - **Problem:** Suppose attempt 1 hits an infra crash and attempt 2 fails the check. Attempt 3 is the first retry after a task failure. It starts from a clean worktree and a new session, although the session is still good. That costs the full price of a fresh run.
   - **Fix:** Resume when `ns.TaskFailures == 1 && ns.LastClass == event.Task`.
   - **If we skip it:** Some retries cost more than they need to.

5. **The 31 passing runs predate the last three fix commits** (`evals/results/2026-11-06.jsonl`, README status)
   - **What this is:** The README says the first batch passed in all 31 runs.
   - **Problem:** Later commits changed what those runs go through. The check mounts the worktree read-only, build output is removed, and the credential moves into the process environment. Tests cover these with a fake `claude`, not the real one.
   - **Fix:** Run one small real batch, for example `-n 3`, before you call the phase done.
   - **If we skip it:** A fix could break the real agent, and only fake tests would show it.

### Prose

6. **Unclear comment on the quiet-run limit** (`cmd/factory/eval.go`, in `evalOnce`)
   - **What this is:** The comment says: "An attempt logs at least once per node time limit, and its check takes minutes, not more."
   - **Problem:** It does not say why the margin is 10 minutes, and "takes minutes, not more" is vague. A reader cannot tell what to change if the check gets slower.
   - **Fix:** For example: `// The run is lost if it logs nothing for the node time limit plus 10 minutes. The 10 minutes cover the check.`
   - **If we skip it:** A later edit may change the margin without knowing what it protects.

Verdict: fix 1 and 2 first. Add 3 before you call the phase done. Items 4 to 6 can wait.

Not checked: I could not run anything. The tests, `go fix -diff` and `bwrap` behavior on real Linux are untested by me. I did not check the `evalgen` `tail` against the new `tail` in `internal/executor/claude.go` for duplication.
