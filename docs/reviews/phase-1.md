# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..32512db. Date: 2026-11-06. Cost: $0.58.

What this change does: The server now finds its own git commit once at startup, and each `run_created` event records it. `factory eval` copies that commit from the run into each result line, so a result names the code that ran it. Results files do not make the commit `-dirty`. The workspace also turns off commit signing, so a host that signs commits cannot break the runtime's own commits. Earlier commits in this range add the sandboxed Claude executor, the eval command, and the review fixes. I read those as well.

Load I assumed: one person on one machine, running `factory eval` with `-parallel 1`, as the README says.

I found nothing that must be fixed. I checked these and they hold up:
- The server's Host and Content-Type checks, which stop a web page from starting paid runs.
- The credential, which travels in the sandbox environment and not on the command line.
- The retry, resume and backoff logic in the runner.
- The generated `ui/src/gen/events.ts`, which is in sync.

**Must fix:** none.

**Should fix:**

1. **`factoryCommit` has no test** (`cmd/factory/main.go`, `factoryCommit`)
   - **What this is:** At startup, the server runs `git rev-parse` and `git status --porcelain -- . :!evals/results`. It builds the commit string from them, adding `-dirty` when the tree has changes.
   - **Problem:** Three branches decide what every eval result says: git fails, the tree is dirty, and a change under `evals/results` is ignored. Nothing tests any of them. If the `:!evals/results` pathspec (git's "exclude this path" syntax) is wrong, every batch is marked `-dirty` and results from different versions look the same.
   - **Fix:** Add one test. It runs `git init` in a temp folder and uses `t.Chdir` to move there. Then:
     - Commit one file and expect a bare hash.
     - Add `evals/results/x.jsonl` and expect no `-dirty`.
     - Add any other file and expect `-dirty`.
   - **If we skip it:** A broken pathspec or a git error produces a wrong label in every result, and nobody notices until two batches get compared.

2. **No test shows the runner records the commit** (`internal/runner/runner.go`, `Start`; `internal/runner/runner_test.go`)
   - **What this is:** `Start` puts `r.Commit` into `RunCreatedData.Commit`, and `Fold` copies it into `State.Commit`.
   - **Problem:** The new eval test fakes the server and writes the event itself, so it never runs `Runner.Start`. If `Start` dropped the commit, every test would still pass and real results would have an empty commit.
   - **Fix:** In `TestFactoryDemoRun`, set `r.Commit = "abc1234"` before `Start`. After the run, assert `st.Commit == "abc1234"`. This is two lines.
   - **If we skip it:** A later edit to `Start` can silently drop the field, and the eval's whole purpose, naming the code that ran, fails with no test red.

3. **Prose: wrong comment on the `Commit` field** (`cmd/factory/eval.go`, `evalResult.Commit`)
   - **What this is:** A doc comment on the result field.
   - **Problem:** It says "the server's commit when the run started". The code takes the commit once, when the server starts. If someone commits and then starts a run on the same server, the field still holds the older commit. The comment is wrong in exactly that case.
   - **Fix:** Write `// the commit the server started on, with -dirty for uncommitted changes`.
   - **If we skip it:** A reader trusts the field to name the code at run time.

**Nice to have:** none.

Verdict: fix 1, 2 and 3 first. They are small, and none of them blocks a run.

Lean: no lean findings.

Not checked: I did not run the tests or `go fix -diff`. I also could not confirm that `uuid` is a standard-library import in the installed Go 1.27.
