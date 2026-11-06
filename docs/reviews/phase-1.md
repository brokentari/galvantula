# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..338be87. Date: 2026-11-06. Cost: $0.53.

What this change does: Phase 1 adds a real agent node. Claude Code runs in a bubblewrap sandbox (a Linux tool that hides the host's files) in its own git worktree. After the agent exits, the runtime checks the work, then commits and merges it. It also adds budgets, retry rules, a usage-limit stop, and `factory eval`, which runs evals N times and scores each product with hidden tests. I assumed the load is one person on one machine, with 1 to 3 runs at a time. I read the diff and spot-checked the runner and server code. I could not run any tests.

## Should fix

1. **A missing `bwrap` looks like the agent's fault** (`internal/sandbox/sandbox.go:L31-50`, `internal/claude/claude.go` Start error path, `cmd/factory/main.go` serve)
   - **What this is:** Every agent attempt and every check step starts `bwrap`. The README's "Run a real agent" section never says that `bwrap` and Linux are needed.
   - **Problem:** If `bwrap` is missing, `cmd.Start` fails with `claude -p: exec: "bwrap": not found`. That is not `ErrNoResult`, so it counts as a task failure. The run burns its 2 retries and ends with a vague "node build failed". This setup is likely, because your own platform is darwin, where `bwrap` does not exist.
   - **Fix:** In `serve`, call `exec.LookPath("bwrap")` when a credential is set, and return a clear error if it is missing. Add one README line: "needs Linux and bubblewrap". Go also has to be reachable as `/usr/bin/go`, because the sandbox `PATH` is only `/usr/bin`.
   - **If we skip it:** A new user wastes a few minutes finding a setup problem that the output blames on the agent.

2. **A blip during the resumed attempt throws away the check feedback and the session** (`internal/runner/runner.go:L268-272`, `internal/executor/claude.go:L142-163`)
   - **What this is:** After the first task failure, the next attempt resumes the session with the check output as feedback. `Feedback` is `ns.LastError`, and `Resume` needs `LastClass == Task`.
   - **Problem:** Say attempt 1 fails the check, and attempt 2 (the resume) fails with an infra error, such as "claude -p gave no result" from a rate limit. `LastError` is now the infra text and `LastClass` is `Infra`. So attempt 3 does not resume. It resets the worktree and starts a new session, and the brief says "The factory reported: infra: …". The real check output is lost and the session's context is paid for again. The latest commit fixed only the opposite order (infra first, then task).
   - **Fix:** Keep the last task error apart. Add `LastTaskError string` to `NodeState`, set it only when `Class == Task`, and use it for `Feedback`. Resume when `TaskFailures == 1`, whatever the last class was. Add one runner test: a task failure, then an infra failure, then check the third `Task`.
   - **If we skip it:** The agent gets misleading feedback and a fresh session at the cost of a full attempt, which is up to $3. This happens most under rate limits.

3. **The new retry timing test can fail on a slow machine** (`internal/runner/runner_test.go` `TestInfraRetriesBackOff`)
   - **What this is:** The test says the first infra retry must start within 200 ms (`d >= r.infraBackoff` fails).
   - **Problem:** Between the two attempts the runner writes events to SQLite, with fsync. On a loaded CI box that can take longer than 200 ms, so the test fails at random.
   - **Fix:** Set `infraBackoff` to about 2 s and check that the first gap is less than half of it. Or compare the gaps: the second must be clearly longer than the first.
   - **If we skip it:** The test fails on some runs and people stop trusting it.

## Nice to have

4. **An attempt cut off by a shutdown loses its cost** (`internal/runner/runner.go:L312-315`)
   - **What this is:** On shutdown the outcome is dropped and the node stays "running". On restart it is recorded as an infra failure with cost 0.
   - **Problem:** The killed agent may have spent up to `node_usd`. Several restarts in one run can pass `run_usd` without the runtime noticing.
   - **Fix:** On shutdown, pass `o.res.CostUSD` into that later infra failure. This takes some plumbing, so accepting the gap and writing it down as a `shortcut:` comment is also fine.
   - **If we skip it:** The run budget is a little too low after restarts.

5. **The start API can spend your Claude plan, and the README does not say so** (`internal/server/server.go` `startRun`, README "Run a real agent")
   - **What this is:** `POST /api/runs` with `executor: "claude"` now starts a paid agent. The server only checks the Host header and the JSON Content-Type.
   - **Problem:** On a shared machine, any local user can `curl localhost:7070` and use the owner's token, with no limit on how many runs. Browsers are blocked by the checks.
   - **Fix:** Add one README sentence: "the API has no login, so run the server only on a machine you alone use". The `-addr` help text already says this, but the README does not.
   - **If we skip it:** Someone on a shared host is surprised by the bill.

6. **A lost eval run keeps spending** (`cmd/factory/eval.go:L158-161`)
   - **What this is:** After 30 quiet minutes, `evalOnce` gives up on the run and records an error.
   - **Problem:** The server may still be running that run, which can spend up to `run_usd`. The error text asks the user to check the server, but nothing stops the run.
   - **Fix:** Add one more sentence to the error text, with the run id, saying the run may still be going.
   - **If we skip it:** A stuck run can cost up to $12 with no warning.

**Prose:** I found no problems in the README, comments, guardrails text or commit messages. They use short active sentences and plain words.

Verdict: fix 1 and 2 first, then 3.

Not checked: I did not run any tests, including the sandbox, eval and bwrap tests. I did not check that the `uuid` import path resolves to the Go 1.27 standard library. `go.mod` lists `github.com/google/uuid` only as indirect. I did not run `go fix -diff`.
