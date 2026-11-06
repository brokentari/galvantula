# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..a8fb46b. Date: 2026-11-06. Cost: $0.56.

**What this change does:** Phase 1 adds a real agent node. Claude Code runs in a bubblewrap sandbox, in a git worktree. The runtime checks the agent's work (gofmt, vet, `go fix`, build, tests, frozen files), then commits and merges it. It also adds spend and time limits, retry and backoff rules, a usage-limit stop, `factory eval` for scoring runs against hidden tests, cost display in the UI, and fixes from six earlier review rounds.

I assumed one person running the server on their own machine, with one agent node at a time. The README says the same.

I read the diff and the server code it depends on (`internal/server/server.go`, `readJSON`, `localOnly`). I also read the UI event handling in `ui/src/App.tsx`. I checked that nothing else still points at the deleted plan `.md` file, and counted the 31 results lines (11 wordfreq, 10 csvq, 10 todo).

### Must fix
None. I traced these paths and found no bug:
- The retry and resume logic, including an infra failure between task failures.
- The backoff arithmetic.
- Budget and run-finish logic.
- The cost fold in Go and TypeScript.
- Git use with `GIT_DIR` set.
- The new `Run` stdout pipe.

The Content-Type check and Host check already block the obvious web-page attacks on the server, which now holds a credential.

### Should fix

1. **Doc comments are mixed up** (`cmd/factory/main.go`, above `sandboxReady` and `credential`)
   - **What this is:** Each function should have a one-line comment above it saying what it does.
   - **Problem:** The comment for `credential` ("credential returns the credential… A token from claude setup-token comes first.") sits directly above the comment for `sandboxReady`. The two run together as one block, so `sandboxReady` is documented as if it returned the credential. `credential` has no comment.
   - **Fix:** Move the first two lines to sit above `func credential()`. Leave the `sandboxReady` lines above `sandboxReady`.
   - **If we skip it:** Readers and `go doc` get the wrong description of both functions.

2. **Comment says the wrong thing closes the pipe** (`internal/claude/claude.go`, comment above `io.Pipe()`)
   - **What this is:** The comment explains why a stray child process cannot hang `Run`.
   - **Problem:** It says "Wait closes the pipe". The code closes `pw` itself, in the goroutine, after `Wait` returns. `Wait` only closes its own OS pipe. A later reader may remove the `pw.Close()` and bring the hang back.
   - **Fix:** Write: "The goroutine closes pw after Wait returns. WaitDelay bounds Wait, so a tool that outlives claude and holds stdout cannot keep read waiting."
   - **If we skip it:** The comment stays wrong, and someone can break the cancel fix without noticing.

### Nice to have

3. **A budget under half a cent passes validation but becomes `0.00`** (`internal/guardrails/guardrails.go` `Validate`, and `claude.go` `--max-budget-usd` formatting)
   - **What this is:** `node_usd` must be above 0, and `Run` passes it to `claude` as `%.2f`.
   - **Problem:** `node_usd: 0.004` is accepted and sent as `0.00`. The `evalgen` fix in this same change says claude reads a budget under a cent as `0.00`. That value may mean no cap.
   - **Fix:** Require `NodeUSD >= 0.01` in `Validate`, and add one line to the existing budget test.
   - **If we skip it:** A typo in `guardrails.yaml` can remove the per-attempt cap, and nothing warns you.

4. **`fmt.Errorf` with no format verbs** (`internal/guardrails/guardrails.go`, the new `budget:` error)
   - **What this is:** The error has a fixed message and no arguments.
   - **Problem:** `errors.New` is the plain form.
   - **Fix:** Use `errors.New(...)`.
   - **If we skip it:** Nothing breaks. It only adds noise.

5. **Two copies of a context-aware sleep** (`internal/runner/runner.go` `sleep`, and `executor.sleep` in `internal/executor/executor.go`)
   - **What this is:** Both wait for a duration or until the context ends.
   - **Problem:** The code is duplicated. I did not check whether the executor copy also guards `d <= 0`.
   - **Fix:** Export one copy, for example `executor.Sleep`, and use it from the runner. Keep the `d <= 0` guard.
   - **If we skip it:** Two copies must change together if the behavior changes.

### Prose
No findings beyond 1 and 2. The README, the eval error messages and the other comments I read are plain and active.

Verdict: fix 1 and 2 first (both are comment-only edits), then ship.
Lean: -10 lines possible (finding 5).
Not checked:
- I could not run anything, so I did not run the tests, `go fix -diff`, or the sandbox on this host.
- I did not verify that the Go 1.27 `uuid` import path is `"uuid"`.
- The README's 31/31 pass result was recorded before the later fixes (read-only check mount, binary removal, sandboxed hidden tests). Re-run the evals before you cite it.
- The agent sandbox and the hidden-test sandbox both mount `runs/.cache/go-build` read-write. An agent could in theory leave poisoned cache entries that scoring then uses. I judged it too contrived to list as a finding.
