# Review: phase-0

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: start..1823952. Date: 2026-11-06. Cost: $0.91.

What this change does: It adds Phase 0 of the software factory and fixes the earlier review findings. The pieces are a Go runtime with a SQLite event log, a fake executor, an HTTP/SSE server, a React UI, pinned coding standards, three model-written evals, and the `evalgen` and `phasereview` tools. I assumed one person on a laptop, with the server on localhost and only the fake executor. I read the code and did not run it.

## Must fix

None.

## Should fix

1. **Any web page can call the local API** (`internal/server/server.go:62-70,88-99`)
   - **What this is:** `POST /api/runs` and `POST /api/runs/{run}/gates/{node}` read the body as JSON. They do not look at the `Content-Type` header. The last commit moved the listen address to `localhost`, but a browser on the same machine can still reach it.
   - **Problem:** A page the user opens can send a cross-site POST with `Content-Type: text/plain` and a JSON body. This is a "simple request", so the browser sends it with no preflight check. The server accepts it. That page can start runs and approve gates. The 1 MB body cap in `maxBody` also has no test.
   - **Fix:** In `startRun` and `decide`, return 415 unless `r.Header.Get("Content-Type")` is `application/json`. That forces a preflight, which the server never answers. The UI already sends that header. Add one test that sends `text/plain` and expects 415, and one that sends a body larger than 1 MB.
   - **If we skip it:** With the fake executor the damage is junk runs. Once phase 1 adds a real executor, a web page could approve a gate that guards real work.

2. **`evalgen` may accept tests that `go fix` flags** (`cmd/evalgen/main.go:194-199`)
   - **What this is:** `check` runs `go vet` and `go fix -diff` and treats only a non-zero exit as failure. The repo's own rule in `AGENTS.md` says `go fix -diff` "must print nothing". The rule names the output, not the exit code.
   - **Problem:** I could not run `go fix`. As I understand it, `go fix -diff` prints the diffs and still exits 0. If so, tests that need changes pass this check, and `provenance.json` and the README claim they were checked.
   - **Fix:** Fail when `out != ""` as well as when `err != nil`, for the `fix` command. This is the same pattern the `gofmt` line above it already uses.
   - **If we skip it:** `evalgen` can install tests that break the repo's own Go standard without noticing.

3. **The phase review can read a mix of old and new code** (`cmd/phasereview/main.go:128-146`, `AGENTS.md:32-37`)
   - **What this is:** `describeChange` builds the diff with `git diff base HEAD`. That covers committed work only. The reviewer's `Read`, `Glob` and `Grep` tools read the working tree.
   - **Problem:** The `AGENTS.md` step says "fix every finding, run the review again". A developer who fixes the code and reruns before committing gives the reviewer the old diff and the new files. The verdict then describes neither version.
   - **Fix:** In `review`, run `git status --porcelain` and return an error ("commit your changes first") if it prints anything. Add "Commit the fixes, then run it again" to step 3 of `AGENTS.md`.
   - **If we skip it:** A review can say "Ship." about code that was never reviewed.

4. **`event.New` drops a payload that does not encode** (`internal/event/event.go:90-97`)
   - **What this is:** `New` calls `json.Marshal(data)` and ignores the error. The comment says the payloads hold only simple types.
   - **Problem:** `encoding/json/v2` rejects invalid UTF-8 by default. The `csvq` reference needs `AllowInvalidUTF8` for this reason. A log line or error text with a stray `0xff` byte, for example from a real executor's output, makes `Marshal` fail. The event is then stored with `Data` set to nil. In the UI, `e.data.line` then throws and the page breaks. Phase 0's fake scripts come from YAML, so it cannot happen yet.
   - **Fix:** Have `New` replace invalid bytes with `strings.ToValidUTF8` for the strings it holds. Or make it return an error that the callers pass on. Do not store an event without its data.
   - **If we skip it:** The first binary byte in executor output hides an event and crashes the UI.

## Nice to have

5. **The UI refolds the whole log for every new event** (`ui/src/App.tsx:36-49,57-58`)
   - **What this is:** `useEvents` copies the array with `[...prev, e]`. `fold(shown)` then rebuilds the run from event 1.
   - **Problem:** Work grows with the square of the event count. A run with 20,000 log events does about 200 million steps in total, so the page stalls. It is fine for the roughly 50 events of the demo.
   - **Fix:** In live mode, keep the `RunView` in state and call `apply(run, e)` once per event. Use `fold` only for replay.
   - **If we skip it:** Phase 1 runs with streamed agent logs make the UI slow.

6. **The phase review includes the previous review report** (`cmd/phasereview/main.go:35`)
   - **What this is:** `skipped` lists paths that the diff leaves out.
   - **Problem:** `docs/reviews/` is not in the list. A rerun gives the reviewer its own earlier findings, which costs tokens and may lead it to repeat them.
   - **Fix:** Add `"docs/reviews"` to `skipped`.
   - **If we skip it:** Reruns cost more and repeat old findings.

7. **A failed post-install test leaves the new tests in place** (`cmd/evalgen/main.go:176-184`)
   - **What this is:** The staging folder protects against a failed copy. After `install`, `generate` runs the tests once more. This step exists because `install` rewrites the import paths.
   - **Problem:** If that run fails, the old tests are already gone and the new files stay. Only git can bring the old ones back.
   - **Fix:** Rewrite the import paths in the staging copy and run the test on it before the renames. This is a small move of the existing code.
   - **If we skip it:** A rare failure costs the last good eval tests.

## Prose

8. **British spelling** (`Software Factory Plan.md:262`, `Software Factory Plan.html:861`)
   - **What this is:** The "Known gaps" list says "the three defences".
   - **Problem:** The rules call for American English. Write "defenses".
   - **Fix:** Change the word in both files.
   - **If we skip it:** The plan mixes two spelling styles.

9. **"fold" is jargon and the README never defines it** (`README.md:44,56`)
   - **What this is:** The README says "the fold from events to state" and "the UI's fold". The word comes from functional programming. It is also the name of the `Fold` function in `internal/event`.
   - **Problem:** A reader who does not know the term has no way to learn it from the README. The comment in `internal/event/event.go:97` has a similar stock phrase: "a crash cannot fall between the two".
   - **Fix:** Write "the code that rebuilds state from events (called a fold)" once. In the `event.go` comment, write "so a crash cannot leave one write done and the other missing".
   - **If we skip it:** Small. A new reader stops at the main idea of the design.

Verdict: fix 1, 2, 3, 4, 8 and 9 first.

Not checked: I did not run any command, so I did not check `go fix -diff` exit behavior (finding 2), that `Event.Decode[T]` compiles as a generic method, or the Node version for the UI. I did not read the model-written eval tests and `assumptions.md` files for prose. I did not check whether the `Software Factory Plan.md` and `.html` have drifted apart. `evalgen` runs the model's code with no sandbox, as the plan's "Known gaps" already says for the factory.
