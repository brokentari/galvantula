# Review: phase-0

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: start..138650e. Date: 2026-11-06. Cost: $0.99.

What this change does: this is the whole phase 0 build of the factory: a graph runtime driven by an event log, a fake executor, an HTTP/SSE server, and a React UI. It also adds the standards tooling, `evalgen` for model-written eval tests, and `phasereview`. The last seven commits fix findings from earlier reviews. I assumed one developer on one machine using `localhost`, which is what the README says.

The earlier fixes hold up. I traced these paths and found no bug:
- the runner loop, including resume and the worker limit;
- `Fill`;
- the store and its subscribers;
- `install` and `putBack`;
- the SSE dedupe;
- the Host and Content-Type checks.

## Should fix

1. **The cycle error names nodes that are not on the cycle** (`internal/graph/graph.go`, `checkAcyclic`)
   - **What this is:** The check removes nodes with no unmet deps until none are left. The comment says "Whatever cannot be removed is on a cycle", and the error says `graph has a cycle through: …`.
   - **Problem:** A node that only depends on a cycle node also cannot be removed. Take `a→c→b→a` plus `e` depending on `c`. The error says "cycle through: a, b, c, e", but `e` is not on the cycle. Phase 3 plans to send planner mistakes back to the planner, and this message would mislead it. The test uses a free node `d`, so it never covers this case.
   - **Fix:** Change the message and comment to "graph has a cycle, or depends on one: …". Add one test row with a node downstream of the cycle.
   - **If we skip it:** The error points at a healthy node, and whoever reads it looks in the wrong place.

2. **`claude.Run` has no test for its result handling** (`internal/claude/claude.go`, `internal/claude/claude_test.go`)
   - **What this is:** `evalgen` and `phasereview` both depend on `Run`. It builds the args, picks `--bare` or `--safe-mode`, parses the JSON result, and turns `is_error` into an error.
   - **Problem:** Only the cancel path has a test. If claude prints non-JSON, exits non-zero, or returns `is_error:true`, the error text is not tested. Neither is the choice between `--bare` and `--safe-mode`. A change there breaks both commands.
   - **Fix:** Reuse the fake-`claude`-on-PATH setup from `TestRunReportsCancel`. Have the script print `{"is_error":true,"subtype":"x","result":"boom"}`, and check that `Run` returns an error containing `boom`. Add a second case that echoes its args and checks for `--safe-mode` when `ANTHROPIC_API_KEY` is unset.
   - **If we skip it:** A silent change to the failure path can pass as a successful review or eval run.

## Nice to have

3. **`evalgen` fails only after spending money when given an absolute path** (`cmd/evalgen/main.go`, `generate` and `runTests`)
   - **What this is:** The in-place test step builds `"./" + dir + "/hidden/"` and runs it from `.`.
   - **Problem:** With `/home/saul/.../evals/csvq` the pattern becomes `.//home/…`, and `go test` fails. This happens after the model has run, which costs about $1–4. The install rolls back safely, but the money is gone.
   - **Fix:** At the start of `generate`, reject a `dir` that is absolute or does not sit under the module root. Or use `filepath.Rel` from the working directory.
   - **If we skip it:** An easy typo costs a model run.

4. **`evalgen` runs model-written code with your API key in its environment** (`cmd/evalgen/main.go`, `run`)
   - **What this is:** `check` runs `go vet` and `go test` on test code the model just wrote, with `os.Environ()`. That includes `ANTHROPIC_API_KEY`.
   - **Problem:** The plan's "Known gaps" lists this risk for factory workers, but `evalgen` has no note on it. The model's own Bash session also holds the key, so stripping it from `run` only covers the check step.
   - **Fix:** At least write the risk in the `evalgen` doc comment and in `evals/README.md`. Optionally drop `ANTHROPIC_API_KEY` in `run`.
   - **If we skip it:** The risk stays unwritten. It is low for a local tool, but it is real.

5. **Two copies of the plan must change together** (`Software Factory Plan.md`, `Software Factory Plan.html`)
   - **What this is:** The `.md` file repeats the `.html` text, and the review fixes edited both.
   - **Problem:** They have already drifted. The HTML says "Dashed edges have caps", while the MD says "Retry edges have caps". Each edit now has to be made twice.
   - **Fix:** Keep one file as the source. Either drop the MD and point the README at the HTML, or add a note saying which file is the source. The HTML has the diagrams.
   - **If we skip it:** The two files will disagree over time, and an agent that reads the MD may follow outdated text.

## Prose

None found. The terms are consistent ("task graph", "in parallel"), and the new prose is plain. I did not review `evals/*/assumptions.md` closely, because a model wrote those files.

Verdict: fix 1 and 2 first.
Lean: -264 lines possible (finding 5, if you drop the MD copy).
Not checked: `standards/vendor/` and `ui/package-lock.json` (both left out of the diff on purpose), the real `claude` flags such as `--permission-prompts` and `--safe-mode`, and the exact bytes of the no-break space in the wordfreq separator test.
