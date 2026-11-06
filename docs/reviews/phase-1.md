# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..1c3fe56. Date: 2026-11-06. Cost: $0.44.

What this change does: It adds phase 1. One Claude Code agent now builds a spec inside a bubblewrap sandbox (a Linux tool that hides the rest of the machine from a command). The runtime checks the agent's work, commits it and merges it. It also adds cost and usage-limit handling, retry backoff, and `factory eval`, which scores runs against hidden tests. I assumed one person running a local factory with a few runs at a time. The server has no login and the agent is untrusted code.

## Must fix

1. **Eval drops runs whose product does not build** (`cmd/factory/eval.go:L168-172`, `L226-228`, `L100-106`)
   - **What this is:** `hiddenTests` runs the eval's hidden tests against the product. `evalOnce` returns its error, and the loop in `runEvals` prints it and skips recording the run.
   - **Problem:** When the product has no `./cmd/<name>`, `evalkit.Main` exits 1 before any test runs. The count is 0 passed and 0 failed, so `hiddenTests` returns "no hidden test ran". The run never reaches the JSONL file or the summary. This is the most common way an agent fails the spec. The pass rate then counts only the runs that built, so it reads too high. "8 of 10" can really be 8 of 14.
   - **Fix:** In `evalOnce`, treat a scoring error as a failed run. Keep `r`, set `r.Pass = false`, put the error text in `r.Reason`, and return `r, nil`. Add one test for `printSummary` that includes such a run.
   - **If we skip it:** The phase-1 exit gate ("8 of 10 pass") can show success when it did not happen.

2. **The runtime reads files the agent made, and follows symlinks** (`internal/executor/claude.go:L211-230`)
   - **What this is:** `removeBinaries` runs outside the sandbox, in the factory process. It calls `os.ReadFile` on every new file in the agent's worktree.
   - **Problem:** The sandbox mounts `/dev`. The agent can run `ln -s /dev/zero x`. `ReadFile` then reads forever and the factory server runs out of memory. This also happens when a stray build leaves a symlink to a large file. `ReadFile` also reads whatever a symlink points to on the host.
   - **Fix:** Skip anything that is not a regular file (`os.Lstat`, then `Mode().IsRegular()`). Read only the first 8000 bytes with `os.Open` and `io.ReadFull`, not the whole file.
   - **If we skip it:** One bad agent run can crash the server and every other run on it.

3. **Hidden tests run the agent's code with the whole repo readable** (`cmd/factory/eval.go:L199-217`)
   - **What this is:** `hiddenTests` builds and runs the agent's program in the sandbox. It mounts the factory repo (`repo`) read-only, and the network is shared.
   - **Problem:** The program is code the agent wrote, and the repo holds more than the tests. `.gitignore` now lists `claude-token`, which suggests the token file sits in the repo. `exports/` is there too, and so are the reference answers and other runs' products. The agent's code can read these and send them out. The agent's own sandbox hides all of this, so this path is weaker than the one it is meant to match.
   - **Fix:** Do not mount the repo. Copy `go.mod`, `evals/evalkit` and `evals/<name>/hidden` into a temp folder and mount that. Or bind only those paths read-only.
   - **If we skip it:** A hostile or prompt-injected spec can steal the subscription token during scoring.

## Should fix

4. **Timed-out and crashed attempts count as $0** (`internal/executor/claude.go:L176-186`, `internal/claude/claude.go:L107-117`)
   - **What this is:** The cost comes only from the final `result` line of the stream.
   - **Problem:** When the 20-minute limit kills the agent, no `result` line exists, so `spent` is `{CostUSD: 0}`. The agent may have used up to `node_usd`, but the run total, the UI and the run-budget stop do not see it. Repeated timeouts can pass `run_usd` without the factory noticing.
   - **Fix:** When the attempt times out or gives no result, charge `Budget.NodeUSD`, which is the most one attempt can cost. Say so in a one-line comment.
   - **If we skip it:** The run budget does not stop spending in the case where it matters most.

5. **`removeBinaries` runs after the check and can delete needed files** (`internal/executor/claude.go:L158-164`, `L211-230`)
   - **What this is:** After the check passes, any new file with a NUL byte is deleted, then the work is committed.
   - **Problem:** A test fixture such as `testdata/sample.gz` or a PNG has NUL bytes. The check passed with it, the runtime deletes it, and the merged product's tests now fail. Nothing checks the product after the deletion.
   - **Fix:** Run `removeBinaries` before `c.check`, so the check sees what will be committed. Or delete only files that sit in the worktree root with the executable bit set. Add a test with a binary fixture in `testdata/`.
   - **If we skip it:** A good run can ship a product that fails its own tests.

## Nice to have

6. **Ignored error gives a wrong standards hash** (`cmd/factory/eval.go:L66`)
   - **What this is:** `lock, _ := standards.Lock()` records which standards version produced each result.
   - **Problem:** If the read fails, the hash is of empty bytes, and the results look comparable when they are not.
   - **Fix:** Return the error: `lock, err := standards.Lock(); if err != nil { return err }`.
   - **If we skip it:** Unlikely, because the file is embedded. The cost is only a wrong label in rare cases.

## Prose

7. **Comment in `guardrails.yaml` is above the wrong block** (`guardrails.yaml:L5-14`)
   - **What this is:** The block above `models:` explains which model plays each role and why the eval author must differ.
   - **Problem:** The change put the new `budget:` comment and block between that comment and `models:`. The model comment now sits above the `budget` comment, and `models:` has no comment.
   - **Fix:** Move the `budget:` block, with its comment, above the model comment.
   - **If we skip it:** The next reader sees a comment about models over the spending limits.

8. **README status is out of date** (`README.md:L8-10`, and the `cmd/factory` row of the "How the parts connect" table)
   - **What this is:** The status says phase 1 "waits for its first real eval runs". The table says `cmd/factory` has `serve`, `start`, `validate`.
   - **Problem:** The same change records 30 of 30 passing runs in `evals/results/2026-11-06.jsonl`. `eval` and `standards` are missing from the table.
   - **Fix:** Say "phase 1 is built; the first batch passed 30 of 30 hidden tests". Add `eval` and `standards` to the table row.
   - **If we skip it:** The README tells the reader the work has not started when it has finished.

9. **Usage text shows a default that does not exist** (`cmd/factory/main.go:L9`, `L38`)
   - **What this is:** The usage line for `eval` shows `[-parallel 3]`.
   - **Problem:** The code sets the default to 1, and the commit message says "one at a time by default". The text implies 3 is the default.
   - **Fix:** Write `[-parallel 1]`.
   - **If we skip it:** Someone reads it as the default and runs three agents at once on a subscription that may hit its limit.

Verdict: fix 1, 2 and 3 first. Then fix 4 and 5.

Not checked: I could not run anything, so I did not run the tests or `go fix -diff`. I assumed the `"uuid"` import compiles because the earlier eval batch ran. I did not confirm that `claude-token` is inside the repo folder.
