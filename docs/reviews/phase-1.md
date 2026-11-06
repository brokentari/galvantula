# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..f7ab30e. Date: 2026-11-06. Cost: $0.50.

What this change does: It adds phase 1 of the factory. One agent node runs Claude Code inside a bubblewrap sandbox. The runtime checks the agent's work (gofmt, vet, `go fix -diff`, build, the agent's own tests, and frozen files), then commits and merges it. It also adds budgets, retries, a usage-limit stop, `factory eval` (runs evals and scores them with hidden tests), and cost display in the UI. The last two commits skip a hidden test as a failure, reword a comment, and let `phasereview` accept "Looks good. Ship."

I assumed the load is one person running the server on their own machine. The default is one eval run at a time, and the README says the API has no login. I read the diff and the code around it. I also checked that `ui/src/gen/events.ts` matches the Go types and that no stale link to the deleted plan `.md` is left.

I found nothing that must be fixed or should be fixed.

### Nice to have

1. **Build caches are not cleared if `hiddenTests` is killed** (`cmd/factory/eval.go`, `hiddenTests`)
   - **What this is:** Scoring makes a new Go build cache and a new HOME folder with `os.MkdirTemp` in the system temp folder. It deletes both with `defer`.
   - **Problem:** A hard kill of `factory eval` leaves them in `/tmp`. The agent caches are covered by the cleanup in `factory serve` at start, but these are not.
   - **Fix:** None needed for one user. If it ever matters, put them under `runs/.cache/build` too, which `serve` already empties.
   - **If we skip it:** A killed eval leaves a few MB per scored run in `/tmp` until the next reboot.

### Prose

None. The README, code comments, and commit text I read are plain, active, and consistent. The lock/standards language matches the repo's own terms.

Verdict: Ship.

Not checked: I did not run the code or tests. I could not measure how much slower each attempt is with a new, empty Go build cache for the agent and for the check. The README already says the eval must run again on the final code. Compare time and cost against the old batch when it does.
