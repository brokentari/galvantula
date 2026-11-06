# Review: phase-0

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: start..40bda8a. Date: 2026-11-05. Cost: $0.60.

What this change does: It builds Phase 0 of the software factory. A Go runtime runs a graph of work nodes and records every step in a SQLite event log. A fake executor plays a script, an HTTP/SSE server feeds a React UI, and `evalgen` and `phasereview` call headless Claude. The change also adds the pinned standards, three evals, and the docs.

I assumed one person running this on their own machine. I read the code but did not run it, because Bash was denied part-way through the review (see "Not checked").

## Must fix

None.

## Should fix

1. **Server listens on every network interface, with no limits** (`cmd/factory/main.go:65`, `internal/server/server.go:63,82`)
   - **What this is:** `factory serve` starts an HTTP server. Anyone who can reach it can start runs (`POST /api/runs`) and approve or reject gates.
   - **Problem:** The default address is `:7070`, which means all interfaces. There is no login and no cap on request body size. The README only ever uses `localhost`. Phase 1 will run Claude with write access behind this server, so a LAN neighbor could approve a gate.
   - **Fix:** Change the default to `localhost:7070`. Wrap both request bodies with `http.MaxBytesReader(w, r.Body, 1<<20)`. That is three lines.
   - **If we skip it:** The first real executor starts with an open door.

2. **A crash at the wrong moment kills a run that should resume** (`internal/runner/runner.go:253-269`, `internal/graph/graph.go:199`)
   - **What this is:** When a plan node finishes, `complete` writes two separate events: `graph_updated`, then `node_succeeded`. The README promises that a run continues after `kill -9`.
   - **Problem:** If the process dies between those two writes, the plan node is still "running" on restart. The runtime marks it as an infra failure and runs it again. `Fill` has already removed the slot, so `fillSlot` returns "no slot is filled by this plan node". That counts as a task failure. With `retries: 1` the run fails for good.
   - **Fix:** Make it one event. In `State.Apply`, when `graph_updated` carries a node, mark that node `Succeeded` as well. Skip the separate `node_succeeded` for plan nodes. Make the same one-line change in `ui/src/state.ts`.
   - **If we skip it:** It is rare, but it happens exactly when recovery matters. The run fails with a confusing message.

3. **The "fails on a do-nothing program" check can pass when zero tests ran** (`cmd/evalgen/main.go:236-248`)
   - **What this is:** `evalgen` builds an empty stub program and requires at least 80% of the hidden tests to fail on it. This is the main guard against tests that check nothing.
   - **Problem:** If the stub does not build, `TestMain` exits before any test runs. Then `stubFailed` and `stubPassed` are both 0. `0 < 0.8*0` is false, so the check passes with nothing proven. The three `os.MkdirAll` and `os.WriteFile` calls on lines 241-243 also ignore their errors.
   - **Fix:** Check `stubPassed+stubFailed == 0` and return an error. Check the errors from the three stub writes.
   - **If we skip it:** A broken setup can approve eval tests that were never run against the stub.

4. **Risky logic has no test** (`internal/event/store.go:311-321,362-401`, `cmd/evalgen/main.go:208-277`)
   - **What this is:** There are three gaps:
     - `Store.Subscribe` drops a slow reader, and `Store.Runs` joins events to build run summaries. Neither has a test.
     - `evalgen`'s `check`, `testCounts` and `install` decide whether a model's tests are trusted. Nothing tests them.
     - Finding 2 has no test either.
   - **Problem:** A change to any of these breaks silently. For example, if the drop-slow-reader branch were removed, one stalled browser tab would block `Append` for every run.
   - **Fix:** Add three small tests:
     - Fill a subscriber past 256 events and expect the channel to close.
     - Call `Runs` on a finished run and a running run.
     - Call `testCounts` on a fixture `go test -json` output with zero tests, one fail and one pass.
     - After fixing finding 2, add a runner test that interrupts a plan node after `graph_updated` and expects the run to resume.
   - **If we skip it:** The parts that guard quality and data are the parts nobody checks.

5. **Modern Go guideline `json_v2` is not followed** (`internal/event/event.go:10`, `store.go:4`, `internal/server/server.go:7`, `internal/claude/claude.go:7`, `cmd/evalgen/main.go:16`, `cmd/factory/main.go:12`, `cmd/gen-ts/main.go:7`, `standards/standards.go:10`)
   - **What this is:** The standards say new JSON code in Go 1.27 uses `encoding/json/v2`. All of this code is new.
   - **Problem:** Every file imports `encoding/json`. No comment says why. One real reason exists: v2's `omitempty` keeps `0`. So `attempt,omitempty` and `retries,omitempty` would start to appear on the wire and change the UI's types. Nobody wrote that down.
   - **Fix:** Either move to v2 and change those int fields to `omitzero`, then regenerate the UI types. Or add one line to `AGENTS.md` project rules saying "we keep `encoding/json` because v2 changes `omitempty` for ints; revisit in phase N".
   - **If we skip it:** The next reviewer reports the same finding again, and builders have no rule to follow.

6. **Three more modern Go guidelines are missed** (`cmd/factory/main.go:90-95`, `internal/server/server.go:140-141`, `internal/event/event.go:116`)
   - **What this is:** The code does by hand what the standard library already does.
   - **Problem:** There are three cases:
     - `go func() { <-ctx.Done(); … srv.Shutdown }()` is the `context_after_func` case. Use `context.AfterFunc(ctx, …)`.
     - `time.NewTicker` plus `defer tick.Stop()` is the `time_tick_gc` case. Use `time.Tick(heartbeat)`.
     - `Decode[T](e Event)` is a package-level generic that belongs to `Event`. This is the `generic_methods` case. Make it a method.
   - **Fix:** Apply the three one-line changes. The `Decode` change touches the callers in `event.go` only.
   - **If we skip it:** The code carries a few extra lines and the repo breaks its own standards.

