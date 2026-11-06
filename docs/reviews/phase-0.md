# Review: phase-0

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: start..fa6f4e4. Date: 2026-11-06. Cost: $0.76.

**What this change does:** It builds phase 0 of the software factory. The runtime drives a graph of nodes from an append-only SQLite event log. A fake executor plays scripted runs, an HTTP/SSE server feeds a React UI, and the eval specs get hidden tests from evalgen. It also adds the standards block for agents, plus `phasereview` for end-of-phase reviews. I assumed one developer running everything on localhost.

I traced the main flows: the run loop, resume after a crash, plan output filling a slot, the SSE backlog and live handoff, the evalgen install and restore path, and the server's Host and Content-Type checks. I found no bug, security hole or data-loss path. I checked Go against the modern guidelines (json/v2, `cmp.Or`, `slices`, `errors.AsType`, `WaitGroup.Go`, `strings.Lines`, `context.AfterFunc`) and found nothing missed. The prose reads plainly. I found no stock phrases or avoidable jargon, so the Prose group is empty.

### Nice to have

1. **`-budget` can turn into "no cap" at the very end** (`cmd/evalgen/main.go`, retry loop; `internal/claude/claude.go`, budget flag)
   - **What this is:** After a failed check, evalgen resumes the model with the money left: `left := budget - p.CostUSD`. `claude.Run` writes it with `strconv.FormatFloat(..., 'f', 2, 64)`.
   - **Problem:** If `left` is positive but under half a cent, such as 0.004, the flag value becomes `"0.00"`. I could not confirm what `claude` does with `0.00`. It may read it as "no limit", which would break the "hard cap" the last commit set out to add.
   - **Fix:** In evalgen, change `left <= 0` to `left < 0.01`. One line.
   - **If we skip it:** The failure needs spend to land within half a cent below the budget, so it is rare. If it happens, one eval run could go past `-budget`.

2. **The UI copies the whole event list for every event** (`ui/src/App.tsx`, `useEvents`)
   - **What this is:** Each live event runs `[...s.events, e]`.
   - **Problem:** This costs O(n²) over a run. The fake demo has about 60 events, so it is fine now. Once phase 1 streams real agent output, a run with 20,000 log events does about 200 million element copies, and the page will stutter.
   - **Fix:** Do this when phase 1 lands. Keep events in a ref and bump a counter to re-render, or cap the stored log events per node.
   - **If we skip it:** Nothing breaks in phase 0. Long real runs will make the UI sluggish.

**Verdict: Ship.**

**Not checked:** I did not run the tests or the Go checks, since I cannot run commands. I did not verify that the installed `claude` accepts `--safe-mode` or `--permission-prompts none`, or how it treats `--max-budget-usd 0.00`. I did not check that the pinned npm versions are at least two weeks old.
