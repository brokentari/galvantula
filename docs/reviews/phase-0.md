# Review: phase-0

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: start..ed3a1b7. Date: 2026-11-06. Cost: $1.56.

**What this change does:** It is the whole of phase 0. It adds a Go runtime that runs a graph of nodes from an event log in SQLite, a fake executor, an HTTP/SSE server, and a React UI that shows runs live and replays them. It also adds three `evalgen`-written evals, the `evalgen` and `phasereview` tools that call headless Claude Code, and the pinned standards block. The latest commit adds claude.Run tests, an `evalgen` path check, and a corrected cycle error message.

I assumed one person running this on their own machine, with the server on localhost. I found no bugs and no security holes at that load. I traced the runner's gate, retry and resume paths, the SSE dedupe, the `localOnly` check, `install`/`putBack`, `countTests`, the `json/v2` payloads, and the Go 1.27 guidelines. None of them broke.

## Should fix

1. **`install` can delete the old tests it failed to put back** (`cmd/evalgen/main.go`, `install`, the `defer os.RemoveAll(stage)` near the top)
   - **What this is:** `install` moves the old tests into a staging folder inside the eval folder. It moves the new ones in, runs `go test`, and calls `putBack` if that fails. The whole design exists so a failed install never loses the old tests.
   - **Problem:** The `defer` removes the staging folder on every exit, including when `putBack` itself fails. Suppose a `Rename` fails with a permission error halfway through putting back. The old files that did not move back are still in `stage/old`, and the `defer` deletes them. The error is reported, but the files are gone.
   - **Fix:** Remove the staging folder only when nothing is left in it. Set a `keep` flag when `putBack` returns an error, and make the `defer` skip `RemoveAll` when `keep` is set. Put the staging path in the returned error so the user can recover by hand.
   - **If we skip it:** A rare filesystem error during rollback loses the old hidden tests. If they are not committed, they are lost for good.

2. **The new path check has no test** (`cmd/evalgen/main.go`, `generate`, the `filepath.IsLocal` check)
   - **What this is:** The commit says evalgen now rejects an eval path outside the repo before any model call. That is a trust-boundary check, and it is what keeps `install` and its `RemoveAll` calls inside the repo.
   - **Problem:** Nothing fails if someone removes or weakens the check. The model call is the expensive part, so the order of "check, then call" matters.
   - **Fix:** Add a small test that calls `generate` with `/tmp/x` and with `../x`, using a zero `guardrails.Guardrails`. Assert that it returns an error. If the check ran too late, the call would fail on `spec.md` or reach the model instead.
   - **If we skip it:** A later edit can silently let `evalgen ../somewhere` replace files outside `evals/`.

## Nice to have

3. **The budget flag is not a hard cap** (`cmd/evalgen/main.go`, the retry loop: `opts.MaxBudgetUSD = max(budget-p.CostUSD, 1)`)
   - **What this is:** `-budget` is documented as "most dollars to spend on one eval". Each retry gets whatever is left of it.
   - **Problem:** The `max(..., 1)` floor gives every retry at least $1, even when the budget is used up. With `-budget 20` and two retries, the spend can reach about $22.
   - **Fix:** If `budget-p.CostUSD <= 0`, return an error. Otherwise pass the remainder as it is, without the floor.
   - **If we skip it:** Spend can exceed the flag by a small, known amount.

4. **Two copies of the plan** (`Software Factory Plan.md` and `.html`)
   - **What this is:** The md file says it has the same text as the HTML file. Both files hold the full plan.
   - **Problem:** Every plan edit must be made twice. The copies already differ: the HTML says "Dashed edges have caps" and "the shaded nodes", and the md says "Retry edges have caps" and "the generated nodes". Earlier review fixes, such as the node-kinds count and the `approve-tasks` rename, had to be made in both.
   - **Fix:** Pick one source. Keep the md because agents read it, and keep the HTML only if you need the diagrams.
   - **If we skip it:** The two files drift, and a reader may follow the stale one.

## Prose

5. **Stock figures of speech** (`README.md`; `Software Factory Plan.md` and `.html`; `cmd/evalgen/main.go`, the `role` prompt)
   - **What this is:** Several headings and phrases use figures of speech you have seen many times: "Ground rules" (plan), "How it fits together" (README), "skeleton" (README status and the phase 0 name), and "They are the ground truth" (the evalgen prompt to the model).
   - **Problem:** Orwell rule 1 forbids these. Each can be plainer, and "ground truth" in the prompt is a figure the model must interpret.
   - **Fix:** Use "Rules", "How the parts connect", "first version" and "They decide whether the build is right".
   - **If we skip it:** It is a style defect only, but the repo's own rules count it as Should fix.

6. **Two terms for one thing** (`ui/src/NodePanel.tsx`; `ui/src/state.ts`)
   - **What this is:** The failure class `infra` has three names in user-facing text. The node panel says "N not the node's fault". `state.ts` log lines and `describe()` say "infrastructure". The plan says "infrastructure failures".
   - **Problem:** The STE rule says to use one term for one thing. A reader may think "not the node's fault" is a different class from "infrastructure".
   - **Fix:** In `NodePanel.tsx`, change the text to `` `${view.infraFailures} infrastructure failures (not counted)` ``.
   - **If we skip it:** The UI keeps two names for one concept.

Verdict: fix 1, 2, 5 and 6 first.
Lean: -264 lines possible (finding 4).
Not checked: I did not run the tests or the UI build, and I did not confirm that the Claude Code flags (`--safe-mode`, `--permission-prompts`) exist in the installed version.
