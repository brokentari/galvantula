# Review: phase-0

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: start..264a3dd. Date: 2026-11-06. Cost: $0.92.

What this change does: It is the Phase 0 code plus three rounds of fixes. The code is a graph runtime with an event log, a fake executor, an HTTP/SSE server, a React UI, and the `evalgen` and `phasereview` tools that call headless Claude Code. This round reworks `phasereview`'s clean-tree check, adds a Host check and a 500 status to the server, adds a 30-second limit per product run in `evalkit`, and makes `evalgen` restore the old tests if the new ones fail. I assumed one developer on localhost running a handful of runs at a time.

I found no bug or security hole in the runner, store, server or event fold. The Host check, the content-type check and the single-event plan success all hold up. I checked the `[::1]` Host form, the `0.0.0.0` Host, and the resume paths in `drive`.

## Should fix

1. **`phasereview` has no test, and its clean-tree rule has broken twice** (`cmd/phasereview/main.go:L74-80`, `L118-130`)
   - **What this is:** Before a phase is called done, this command checks that the tree is clean, builds the diff, and reads the reviewer's `Verdict:` line. It exits 1 unless the verdict is exactly `Ship.`.
   - **Problem:** Two review rounds in a row fixed this same clean-tree check, and no test pins it. A wrong pathspec (the git rule for which files count) again blocks the documented fix-and-rerun loop. Verdict parsing also has no test. The verdict check fails closed, so `**Verdict: Ship.**` counts as "no verdict line". There is one wrong message as well. When `git status` itself fails, the returned error still says "commit your changes first".
   - **Fix:** Pull the verdict loop into `verdict(text string) string`. Add one table test with `Verdict: Ship.`, `Verdict: fix 1 first.` and no line. Add one test that uses `t.Chdir` into a temp git repo and checks that a changed `docs/reviews/x.md` leaves the tree clean while a changed `a.go` does not. Return the "commit your changes first" text only when `dirty != ""`.
   - **If we skip it:** The next pathspec slip shows up only when someone runs the real review, and each run costs money.

## Nice to have

2. **`countTests` can silently undercount** (`cmd/evalgen/main.go:L252-272`)
   - **What this is:** It reads `go test -json` output line by line with `bufio.Scanner` and a 1 MB limit.
   - **Problem:** If one line is longer than 1 MB, `Scan` stops and `sc.Err()` is never read. The counts then drop with no message. The reference check would report "0 tests pass", which hides the real cause.
   - **Fix:** Use `for line := range strings.Lines(out)` and delete the `Scanner` and `Buffer` lines. That is shorter and has no limit.
   - **If we skip it:** An eval with very long failure output gives a confusing count error.

3. **The 30-second limit can hang if the product starts a child process** (`evals/evalkit/evalkit.go:L69-80`)
   - **What this is:** `exec.CommandContext` kills the product after 30 seconds so one hung product fails one test.
   - **Problem:** Stdout and stderr are `bytes.Buffer`, so `Run` waits for the pipes to close. A child process that inherited the pipes keeps `Run` blocked after the kill. The timeout then fails to protect the package.
   - **Fix:** Add `cmd.WaitDelay = 5 * time.Second` before `cmd.Run()`. One line.
   - **If we skip it:** A product that starts a background process can still hang the whole eval package.

4. **`describeChange` runs a git command that is known to fail** (`cmd/phasereview/main.go:L148-153`)
   - **What this is:** It builds the commit log for the reviewer.
   - **Problem:** With the default base (the empty-tree id), it first runs `git log <tree>..HEAD`, which cannot work. It then overwrites that error with a second call.
   - **Fix:** Pick the range first: `rng := "HEAD"`, and `if base != emptyTree { rng = base + "..HEAD" }`. Then make one `git log` call.
   - **If we skip it:** Nothing breaks. It is two wasted lines.

## Prose

5. **A heading personifies rules** (`Software Factory Plan.md:51`, `Software Factory Plan.html:563`)
   - **What this is:** The heading "Rules that keep parallel work honest" is above the write-scope, frozen-test and `go.mod` rules.
   - **Problem:** Work cannot be honest, so this is a figure of speech, and Orwell rule 1 forbids it. The last commit already replaced four others like it.
   - **Fix:** Use "Rules for parallel work" in both files. In `evals/README.md`, replace "evalgen does not trust the model's report" with "evalgen checks the model's work itself".
   - **If we skip it:** A reader has to work out what "honest" means here. The next phase review will report it again.

Verdict: fix 1 and 5 first.
Lean: -5 lines possible.
Not checked: I could not run anything, so I did not run the tests, `go fix -diff`, or the `claude` flags (`--safe-mode`, `--permission-prompts`, `--effort`). The Go json v2 behavior is from reading the code only. `evalgen` and `phasereview` run model-written code and commands on this machine by design, and I did not review that risk.
