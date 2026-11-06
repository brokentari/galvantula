# Review: phase-1

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: 6189377..9f4a262. Date: 2026-11-06. Cost: $0.65.

What this change does: Phase 1 adds a real agent node. Claude Code runs in a bubblewrap sandbox in its own git worktree. The runtime checks the agent's work with gofmt, vet, `go fix -diff`, build and tests, then commits and merges it. The change also adds budgets, retry and backoff rules, a usage-limit stop, `factory eval` (runs evals and scores them with hidden tests), and cost display in the UI. I assumed one person on one machine, with a few runs at a time. That matches the README.

I traced the run flow, the retry and resume rules, the budget stop, the sandbox, the git handling and the event encoding. I found no bug and no security hole that blocks shipping. The server rejects wrong content types and non-local Host headers. Node IDs are validated, so they cannot be path tricks. Invalid UTF-8 is replaced, not dropped. `ui/src/gen/events.ts` is up to date, and a test guards it.

**Should fix**

1. **No test that `factory eval` stops after a usage limit** (`cmd/factory/eval.go:L101-111`)
   - **What this is:** After one run hits the account's usage limit, `limited` is set. Later runs skip themselves, so the eval buys no more usage.
   - **Problem:** The commit message says this must never happen, but no test fails if the check is removed. The runner's limit test covers only the single-run side. A later edit could make the eval keep starting paid runs.
   - **Fix:** Add one test like `TestLostRunsCountAsFailures`. Start a fake `httptest` server that creates a run, and write a limit-class event for it into a temp store. Run `runEvals` with `-n 3` and check that only one run starts.
   - **If we skip it:** A refactor can silently remove the guard. The cost shows up on the bill.

4. **An error string holds two sentences** (`cmd/factory/eval.go:L172`)
   - **What this is:** The error says a run logged nothing for too long.
   - **Problem:** `run %s logged nothing for %s. It may still be running and spending: check the server` has a capital "It" after a period, and it is hard to scan. Go error strings should be one lower-case clause.
   - **Fix:** `run %s logged nothing for %s; it may still be running and spending, so check the server`.
   - **If we skip it:** The text stays correct but breaks the Go convention and the Orwell rule against stacked clauses. The next phase review will report it again.

5. **README says the sandbox "shows only `/usr`"** (`README.md`, "Run a real agent")
   - **What this is:** The README tells readers what the sandbox contains, so they know what an agent can use.
   - **Problem:** `internal/sandbox/sandbox.go` also mounts `/etc` read-only, and the agent's own folders. The sentence is not true as written. A reader may think `/etc/ssl` or `/etc/passwd` is hidden.
   - **Fix:** Write "shows only `/usr`, `/etc` and its own folders, and finds commands only in `/usr/bin`".
   - **If we skip it:** The README claims more isolation than the code gives.

**Nice to have**

2. **Log lines can be cut in the middle of a character** (`internal/claude/claude.go:L205-210`)
   - **What this is:** `short` cuts each log line to 200 bytes so the UI log stays small.
   - **Problem:** `s[:200]` counts bytes. If an agent's text has "—" or "é" at byte 199, the cut leaves half a character. `event.New` repairs it into a `�`, so the log line shows a stray `�` before "…".
   - **Fix:** Cut by rune: `r := []rune(s); if len(r) > 200 { return string(r[:200]) + "…" }`. Check the length in runes too.
   - **If we skip it:** Only a cosmetic glitch in the UI log.

3. **`eval` can spend money, then fail to score, when run outside the repo root** (`cmd/factory/eval.go:L227-233`)
   - **What this is:** `hiddenTests` mounts `go.mod`, `go.sum` and `evals/evalkit` from the current directory.
   - **Problem:** An absolute eval folder is accepted. If you run `factory eval /abs/evals/wordfreq` from another folder, the spec is read and the run is paid for. Scoring then fails because the mounts are missing, and the run counts as failed with "no hidden test ran". The product stays on disk, and nothing can rescore it.
   - **Fix:** At the start of `runEvals`, stat `go.mod` and `evals/evalkit` in the working directory. Return "run factory eval from the repo root" if either is missing.
   - **If we skip it:** One wrong working directory wastes a whole batch.

**Prose:** findings 4 and 5 above. The commit messages, doc comments and guardrails comments read well.

Verdict: fix 1, 4 and 5 first.
Lean: no lean findings.
Not checked: I did not run the tests or a real agent. The plan's "median cost you set" criterion for phase 1 has no recorded limit. The results show about $0.10 to $0.20 a run, so you may want to record a number before you call the phase done.
