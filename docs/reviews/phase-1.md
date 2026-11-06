# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..fdc2085. Date: 2026-11-06. Cost: $0.61.

What this change does: It adds phase 1 of the factory. One Claude Code agent builds a spec inside a bubblewrap sandbox, and the runtime checks, commits and merges its work. The change also adds budgets, retry and backoff rules, a usage-limit stop, a `factory eval` command that scores runs with hidden tests, and cost display in the UI. I assumed a single local user running one server and a few runs at a time, as the README says.

I could not run any code, so Go tool behavior below comes from reading the diff and from knowledge of the tools.

### Must fix

1. **A run where the agent builds nothing counts as a success** (`internal/executor/claude.go:L184-195`, the `check` function)
   - **What this is:** After the agent exits, `check` runs `gofmt -l`, `go vet`, `go fix -diff`, `go build` and `go test` on the worktree. If all pass, the runtime commits and merges, and the node succeeds.
   - **Problem:** The module has only `go.mod` and no Go files, and I believe all five commands exit 0 in that case. They only print "matched no packages" or "no packages to test". The agent can end after a refusal, a clarifying question, or only writing `NOTES.md`. In that case `CommitAll` finds little or nothing, the merge is a no-op, and the run is marked **succeeded** with no product. The agent's cost is charged. Only the eval's hidden tests would catch it. A normal `factory start` run reports success.
   - **Fix:** Add a first step to `check` that fails when `go list ./...` prints no packages. For example, run it in the sandbox and add "the agent wrote no Go package" to `errs` if the output is empty. Add one test with `fakeAgent(t, "")` that expects a check failure, which also counts toward retries.
   - **If we skip it:** An empty run shows green in the UI and in `runs/<run>/repo`, and the user finds out only when they open the product.

### Should fix

2. **Two failure paths in `claude.Run` have no test** (`internal/claude/claude.go:L110-117`, `internal/executor/claude.go:L169-176`)
   - **What this is:** Two decisions control paid retries. First, claude printing a limit message on stderr with no result line becomes `ErrUsageLimit` and never retries. Second, a crash with no result becomes `ErrNoResult`, which the executor turns into an infra failure with no cost.
   - **Problem:** Tests cover a limit message inside the result line and a timeout. Nothing covers the stderr-only limit or the crash-to-infra mapping. A change to either would turn "never buy more usage" into up to 5 retries, or turn a crash into a counted task failure. No test would fail.
   - **Fix:** Add two table rows. In `internal/claude/claude_test.go`, use a script that runs `echo 'usage limit reached' >&2; exit 1` and expect `ErrUsageLimit`. In `TestAgentFailureClasses`, use `fakeAgent(t, "exit 3")` and expect `IsInfra(err)` with cost 0.
   - **If we skip it:** A later edit to `limitPhrases` or to the error switch can break the "never retry on a limit" rule without any test failing.

### Nice to have

3. **`factory eval` can hang forever on a stuck server** (`cmd/factory/main.go`, `startRun`)
   - **What this is:** `startRun` calls `http.Post`, which uses the default client with no timeout.
   - **Problem:** If the server accepts the connection and never answers, the eval waits forever and holds a parallel slot. The quiet-run limit only starts after the run exists.
   - **Fix:** Use `(&http.Client{Timeout: 30 * time.Second}).Post(...)`.
   - **If we skip it:** An eval batch can hang with no message.

### Prose

I checked the README, the code comments, the guardrails comments, the plan text edits and the UI text. They are short, active and consistent, and I found no findings. The result counts in the README (31 runs: 11 wordfreq, 10 todo, 10 csvq) match `evals/results/2026-11-06.jsonl`.

Checked and fine:
- The local API's cross-site and DNS-rebinding guards.
- The credential going through the environment and not the command line.
- The planted `.git` handling in host git.
- The frozen-file check.
- `ui/src/gen/events.ts`, which is guarded by a test.
- No leftover references to the deleted `Software Factory Plan.md`.

Verdict: fix 1 first.
Lean: no lean findings.
Not checked: I did not run bwrap, Go or the UI. Finding 1 depends on `go vet`, `go build` and `go test` exiting 0 on a module with no packages, and I am not certain of that. Confirm it with a one-line `fakeAgent(t, "")` test.
