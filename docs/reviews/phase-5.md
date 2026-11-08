# Review: phase-5

Reviewer: claude-sonnet-5-5 at medium effort, in a new session. Range: 03684de..ad0d946. Date: 2026-11-07. Cost: $0.17.

What this change does: It adds an MCP server (`factory mcp`) and a Claude Code plugin. The server calls the factory's HTTP API, which gains two read endpoints, `/state` and `/logs`. It also lets a person reject the contract gate with notes. The runtime then rewinds main to the standards commit and reruns the architect with those notes, up to `budget.revisions`. The planted eval plan is replaced with one that always fails, and the attempts count is now taken from events.

I found no Must fix items.

I checked the points most likely to break:
- **Revision on the task gate:** `approve-tasks` waits on `plan`, which is `kind: plan` and not an agent. `revisable` therefore refuses it, so a rejection there ends the run. A revision cannot rewind main past the contract.
- **Revision state:** `Revised` is cleared on `NodeStarted`. `Attempt` and cost survive the dependency reset. `Task.Revision` reaches `claude.go`, where it rewinds main and puts the rejected contract in the prompt.
- **MCP graph name:** the `graphName` regex stops path traversal in `factory_start`. Tool errors come back to the model as `isError` results.
- **Logs endpoint:** `tail` is bounded from 1 to 1000. The run id is path-escaped.
- **Test coverage:** the new logic is covered by tests for revision, rejection limits, `Rewind`, the snapshot endpoints and the MCP protocol.

Verdict: Ship.

Not checked: I did not run the tests. I did not check the UI reducer change in `ui/src/state.ts` against a live run.
