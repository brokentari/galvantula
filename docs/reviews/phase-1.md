# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..4b80b63. Date: 2026-11-06. Cost: $0.54.

**What this change does:** It finishes phase 1. A Claude Code agent runs inside a bubblewrap sandbox (a Linux tool that hides most of the host from a command), and its work is checked and merged by the runtime. `factory eval` runs the evals through the server and scores each product with hidden tests. The last two commits add a $0.50 median cost limit and make `eval` write the factory commit on each result line. I assumed one developer on one machine, a few runs at a time, as the README says.

### Should fix

1. **The recorded commit is the eval's tree, not the server's** (`cmd/factory/eval.go:L118`, `L124-138`)
   - **What this is:** `factoryCommit()` runs `git rev-parse` and `git status` where `factory eval` runs. It writes the result into every line. The commit message says this keeps "batches from two versions never mix".
   - **Problem:** The agent code does not run in the eval process. It runs in `factory serve`, which started earlier and may be on older code. You start the server, edit the executor, then run `factory eval`. Every line says the new commit, but the runs used the old code. The field gives false proof of the exact thing it exists to prove. Nothing tests it either: `TestLostRunsCountAsFailures` only checks that `Commit` is not empty.
   - **Fix:** Compute the commit once in `serve` and put it in `RunCreatedData`, next to `Guardrails`. `evalOnce` then copies it from the run state, as it already does for `Implementer`. Regenerate `events.ts`. Add a test that the result's commit is the one in the run state.
   - **If we skip it:** The after-fix batch that phase 1 needs can be recorded as one commit and run on another. You would decide that phase 1 is done from data that cannot be trusted.

### Nice to have

2. **A second batch on the same day is labelled "-dirty"** (`cmd/factory/eval.go:L138`)
   - **What this is:** `git status --porcelain` lists untracked files. It reports a change if anything differs from `HEAD`.
   - **Problem:** The first batch writes `evals/results/<date>.jsonl`, which is untracked until you commit it. The next `factory eval` then sees a change and adds `-dirty` to every line. The label then says nothing about the code.
   - **Fix:** Add a pathspec to the status call: `git status --porcelain -- . ":!evals/results"`.
   - **If we skip it:** Results from clean code are labelled `-dirty` until you commit the previous results.

3. **The host's git config can break the runtime's commits** (`internal/workspace/workspace.go:L97-101`)
   - **What this is:** Every `git` call goes through `Repo.git`. It sets `user.name`, `user.email`, `hooksPath` and `fsmonitor`, but it still reads the user's global config.
   - **Problem:** If the user has `commit.gpgsign=true`, every `commit` can fail or wait for a passphrase prompt. The runner treats that as an infra failure, retries it, and ends the run after 5.
   - **Fix:** Add `"-c", "commit.gpgsign=false"` to the same `-c` list. This is one line.
   - **If we skip it:** Factory runs fail on machines that sign commits, and the error looks like an agent failure.

**Verdict:** fix 1 first. The rest is optional.
**Lean:** nothing worth cutting.
**Not checked:** I could not run commands. I did not run the tests or `go fix -diff`, and I did not confirm that the `uuid` import builds on Go 1.27. I found no prose problems.
