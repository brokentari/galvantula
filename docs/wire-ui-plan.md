# Plan: the Wire look, and a gate you can decide at

## The problem

At a gate the UI shows a node id, a title, a notes box and two buttons. It
does not show the work you approve, what Approve starts, or what Reject does.
The server already builds the text a person must read (`GateSnapshot.Context`
in `internal/server/snapshot.go`), but only the Claude Code plugin shows it.

The rest of the UI is bare: no time, no budget, no sign in a background tab
that a gate waits.

## Decisions

- The look is "Wire", mockup B on the design canvas: dark, one monospace
  font, 1px lines, 2px corners, no shadows. Yellow means it is your turn and
  nothing else. Blue means running.
- One theme. The light theme goes.
- Copy follows the clean-copy rules: a string stays only if the user cannot
  act without it. A hint states a consequence where the user chooses. An
  error says what to do next. A bare number gets its unit.
- The runtime says what a rejection will do. The UI never works it out.
- The form keeps its field order and its API call.

## What a gate shows, top to bottom

1. **The work.** A section named for its source, such as "From architect",
   holds the lines of `GateSnapshot.Context` in a box that scrolls. Each
   source id is a button that opens that node.
2. **Notes**, with its label above it. The size check on blur stays.
3. **What each button does**, one line each, above the buttons:
   - `Approve starts plan.` The names are the nodes that depend on the gate.
   - With revisions left and notes: `Reject sends this back to architect with your notes. 2 revisions left.`
   - With revisions left and no notes: `Reject ends the run. Add notes to send this back to architect.`
   - With no revisions left: `Reject ends the run.`
   - For an old log that does not say: `Reject without notes ends the run.`
4. **Approve** and **Reject**.

A gate that is decided shows its decision in place of the form: `Approved`,
`Sent back` or `Rejected`, the time, and the notes.

A gate has no attempt, cost or retries, so its panel leaves those rows out.
The heading "This gate waits for you" goes: the header chip says it.

The side panel is wider while it shows a gate that waits, so the work is
readable. The graph follows the nodes that matter at a size you can read:
the nodes you opened, else the ones that run or wait, else the whole graph.
The Fit button still shows the whole graph.

## Contracts between the workstreams

Each workstream builds against these names. Do not change one without
telling the coordinator.

### Event: `gate_waiting` gets data

```go
// GateWaitingData says what a rejection will do, so no reader repeats the runtime's rules.
type GateWaitingData struct {
	// RevisionsLeft is how many more times a rejection with notes sends the
	// work back to the nodes the gate waits for. At 0, a rejection ends the run.
	RevisionsLeft int `json:"revisions_left"`
}
```

One function in `internal/runner` gives this number, and `decide` uses the
same function, so the event and the decision cannot disagree. An old log has
`gate_waiting` events with no data; `State.Apply` and the UI must accept them.

### UI state (`ui/src/state.ts`)

```ts
interface NodeView {
  // ...the fields it has now
  startedAt?: string;      // `at` of the last node_started
  endedAt?: string;        // `at` of the last node_succeeded, node_failed or gate_decided
  revisionsLeft?: number;  // from gate_waiting; undefined for an old log
  decision?: GateDecidedData & { at: string }; // from gate_decided; cleared by gate_waiting
}
interface RunView {
  // ...the fields it has now
  startedAt: string;       // `at` of run_created
  endedAt?: string;        // `at` of run_finished
  budgetUSD?: number;      // run_created guardrails.budget.run_usd, if it is more than 0
}
```

These are copies of event fields. `state.ts` stays a mapping with no rules.

### Shared helpers (`ui/src/time.ts`)

```ts
export function useNow(active: boolean): number; // Date.now(), again each second while active
export function clock(ms: number): string;       // "0:42", "12:05", "1:02:03"
```

### Generated types

`cmd/gen-ts` also writes `Snapshot`, `NodeSnapshot` and `GateSnapshot` from
`internal/server`. The UI reads a gate's work from `GET /api/runs/{run}/state`.

### CSS tokens (`ui/src/styles.css`, on `:root`)

| Token | Value | Use |
|---|---|---|
| `--bg` | `#0e1116` | page |
| `--surface` | `#151a21` | inputs, log boxes |
| `--line` | `#2a323d` | rules between regions |
| `--line-strong` | `#4a5563` | borders of controls, pending nodes |
| `--ink` | `#e6edf3` | text |
| `--muted` | `#9aa7b4` | labels, secondary text |
| `--you` | `#f5d547` | your turn: the waiting gate, its chip, Approve, focus |
| `--you-ink` | `#0e1116` | text on a `--you` fill |
| `--run` | `#6cb6ff` | running, live |
| `--ok` | `#5fd08f` | succeeded |
| `--bad` | `#ff8a80` | failed, errors, Reject |
| `--retry` | `#f0a640` | a failure that will retry |
| `--font` | `"JetBrains Mono", ui-monospace, "SF Mono", Menlo, monospace` | everything |

