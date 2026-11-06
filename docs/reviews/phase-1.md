# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..b86950c. Date: 2026-11-06. Cost: $0.58.

**What this change does:** It adds phase 1 of the factory. One Claude Code agent runs inside a bubblewrap sandbox (a Linux tool that hides the host's files). It works in its own git worktree, and a check decides whether its work merges into the product repo. The change also adds spending and time limits, retry and backoff rules, and a usage-limit stop. It adds `factory eval`, which runs each eval spec N times, scores the products with hidden tests, and records the results. The UI now shows cost.

I assumed the load in the README: one person, one run at a time, and one agent node per run in `graphs/phase1.yaml`.

### Should fix

1. **The check can pass on files that never reach the product** (`internal/executor/claude.go:L175-186`, `internal/workspace/workspace.go:L28-37`)
   - **What this is:** After the agent finishes, the check runs gofmt, vet, build and tests on the files on disk. Then `CommitAll` runs `git add -A` and merges the commit into main.
   - **Problem:** `git add -A` skips files that a `.gitignore` matches. The agent may write a `.gitignore`. A common line is the binary's name, such as `wordfreq`, and that also ignores the folder `cmd/wordfreq/`. The check passes, because the files are on disk. The commit leaves them out. The run ends "succeeded" with a product that has no program. The hidden tests would catch it in an eval. A real run would not.
   - **Fix:** After the build step in `check`, fail if git ignores any `.go` file. For example, list `git ls-files --others --ignored --exclude-standard -z` and fail on any name ending in `.go`. Add one test: the agent writes `main.go` and `.gitignore` containing `main.go`, and the check must fail.
   - **If we skip it:** Some runs report success with an empty or partial product. Nothing tells the user.

2. **The phase's "Done when" is not fully met, and the results come from older code** (`Software Factory Plan.html:L797`, `README.md` status, `evals/results/2026-11-06.jsonl`)
   - **What this is:** The plan says the phase is done when at least 8 of 10 runs pass per eval, "under a median cost you set after the first runs". The README says the first batch passed 31 of 31 runs.
   - **Problem:** No file sets a median-cost limit, so that part of the criterion cannot be checked. The batch was committed in `4f02b54`, before the later fixes: the check mounts the worktree read-only, hidden tests get a smaller sandbox mount, and binaries are removed before the check. Its pass rate describes the old code, not the code you ship.
   - **Fix:** Pick a median cost, for example in `guardrails.yaml` or the README (the batch's medians are about $0.10 to $0.17). Run a short batch on the final code, such as `factory eval -n 3 ...`. Record it and name the commit it ran on.
   - **If we skip it:** The README's "31 of 31" claim and the "Done when" line rest on code that is no longer the code in the repo.

### Nice to have

3. **A test sleeps 5 seconds for nothing** (`cmd/factory/eval.go:L141`, `cmd/factory/eval_test.go` `TestEvalStopsAtUsageLimit`)
   - **What this is:** `evalOnce` waits 5 seconds before it reads the log for the first time.
   - **Problem:** The test's fake server has already finished the run, so each test pays the full 5 seconds.
   - **Fix:** Make the poll interval a package variable (`var pollEvery = 5 * time.Second`). The test sets it to 10 ms.
   - **If we skip it:** The suite is 5 seconds slower. Nothing else breaks.

Verdict: fix 1 and 2 first.

Not checked: I did not run any tests or bwrap. The sandbox, git and `go fix` behavior come from reading the code and the tests. I did not read `docs/reviews/phase-1.md`.
