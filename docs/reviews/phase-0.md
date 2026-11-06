# Review: phase-0

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: start..bd11382. Date: 2026-11-06. Cost: $0.80.

**What this change does:** It is the phase-0 skeleton of the software factory. A Go runtime runs a graph of nodes, using a SQLite event log, a fake executor, an HTTP/SSE server and a React UI. It also adds the shared standards block in `AGENTS.md`, `evalgen` (a model writes the hidden eval tests and the tool checks them) and `phasereview` (a new session reviews each phase). I assumed the expected load is one person on one machine, running `factory serve` locally. That is what the README describes.

I could not run commands. I read the code and traced the flows.

## Must fix

None.

## Should fix

1. **A second `factory serve` on the same database resumes the runs of the first** (`cmd/factory/main.go`, `serve`)
   - **What this is:** At start, `serve` opens the log and calls `run.ResumeAll()`. Only after that does it call `srv.ListenAndServe()`.
   - **Problem:** Say a server is already running and you start another one by mistake, or start it again after a Ctrl-C that has not finished. The second process finds the port busy only after it has resumed every unfinished run. For each node still marked running, it writes a "runtime restarted" infra failure and starts the node again. The first server is still driving the same nodes. Both now write to one log, and the node counts are wrong. The second process then exits with "address in use".
   - **Fix:** Bind the port first with `ln, err := net.Listen("tcp", *addr)`. Call `ResumeAll()` after it, and serve with `srv.Serve(ln)`. This covers the README flow. It does not cover two servers on different ports with one database. A lock file or an SQLite lock row would be needed for that, so leave it for later.
   - **If we skip it:** One slip with the terminal leaves a run with doubled attempts and false failure counts. You cannot repair this, because the log is the only state.

## Nice to have

2. **`EVAL_PRODUCT` in your shell changes evalgen's checks** (`cmd/evalgen/main.go`, `check` and the `runTests` closure in `generate`)
   - **What this is:** evalgen runs the hidden tests against the reference. Then it runs them against a do-nothing stub, passing `EVAL_PRODUCT=<stub>`. `run` starts from `os.Environ()`.
   - **Problem:** If `EVAL_PRODUCT` is already set in your shell (the README tells you to use it for real products), the "passes on the reference" check and the final in-place run test that product instead.
   - **Fix:** Pass `[]string{"EVAL_PRODUCT="}` for those runs. `evalkit.Build` uses `cmp.Or`, so an empty value falls back to the reference.
   - **If we skip it:** evalgen can reject good tests, or keep bad ones, and the message will not say why.

3. **A failed first move in `install` can delete the old tests** (`cmd/evalgen/main.go`, `install`)
   - **What this is:** `install` moves the old files into `stage/old` and defers `os.RemoveAll(stage)`.
   - **Problem:** If `swap(dir, old)` fails after it moved `hidden` but before it moved `reference` (for example, a permission error), `install` returns at once. The deferred `RemoveAll` then deletes the moved files. The later failure paths do restore them.
   - **Fix:** `return errors.Join(err, swap(old, dir))`, the same as the next branch.
   - **If we skip it:** You lose hidden tests in a rare failure. They are in git, so you can recover them.

4. **`claude.Run` loses the error chain** (`internal/claude/claude.go`)
   - **What this is:** When the JSON result does not parse, it returns `fmt.Errorf("claude -p: %v; ...", errors.Join(runErr, err), ...)`.
   - **Problem:** `%v` turns the error into text. Callers cannot use `errors.Is` to see `context.Canceled` or an `exec.ExitError`.
   - **Fix:** Use `%w` for the joined error.
   - **If we skip it:** Nothing breaks today. It will hurt once the executor needs to tell a cancel from a crash.

## Prose

5. **"DAG" and "fan-out" are never explained** (`Software Factory Plan.md`, "Ground rules"; `graphs/factory.yaml` node `approve-dag`)
   - **What this is:** The plan uses "task DAG" from its first section on. The UI shows the gate title "Approve the DAG and its cost".
   - **Problem:** "DAG" is jargon that is never defined (Orwell rule 5). "Fan-out" is the same. The README says "graph" and "run at the same time".
   - **Fix:** At the first use, write "task graph (a graph with no cycles)". Change the gate title to "Approve the task graph and its cost". Replace "fan-out" with "run at the same time".
   - **If we skip it:** A new reader has to guess what the term means.

The Go code follows the guidelines I could check. I found no missed ones, and I saw no `go fix` issues in the diff. The tests I looked at cover the risky new logic. These are the Host check, the content-type and body limits, the install and restore, the clean-tree check, the verdict parser, and resume after a plan node.

Verdict: fix 1 and 5 first.

Not checked: I could not run the checks or `claude`. I did not verify the `claude` flags (`--safe-mode`, `--permission-prompts`) or the npm versions. Risk to know: evalgen lets the model run `go test` and `go run` in your account, with your environment and network and no sandbox. This includes `ANTHROPIC_API_KEY` if you have it set. Use a key with a low spend limit.
