# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..98afaa5. Date: 2026-11-06. Cost: $0.56.

**What this change does:** It adds phase 1 of the factory. One Claude Code agent runs inside a bubblewrap sandbox (a Linux tool that hides the host's files from a process). The agent builds a spec in its own git worktree (a separate checkout of the product repo). The runtime then runs checks, commits and merges. It also adds cost and time limits, a "usage limit" failure class that never retries, `factory eval`, and cost display in the UI. The latest commits fix the earlier review findings.

I assumed one developer machine, one `eval` process and up to 3 workers per run.

## Must fix

1. **The agent can run code outside the sandbox through its `.git` link** (`internal/workspace/workspace.go:L88-98`, `internal/executor/claude.go:L158-176`)
   - **What this is:** The agent works in a worktree where `.git` is a small file that points at the real repo. After the agent exits, the runtime runs `git add`, `git diff`, `git commit` and `git ls-files` in that folder, on the host and outside the sandbox. The folder is read-write inside the sandbox.
   - **Problem:** The agent can delete the `.git` link and put its own `.git` folder there. That folder can hold a `config` with `core.fsmonitor = <command>`, or a `hooks/pre-commit` script. Host git then runs that command with your user's rights. The sandbox only protects you while the agent runs, so this escapes it. The plan and the commit text say "git cannot work there", but that only holds for the original link. I did not run a proof of concept.
   - **Fix:** In `Repo.git`, when `dir` is a worktree, set `GIT_DIR` to `<repo>/.git/worktrees/<node>` and `GIT_WORK_TREE` to the worktree. Add `-c core.hooksPath=/dev/null -c core.fsmonitor=false`. Then git never reads anything the agent wrote. Add a test where the fake agent replaces `.git` with a folder holding a hook, and check the hook does not run.
   - **If we skip it:** A prompt-injected or misbehaving agent gets full access to your account, and the sandbox gives a false sense of safety.

## Should fix

2. **The agent's own tests run after the frozen-file check and the binary cleanup** (`internal/executor/claude.go:L163-197` and `L251-270`)
   - **What this is:** `agent` runs `removeBinaries`, then `check`. `check` calls `repo.Changed` first, then runs `go test` in the read-write worktree. Then the runtime commits everything.
   - **Problem:** A test can change the worktree after both guards ran. For example, a CLI test that runs `go build -o wordfreq .` in the package folder leaves a compiled program, and it gets committed. This is the bug that commit 5253e7d fixed. A test can also edit `AGENTS.md` or `spec.md`, and nothing notices.
   - **Fix:** Run the `Changed` check and `removeBinaries` after the five steps, just before `CommitAll`. Keep the early `removeBinaries` if you want the build to see what main gets. Add a test where the fake agent's test file writes a binary and edits `spec.md`.
   - **If we skip it:** Build output and edits to frozen files can reach the product's history even though the guards exist.

3. **No test for the new "stop reason with a waiting gate" branch** (`internal/runner/runner.go:L415-432`, `internal/guardrails/guardrails.go:L78-80`)
   - **What this is:** `finished` now counts a `Waiting` gate as stuck when a stop reason exists, for example when the budget is spent. It is a new branch, and the commit message names it.
   - **Problem:** No test fails if it breaks. `TestRunFails` has no case where a gate waits while another node fails or the budget runs out. `Waiting` also still returns "not done" when `stop == ""`. A slip here leaves a run stuck for good, or fails a run that should wait. The new budget validation rule has no test either.
   - **Fix:** Add one `TestRunFails` case: node `a` fails with no retries, and a gate `g` has no deps. Expect `failed`. Add a one-line budget case to `guardrails_test.go`, such as `RunUSD < NodeUSD` giving an error.
   - **If we skip it:** A later change to `finished` can silently hang or wrongly fail runs at gates.

## Nice to have

4. **The UI still copies the event list on every event** (`ui/src/App.tsx`, `shown` memo)
   - **What this is:** The change appends events in place so a long run stays cheap. `shown` is `events.slice(0, cursor ?? count)`.
   - **Problem:** In live mode `slice` copies the whole list on every event. That is the cost the comment says it avoids. At thousands of events it is small, but the comment is wrong.
   - **Fix:** Use `cursor === null ? events : events.slice(0, cursor)`, and keep `count` in the dependency list so it re-renders. Or fix the comment.
   - **If we skip it:** A slightly misleading comment and a small cost.

5. **`factory eval` can wait forever** (`cmd/factory/eval.go`, the poll loop in `evalOnce`)
   - **What this is:** After a run starts, it reads the event log every 5 seconds until the run is no longer `running`.
   - **Problem:** If the server is killed mid-run, the run stays `running` in the log. `eval` then loops forever, and with `-parallel 1` the other runs never start. The code also joins `dir` to the working directory, so an absolute eval path or a different working directory fails with a confusing error.
   - **Fix:** Give each run an overall deadline, for example the node limit times retries plus slack. Or fail early if no new event arrives for several minutes. Reject absolute `dir` values, or use `filepath.Abs`.
   - **If we skip it:** An unattended batch can hang overnight.

## Prose

6. **README status sentence is repetitive and one long bold span** (`README.md`, Status paragraph)
   - **What this is:** The status paragraph says phase 1 is built and gives the eval result.
   - **Problem:** "Its first eval runs passed ... in 31 of 31 runs" repeats "runs". The whole result sits inside the bold, in one sentence of about 40 words.
   - **Fix:** `**Status: phase 1 (one sandboxed agent) is built.** The first eval batch passed the hidden tests in all 31 runs: one trial run of wordfreq, then 10 runs of each eval (evals/results/2026-11-06.jsonl).` Also replace `[-server ...]` in the `eval` usage line with the real default, `[-server http://localhost:7070]`, as the `start` line does.
   - **If we skip it:** The README is harder to read, and the usage line hides a flag's default.

**Verdict: fix 1 and 2 first.**
**Not checked:** I did not run any code, so the sandbox escape in 1 is reasoned from the code, not proven. I did not read the `internal/claude` read-before-Wait path closely under hung child processes, or the eval specs and their hidden tests.