7. **The reviewer's "read-only" Bash can write files** (`cmd/phasereview/main.go:84-86`)
   - **What this is:** `phasereview` allows `Bash(git diff *)`, `git log *` and `git show *`. The comment says this is read-only.
   - **Problem:** `git diff --output=<path>` writes the diff to any path the user can write. The `*` wildcard allows it. A reviewer model that is tricked by text in the diff could overwrite a file.
   - **Fix:** Run `git diff` and `git log` in Go and put the text in the prompt. Then drop `Bash` from `Tools` and delete the `Allow` list. For very large diffs, keep Bash but add a deny rule for `--output`.
   - **If we skip it:** The "read-only" promise is false, though the risk is low today.

## Nice to have

8. **`Runs()` scans the whole events table on every poll** (`internal/event/store.go:362-368`, `ui/src/App.tsx:22`)
   - **What this is:** The UI asks for the run list every 3 seconds. `Runs` finds `run_created` rows by `type`, and there is no index on `type`.
   - **Problem:** The cost grows with every `node_log` line ever written. It is fine for a long while at one user.
   - **Fix:** Add `CREATE INDEX IF NOT EXISTS events_by_type ON events (type, run) WHERE type IN ('run_created','run_finished')`.
   - **If we skip it:** Polling slows down after thousands of logged lines. It matters only much later.

9. **Temp file leaks when `phasereview` exits with an error** (`cmd/phasereview/main.go:66,109`)
   - **What this is:** The reviewer's system prompt is written to a temp file, and `defer os.Remove` is meant to delete it.
   - **Problem:** `os.Exit(1)` and `log.Fatal` skip deferred calls. Every review that does not end in "Ship." leaves a file in `/tmp`.
   - **Fix:** Move the work into `run() int` and call `os.Exit(run())` in `main`. Alternatively, remove the file before calling `os.Exit`.
   - **If we skip it:** A few small files pile up in `/tmp`.

10. **`install` deletes the good tests before it copies the new ones** (`cmd/evalgen/main.go:280-304`)
    - **What this is:** After the checks pass, `install` removes `hidden/` and `reference/` in the repo, then copies the new files in.
    - **Problem:** If a copy fails half-way, or the in-place test on line 181 fails, the old tests are already gone and the new ones are bad. Git can restore them only if they were committed.
    - **Fix:** Copy to a temp folder inside `dir` and rename it over the old folder. Or run the in-place test before you write `provenance.json`.
    - **If we skip it:** A rare failure costs you the last good eval tests unless they were committed.

11. **Shutdown closes the database while workers may still write** (`cmd/factory/main.go:80,100`, `internal/runner/runner.go:213-223`)
    - **What this is:** `Runner.Wait` waits only for the `drive` goroutines. The node workers are separate goroutines.
    - **Problem:** On Ctrl-C the store closes while a worker is still returning from `exec.Run`. A last `log` call then fails with "drop log line". Today this is only noise. With a real executor, worker processes can outlive the runner.
    - **Fix:** Start workers with `r.wg.Go(...)` so `Wait` covers them.
    - **If we skip it:** You see a warning on shutdown now, and orphan processes later.

## Prose

12. **`AGENTS.md` points at a file that does not exist** (`AGENTS.md:19-21`)
    - **What this is:** The "before you call a change done" step tells agents to read the ponytail-review skill at `.claude/skills/ponytail-review/SKILL.md`.
    - **Problem:** The repo has no `.claude/` folder. The skill lives at `standards/vendor/ponytail/skills/ponytail-review/SKILL.md`. An agent that follows the text finds nothing.
    - **Fix:** Use the real path. Or say "run `go run ./cmd/phasereview`".
    - **If we skip it:** Agents skip the review step or invent their own.

13. **README says "phase 0 is done" before the phase review passed** (`README.md:9`)
    - **What this is:** The status line says phase 0 is done.
    - **Problem:** `AGENTS.md:24-36` says a phase is not done until `docs/reviews/phase-0.md` exists with the verdict "Ship." That file does not exist.
    - **Fix:** Write "phase 0 (skeleton) is built and waits for its review." Change it after the review passes.
    - **If we skip it:** The README states something the repo's own rule says is not yet true.

14. **A stock phrase where a plain one is better** (`evals/README.md:22`, `cmd/evalgen/main.go:3`)
    - **What this is:** Both texts explain why the test author must be a different model from the builders.
    - **Problem:** "blind spots" is a worn-out figure of speech. The reader has to guess what it means.
    - **Fix:** Write "so the tests do not repeat the builders' mistakes" in both places.
    - **If we skip it:** Small. It is the one place where the key idea of the eval design is stated loosely.

Verdict: fix 1, 2, 3, 4, 5, 6, 7, 12 and 13 first, then rerun the phase review. Findings 8 to 11 and 14 can wait.

Not checked: I did not run `go vet`, `go test -race`, `go fix -diff` or `npm run build`, and I could not confirm the Go version on this machine. I did not confirm that the Claude CLI accepts `--safe-mode` and `--permission-prompts none`. I did not read `ui/src/GraphView.tsx`, the CSS, the `wordfreq` and `csvq` hidden tests, `Software Factory Plan.html`, or the `graph_test.go` and `guardrails_test.go` files.
