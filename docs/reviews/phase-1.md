# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..c25f963. Date: 2026-11-06. Cost: $0.64.

What this change does: Phase 1 adds one sandboxed Claude Code agent node. It adds `factory eval`, which starts runs, scores each product with hidden tests and records the results. It also adds a workspace (a git repo with one worktree per agent node), spending limits, usage-limit stops, retry and resume rules, UI cost display, and the first 31-run results file. The last two commits set the $0.50 median-cost limit in the plan and note that the batch must run again on the final code.

I assumed one developer running the server locally and one eval at a time. The server rejects non-localhost Hosts and requires `application/json`, so the no-login API is covered for that load.

**Should fix**

1. **Result lines do not say which code made them** (`cmd/factory/eval.go:L28-46`, `L103-109`, `README.md:L8-14`)
   - **What this is:** Each run adds one line to `evals/results/<date>.jsonl`. The README says the 31 lines came from commit `4f02b54`, and tells you to run the batch again on the final code before phase 1 is done.
   - **Problem:** A line records the graph name, the standards hash and the Claude version, but not the factory's commit. The file is named by date and opened in append mode. The final batch will almost certainly run today, so it lands in `2026-11-06.jsonl` after the 31 old lines. The file then holds 62 runs from two code versions, and nothing in it tells them apart. The README's "31 runs, commit 4f02b54" and the medians no longer match the file. The done criterion (8 of 10 pass, median under $0.50) would be judged on a mix of old and new runs.
   - **Fix:** Add a `Commit string` field to `evalResult`. Fill it once per eval with `git rev-parse --short HEAD`, adding `-dirty` if `git status --porcelain` prints anything. The eval already requires the repo root, so git is available. Then move the old 31 lines to a name that shows their commit, or let the README point to the new batch by commit.
   - **If we skip it:** The final batch cannot be told apart from the first. The proof that phase 1 is done is ambiguous, and someone has to sort it out by hand from timestamps.

**Nice to have**

2. **No test for `median`** (`cmd/factory/eval.go:L275-285`)
   - **What this is:** `median` and `printSummary` produce the "median $" figure that the plan's done criterion uses.
   - **Problem:** The even-count branch averages two values, and `median` also sorts the caller's slice in place. A slip in either would silently change the number you compare to $0.50. Nothing tests them.
   - **Fix:** Add a table test of three cases: empty gives 0, `{3,1,2}` gives 2, and `{4,1,3,2}` gives 2.5.
   - **If we skip it:** A wrong median is unlikely, but nothing would catch one.

Prose: I found nothing. The README, plan text, comments and commit messages are plain and active. The numbers in the README status match `evals/results/2026-11-06.jsonl`: 11 wordfreq runs (one trial plus 10), 10 csvq and 10 todo. The medians are about $0.10 to $0.17.

Modern Go: I found no missed guideline. The code uses `encoding/json/v2`, the standard `uuid`, `errors.AsType`, `wg.Go`, `cmp.Or`, `slices.Sorted(maps.Keys(...))` and `strings.Lines`.

Verdict: fix 1 first. It is cheap, and it must be in place before the final batch runs. Item 2 is optional.

Not checked: I could not run anything. The tests that need `bwrap` and a real Claude run were read, not run, and I did not check the Claude cost reported when a session is resumed (whether `total_cost_usd` is for that call or the whole session).
