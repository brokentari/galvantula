# Review: phase-0

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: start..5b61c98. Date: 2026-11-06. Cost: $0.77.

What this change does: this is the last two review-fix commits on top of phase 0, the software factory's first phase. It adds a content-type check on API bodies, a UTF-8-safe `event.New`, a clean-tree check in `phasereview`, a safer file swap in `evalgen`, and an apply-once live path in the UI. The full range also covers the graph runtime, the SQLite event log, the HTTP and SSE server, the React UI, the standards renderer and three evals.

I assumed one user on localhost with a few runs, per the README. I did not assume many users.

## Must fix

1. **The review loop in AGENTS.md cannot run twice** (`cmd/phasereview/main.go`, `review()`, the `git status --porcelain` check)
   - **What this is:** Before it asks the reviewer, `phasereview` refuses to run if the working tree has uncommitted changes. This makes the reviewer's diff match the files it reads.
   - **Problem:** Each run writes `docs/reviews/<phase>.md`. After the first run that file is untracked, so the second run stops with "commit your changes first". AGENTS.md step 3 says "fix, commit the fixes, run the review again", and step 4 says to commit the report only at the end. The documented loop fails on its second pass.
   - **Fix:** Leave the reports out of the status check: `git("status", "--porcelain", "--", ".", ":(exclude)docs/reviews")`. The diff already leaves them out.
   - **If we skip it:** You must commit a half-finished report between runs, or delete it by hand. That breaks the process the phase depends on.

## Should fix

2. **Another web page can drive the local server through DNS rebinding** (`internal/server/server.go`, `New`)
   - **What this is:** The API has no login and binds to localhost. The commit added a content-type check so a cross-site `text/plain` POST fails.
   - **Problem:** A page on `evil.example` can rebind that name to 127.0.0.1. The browser then treats `http://evil.example:7070` as the same origin. The page can send `application/json`, read `/api/runs`, start runs, and approve gates. The server never checks the `Host` header. Today the damage is small because only the fake executor exists. Once real agents run, gate approval is the safety control.
   - **Fix:** Wrap the mux. Reject requests whose `Host` name is not `localhost`, `127.0.0.1`, `[::1]` or the host from `-addr`. About 8 lines.
   - **If we skip it:** A malicious page can approve gates without you.

3. **A hung product hangs the whole eval** (`evals/evalkit/evalkit.go`, `Run`)
   - **What this is:** The hidden tests run the factory-built command with `exec.Command` and no time limit.
   - **Problem:** A product with a loop bug, such as `todo list` never returning, blocks that test. After 10 minutes `go test` panics and kills the package. All results for that eval are lost, not just the one failing test.
   - **Fix:** Use `exec.CommandContext` with `context.WithTimeout(t.Context(), 30*time.Second)`. A timeout then fails that one test.
   - **If we skip it:** Eval runs on buggy products waste 10 minutes each and give no per-test signal.

4. **The file swap that guards the old hidden tests has no test** (`cmd/evalgen/main.go`, `install` and `swap`)
   - **What this is:** The last commit rewrote `install` to keep the old tests aside and put them back if the new ones fail in place. The old tests are the ground truth for the evals.
   - **Problem:** Only `countTests` is tested. The restore path (`swap(old, dir)` after a failed in-place test, or after a failed `swap(fresh, dir)`) is new data-protection logic and has no test that fails if it breaks. It is also hard to test now, because `goCmd(".", …)` and the relative `./<dir>` path tie it to the repo root.
   - **Fix:** Pass the "run the tests" step in as a function argument. Add one test with a temp dir where that function fails. Assert that the old `hidden/`, `reference/` and `assumptions.md` are back and the new ones are gone.
   - **If we skip it:** A bug in the restore code could silently delete the only copy of an eval's tests.

5. **A hand-written search loop where the Go guidelines name a helper** (`internal/runner/runner.go`, `firstFailed`)
   - **What this is:** `firstFailed` loops over the nodes to find the first one with status `Failed`.
   - **Problem:** This is the `slices_index_func` guideline. `go fix` does not catch it, so it is a missed guideline.
   - **Fix:** `i := slices.IndexFunc(st.Graph.Nodes, func(n graph.Node) bool { return st.Nodes[n.ID].Status == event.Failed })`, then return `""` if `i < 0`, otherwise the node ID.
   - **If we skip it:** Nothing breaks. The code stays 4 lines longer than needed.

## Nice to have

6. **A store failure is reported as "unprocessable"** (`internal/server/server.go`, `startRun`)
   - **What this is:** `startRun` returns 422 for every error from `runner.Start`.
   - **Problem:** `Start` can also fail when the SQLite append fails, for example on a full disk. That is a server error, not a bad request. The UI shows the wrong cause.
   - **Fix:** Return 500 when the error is not from `graph.Validate` or `ParseScript`. Do this by validating both before calling `Start`, or by wrapping the append error.
   - **If we skip it:** Misleading status codes when the disk fails.

7. **The plan exists twice** (`Software Factory Plan.md` and `Software Factory Plan.html`)
   - **What this is:** The `.md` file says "The HTML version has the two diagrams. This file has the same text."
   - **Problem:** Two files must change together, and they have already drifted. For example, the graph caption says "shaded nodes" in one and "generated nodes" in the other.
   - **Fix:** Keep the HTML, which has the diagrams. Delete the `.md`, or generate it from the HTML.
   - **If we skip it:** The two copies disagree over time, and one of them misleads.

## Prose

8. **Stock phrases and figures of speech in the plan** (`Software Factory Plan.md` and `.html`)
   - **What this is:** Plan text that Orwell rule 1 and the STE baseline apply to.
   - **Problem:** These phrases are figurative, or they are jargon where plain words exist.
     - "They are never passed downstream." Say "They are never given to later nodes."
     - "Once the runtime works, it is a thin layer." Say "Once the runtime works, it needs little code."
     - "Replanning is the last resort." Say "Replanning runs only after retries and the fix node fail."
     - "The second retry starts a new session, which escapes a bad approach." Say "…so the node can try a different approach."
   - **Fix:** Replace those four phrases in both files, or in the one file you keep after finding 7.
   - **If we skip it:** The plan reads less clearly, and readers who do not know the idioms may misread it.

Verdict: fix 1, 2, 3, 4, 5 and 8 first.
Lean: -263 lines possible.
Not checked: I could not run the tests or any command. I did not verify the `claude` flags (`--safe-mode`, `--permission-prompts`) or whether Go 1.27 accepts the generic method `Event.Decode[T]`.