Sizes: 13px text, 12px small text, 11px card meta. Corners 2px. Lines 1px.
The only fills are `--you` (your turn) and `--surface`. Focus is
`2px solid var(--you)` with a 2px offset. Motion stops under
`prefers-reduced-motion: reduce`.

The old tokens `--accent`, `--gate` and every `-soft` token go.

### Props

- `NodePanel` gets `onSelect: (id: string) => void`. `App` passes `setSelected`.
- `GraphView` gets `now?: number`, the time in milliseconds that a running
  card measures against. `App` passes the time of the last event shown while
  it replays, and nothing in live mode, where the card uses the clock.
- `GraphView` works out the nodes under review from `selected` and the graph.

## Workstreams

Each one owns its files. Put new rules in the CSS file named for you. In
phase 2, change `styles.css` only to take out rules you move, and read the
file again before each edit.

### Phase 1, two at once

**A. Runtime and state** owns `internal/**`, `cmd/**`, `ui/src/gen/**`,
`ui/src/state.ts`, `ui/src/time.ts`.

- Add `GateWaitingData` and emit it. Share the rule with `decide`.
- Test: the number is the budget at a gate after an agent, 0 at a gate after
  a plan or tool node, and one less after a send-back.
- Add the snapshot types to `cmd/gen-ts`. Run `go generate ./...`.
- Add the fields above to `state.ts`. Write `time.ts`.

**B. Wire theme** owns `ui/src/styles.css`, `ui/src/main.tsx`,
`ui/package.json`, `ui/package-lock.json`.

- Replace the tokens. Take out the light theme. Restyle every rule that
  exists now to the Wire rules above. Change no TSX markup.
- Bundle JetBrains Mono (400 and 700) with `@fontsource/jetbrains-mono`, at an
  exact version that is at least two weeks old. The server must work offline.

### Phase 2, three at once

**C. Gate review** owns `ui/src/NodePanel.tsx`, new `ui/src/GateReview.tsx`,
new `ui/src/gate.css`.

- Build "What a gate shows" above. Move the form out of `NodePanel`.
- Load the work from `/api/runs/{run}/state` when the gate starts to wait and
  when it waits again after a send-back. While it loads: `Loading…`. If it
  fails: `Could not load this. Open architect to read its log.` If it is
  empty: `architect logged nothing. Open it before you decide.`
- Widen the panel with `.body:has(.gate-work)` in `gate.css`.
- Keep: label above the field, validation on blur, the loading, disabled,
  focus and error states, the field order and the API call.

**D. Run chrome** owns `ui/src/App.tsx`, `ui/index.html`, new
`ui/src/RunStats.tsx`, new `ui/src/Timeline.tsx`, new `ui/src/chrome.css`.

- Header: elapsed time, nodes done (`5 of 8`), and cost against the budget
  (`$0.42 of $5.00`) with a 1px bar.
- Tab title and icon: `(1) approve-contract waits · Galvantula`. The icon is
  yellow when a gate waits, blue when the run runs, green or red at the end.
- When a gate starts to wait in live mode, select it if no node is selected
  or the selected node is a gate that is decided.
- Timeline: marks above the slider where a gate waited (yellow), a node
  failed (red) and the graph changed (ink).
- Events list: the time since the run started on each row.
- Empty state: `No runs yet. Start one:` with the command and a Copy button.
- Pass `onSelect` to `NodePanel`.

**E. Graph** owns `ui/src/GraphView.tsx`, new `ui/src/graph.css`.

- Under review: when the selected node is a waiting gate, outline the nodes
  it waits for in yellow, draw their edges to the gate yellow, and dim the rest.
- A running card shows its time (`agent · running · 0:42`). A finished card
  shows how long it took, and its cost if it is more than 0.
- A status mark with its own shape for each status, so color is not the only sign.
- A short flash of the border when a card changes status.
- A minimap when the graph has more than 12 nodes.

### Phase 3, the coordinator

Run every check in `AGENTS.md`. Drive the UI in a browser against a fake run:
approve, send back, reject, replay. Review with ponytail-review and against
the copy rules. Fix what the review finds.

## Not in this plan

Rendered markdown for the contract, a diff between two versions of a contract,
browser notifications, keyboard shortcuts, a panel you can drag wider, a
confirm dialog, a light theme.

## Risks

- `gate_waiting` changes the event log. An old log must still load, and the
  generated-file test must pass.
- Three agents change the UI at once in phase 2. A type check can fail on a
  file that another agent is in the middle of. Each agent reports that and
  does not fix a file it does not own.
