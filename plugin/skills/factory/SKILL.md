---
name: factory
description: Run the software factory. Use when the user wants to build a Go program from a spec with the factory, check on a factory run, or approve or reject its contract or task graph.
---

# Run the factory

The factory builds a Go program from a spec. It stops twice and waits for the user:
the **contract** (`approve-contract`) and the **task graph** (`approve-tasks`).
You run it with the `factory_*` tools. The server must be running: `factory serve`,
with `CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY` set. If a tool says the server
is not reachable, tell the user to start it. Do not start it yourself.

## Start a run

1. Get the spec from the user: what the program must do, in a paragraph or two.
   Name the packages if the user wants a certain split.
2. Call `factory_start` with the spec. Leave out `graph` (it defaults to `phase4`).
   Set `auto_approve` only if the user says to skip the gates.
3. Tell the user the run id.

## Follow a run

Call `factory_status`. It shows each node's state, the cost and any gate that waits.
Calling it once a minute or so is enough: a run takes minutes. Between calls,
do nothing else for the run. If a node fails or a run stops, call `factory_logs`
with the node id and tell the user what you see.

## At a gate

`factory_status` prints `WAITING FOR A DECISION` with the gate's node id and what the
user must read: the contract at `approve-contract`, the task list and cost ceiling at
`approve-tasks`.

1. Show the user that text. Do not decide for them, and do not summarize it away.
2. Ask: approve, or reject with notes?
3. Call `factory_approve` with their answer. To send the contract back to the architect,
   reject it and pass their notes. To comment on a line of the contract, name the line
   number and quote the line in the notes. A rejection without notes ends the run. The run allows
   a few revisions, and `factory_status` shows how many it has used.
   Only the contract can go back. Rejecting the task graph ends the run.
4. Go back to following the run.

## When it ends

`factory_status` shows `succeeded` or `failed`, and the reason. The product is in
`runs/<run>/repo` on the server's machine. If a task failed, the factory may have
replanned it: the status shows the replan count. Tell the user what happened in
two or three sentences.
