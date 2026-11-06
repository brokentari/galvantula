# Review: phase-0

Reviewer: claude-sonnet-5-5 at high effort, in a new session. Range: start..ee32346. Date: 2026-11-06. Cost: $0.78.

**What this change does:** This is the finished phase 0 of the software factory. A Go runtime runs a graph of work from an event log, with a fake executor and a React UI. Support tools sit around it: `evalgen` has a model write hidden eval tests, `phasereview` has a model review the code, and `claude.Run` runs headless Claude Code. The last six commits fix earlier review findings, such as the Host check, the JSON-only bodies, binding the port before resuming runs, and cancel handling in `claude.Run`.

I assumed one developer on localhost, a few runs at a time, and the fake executor only.

## Must fix

None.

## Should fix

1. **The "keep the old tests" step can delete them** (`cmd/evalgen/main.go`, `install` and `swap`)
   - **What this is:** `install` moves the old `hidden`, `reference` and `assumptions.md` into a side folder. It moves the new ones in, runs the tests, and moves the old ones back if anything fails.
   - **Problem:** The restore path is `swap(old, dir)`. For each part it first runs `RemoveAll(dir/part)`, and only then renames `old/part`. Suppose the first `swap(dir, old)` fails after moving only `hidden`, for example because `reference` cannot be renamed. The restore moves `hidden` back. Then it deletes the `reference` that never moved, and `assumptions.md` too. The `ErrNotExist` from the missing source is ignored, so the loss is silent.
   - **Fix:** In the first error path, move back only the parts that exist in `old/`. Check that `old/part` exists before `RemoveAll(dir/part)`. Keep the current behavior for the later error paths, which must remove the new files. Add a test where the first move fails halfway.
   - **If we skip it:** The failure is rare, and git has the committed files. But the step that promises to keep the old tests can delete them on the failure it was built for.

2. **Prose: the plan says "five kinds of node", but the code has six** (`Software Factory Plan.md`, "Node kinds"; the same text in the `.html`; `internal/graph/graph.go:L18-27`)
   - **What this is:** The plan says "The runtime knows five kinds of node... The factory graph is made from these and nothing else." Its table lists agent, tool, gate, merge and plan.
   - **Problem:** `graph.Kinds` also holds `slot`. `graphs/factory.yaml` uses it for `tasks`. A reader who follows the plan will not know what `kind: slot` is.
   - **Fix:** Say "six kinds". Add a `slot` row to the table: "placeholder in the graph that a plan node's output replaces". Change both the `.md` and the `.html`.
   - **If we skip it:** The plan and the code disagree about the core vocabulary.

3. **Prose: "DAG" is still in the code, the node id and the UI** (`internal/graph/graph.go:L35`, `graphs/factory.yaml`, `internal/runner/runner_test.go`)
   - **What this is:** Commit 6 set one term, "task graph". The fifth fix said to explain "DAG" at first use.
   - **Problem:** The comment "Deps make it a DAG." has no explanation. The node id `approve-dag` shows on the UI card and in the event list. The runner test says "the DAG gate waits". The node's title already says "task graph".
   - **Fix:** Rename the id to `approve-tasks`. Update the `Decide` calls and wait messages in `runner_test.go`. Change the comment to "Deps order the nodes; a cycle is an error."
   - **If we skip it:** The same thing has two names, and the UI shows the jargon one.

## Nice to have

4. **Every log line rebuilds the whole graph view** (`ui/src/App.tsx`, `useEvents`; `ui/src/GraphView.tsx`)
   - **What this is:** Each new event creates a new `run` object and a new events array.
   - **Problem:** `GraphView` recomputes the layout and every node for each event, including log lines that change only one node's text. `[...s.events, e]` also copies the whole array each time. With a real executor and thousands of `go test` lines, this becomes slow.
   - **Fix:** Memoize the layout on `run.graph` and `run.graphVersion` only. Pass node views to the cards separately from the layout.
   - **If we skip it:** Phase 0's fake run is unaffected. A long real run will make the UI sluggish.

**Verdict: fix 1, 2 and 3 first.**

**Not checked:** I did not run any command or test. I did not verify the `claude` flags `--permission-prompts` and `--safe-mode`. I only skimmed the model-written eval tests. `evalgen` lets the model run `go test` and `go run` on the host with no sandbox. That is a known gap in the plan, and phase 1's sandbox covers it.
