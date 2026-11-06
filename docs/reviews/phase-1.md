# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..0a0be7a. Date: 2026-11-06. Cost: $0.44.

What this change does: Phase 1 adds one Claude Code agent node that runs inside a bubblewrap sandbox (a Linux tool that walls a process off from the host). The agent works in its own git worktree. The runtime checks its work (gofmt, vet, `go fix -diff`, build, tests, frozen files), then commits and merges it. It also adds spend and time limits, a stop on usage limits, retry and backoff rules, and `factory eval`, which scores each run with hidden tests. The last commit removes compiled programs before the check, counts a product that does not build as a failed run, narrows the hidden-test sandbox, and charges timed-out attempts.

I assumed the load is one person running one server and `factory eval -parallel 1`. I could not run anything. Reading `go.mod` and the standards lock, I found no sign that the `uuid` import is wrong.

## Must fix

None.

## Should fix

1. **A run that finishes all its work can be marked failed on budget** (`internal/runner/runner.go:384-396`, `:411-414`)
   - **What this is:** `stopReason` returns a budget message once the run's cost reaches `run_usd`. `finished` returns "failed" whenever that message is not empty, before it checks whether every node succeeded.
   - **Problem:** Say the last node succeeds and pushes the total to or over the budget. The run is then reported as failed, although all work is merged. `phase1.yaml` has one agent, so `run_usd: 12` will not trigger this today. A multi-node graph will, as will a lower budget. `TestRunBudgetStopsNewNodes` only covers the case where a node is left unstarted.
   - **Fix:** In `finished`, only treat the budget as a stop when a node is still pending. Do this by moving the `stuck` loop first and returning the stop message only if `stuck` is not empty. Add a test where the last node pushes the cost past the budget.
   - **If we skip it:** Later phases show good runs as failed. Eval pass rates can drop for the wrong reason.

2. **A file with a non-ASCII name makes every attempt fail as an infra failure** (`internal/workspace/workspace.go:76-82`, used in `internal/executor/claude.go` `removeBinaries`)
   - **What this is:** `NewFiles` lists untracked files with `git ls-files --others` and splits the output on newlines. `removeBinaries` then calls `os.Lstat` on each name.
   - **Problem:** By default git writes names with non-ASCII bytes, or with quotes or newlines, as a quoted string such as `"testdata/caf\303\251.txt"`. `Lstat` on that string fails, and `removeBinaries` returns an `InfraError`. An agent that adds a Unicode fixture (plausible in `wordfreq`) fails the node. Infra failures retry up to five times with backoff, and each retry costs money. `Changed` has the same quoting problem, but it only affects the message.
   - **Fix:** Use `git -c core.quotepath=off` in `git()`. This fixes Unicode names, and quotes and newlines remain (names with those are rare). For full safety, use `-z` and split on NUL.
   - **If we skip it:** One odd file name burns up to five attempts and the run fails.

3. **The README says 30 of 30 runs, but the results file has 31** (`README.md` status, `evals/results/2026-11-06.jsonl`, and the commit message of 4f02b54)
   - **What this is:** The status line states how many runs passed the hidden tests.
   - **Problem:** The file has 31 lines: 11 `wordfreq`, 10 `csvq`, 10 `todo`. The first line (18:52) is probably the earlier real run, from before the binary-removal fix. A reader who counts finds a mismatch.
   - **Fix:** Either drop that first line from the file, or write "31 of 31" and say which runs ran on which code.
   - **If we skip it:** The headline number cannot be checked against the file.

4. **No test for the infra backoff** (`internal/runner/runner.go:262-285`)
   - **What this is:** After the second infra failure a node waits 5 s, then 10 s, up to 2 min, inside the worker goroutine. The node shows as running during the wait.
   - **Problem:** A wrong shift or condition (for example `InfraFailures > 1`) would go unseen. A restart during a wait ends in a context error, and no test covers that path.
   - **Fix:** Make `infraBackoff` a `Runner` field so a test can set it to 1 ms. Then run an executor that fails with `InfraError` twice and check that the third attempt waits. Check also that a cancel during the wait stops the node.
   - **If we skip it:** A bug here shows up only during a real API outage.

## Nice to have

5. **The agent's check has no time limit of its own** (`internal/executor/claude.go` `check`, `cmd/factory/eval.go` `hiddenTests`)
   - **What this is:** `go test ./...` runs the agent's tests with the parent context. The node time limit covers only the claude call.
   - **Problem:** A test that hangs blocks the run until Go's default 10-minute test timeout, per package. Eval has no deadline either.
   - **Fix:** Add `-timeout 2m` to both `go test` calls.
   - **If we skip it:** One bad test can hold a worker for 10 minutes.

6. **The credential appears in the bwrap command line** (`internal/sandbox/sandbox.go:50-53`)
   - **What this is:** Each `Env` entry becomes `--setenv KEY VALUE` on the `bwrap` command line.
   - **Problem:** Any local user can read `/proc/<pid>/cmdline` and see `CLAUDE_CODE_OAUTH_TOKEN`. On a one-user machine this is a small risk. `README` says the agent can read the token anyway.
   - **Fix:** Use bwrap's `--args FD` option, which reads arguments from a pipe, if this runs on a shared host. Otherwise record it as a known gap.
   - **If we skip it:** Another user on the same host could take the token.

7. **`tail` exists twice** (`internal/executor/claude.go:315`, `cmd/evalgen/main.go:400`)
   - **What this is:** Two copies of the same helper.
   - **Problem:** The copies can drift apart.
   - **Fix:** Put one copy in `internal/gotest` or a small shared package. That saves about 4 lines.
   - **If we skip it:** Almost nothing is lost.

## Prose

Finding 3 above is the only prose problem I found. The other new comments, `README` text and usage lines are short and direct.

Verdict: fix 1 and 2 first, then 3 (a one-line README change).
Lean: -4 lines possible.
Not checked: I did not run any tests. I did not inspect `bwrap` mount behavior or the Go 1.27 `uuid` package beyond the import path.
