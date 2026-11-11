# Plan: a gate that says what to judge, line comments, and Crit

## The problem

At `approve-contract` the person does not know what to read. The gate shows
the last log lines of the architect (`GateSnapshot.Context`). In a real run
those lines are `CONTRACT.md`, indented. In the fake demo they are activity
lines, so the gate reads like a to-do list. In both, nothing says what to
judge or what Approve commits.

The person must judge three things:

1. The contract's section on unclear spec. Each item is a choice the
   architect made where `spec.md` does not say. This is the one place where
   the person adds what the machine lacks.
2. The tests. Builders may not change a test, so a wrong test fails the run.
3. The red check: how many tests fail on the stubs.

A rejection takes one notes box. The person cannot point at a line, and
cannot see what the architect changed in the next round.

## The three steps

1. **Clarity.** The runtime writes a gate brief: what to judge, the work, and
   what Approve does. The UI and the MCP server show it.
2. **Line comments.** The person comments on lines of the rendered contract.
   The runtime stores the comments in `gate_decided` and gives them to the
   architect with the lines they quote. The next round shows what changed.
3. **Open in Crit.** If `crit` is installed, the person can review the
   contract in Crit and bring its comments into the same list.

## Decisions

Decisions marked **Owner** need the owner's yes. Each has a recommendation,
and the plan builds the recommendation, so none blocks the work.

- The gate brief goes in the `gate_waiting` event. Reason: the log is the
  only state, so a reader after a restart, a replay and the MCP server all
  see the same brief, and none repeats a rule.
- The runner builds the brief from the folded state, with one function.
  Reason: the same pattern as `revisionsLeft`, and a restart gives the same
  brief.
- The architect's contract goes in its `node_succeeded` event. Reason: the
  runner must find it in the state after a restart. The log holds the
  contract twice for each round, at most 64 KB each time.
- The task table needs no executor change. The runner builds it from the
  graph and the guardrails, which the state already holds.
- The brief holds sentences, not codes. Reason: the UI and `factory_status`
  print the same words, and the UI needs no rule to choose them.
- A comment names lines of the contract's markdown source. The runtime sets
  the quote from its own copy. Reason: a client cannot send a quote that
  does not match, and validation is one range check.
- The runtime works out the round-to-round changes and where each earlier
  comment is now. Reason: this is non-trivial logic, the UI has no test
  runner, and Go tests cover it. The MCP server shows the same result.
- A draft (notes and comments not yet sent) lives in `localStorage`.
  Reason: the panel remounts when the person opens another node, and a
  reload must not delete what they typed. A draft becomes state only in
  `gate_decided`.
- A gate with no contract and no tasks in its brief shows what it shows now.
  Reason: one path serves old logs and gates after other nodes.
- **Owner: the UI renders markdown with its own small renderer, not a
  package.** Recommended. Ponytail's order puts a new dependency last, and
  the task is small for the subset `CONTRACT.md` uses. Each block must carry
  its source line for comments, which the renderer gives by construction.
  It builds React elements and never sets HTML, so text from an agent
  cannot run script. A parser mistake shows source text in a monospace UI,
  which is still readable. It adds about 2 kB to the bundle. The other
  choice is `react-markdown` 10.1.0 (published 2025-03-07) with `remark-gfm`
  4.0.1 (published 2025-02-10), both checked with `npm view` on 2026-11-11.
  It is safe by default if images are turned off, but it adds dozens of
  packages to the lockfile and tens of kB to the bundle (not measured).
- **Owner: the architect's brief names a fixed heading, `## Unclear in the
  spec`, as the first section of `CONTRACT.md`.** Recommended. The runtime
  finds the section by its heading. The check does not enforce the heading:
  a missing section costs no retry, and the gate says that the contract
  names nothing unclear. This changes the architect's prompt, so eval
  results can move.
- **Owner: the architect's log gets one summary line in place of the whole
  contract.** Recommended. The contract is in the event, so the log lines
  are a third copy.
- **Owner: Approve sends the draft comments too.** Recommended. The log
  keeps them, and the gate says that the architect does not see them.
- **Owner: build step 3.** Recommended. Crit's license is MIT. The adapter
  is one small package and three routes. It reads a documented file. It
  never shares. If `crit` is missing or fails, step 2 still does the whole
  job. The cost is drift: Crit moves fast (see "What was checked about
  Crit").
- Crit opens from a link that the person presses. Reason: Crit starts with
  `--no-open`, because its own home folder hides the person's browser
  settings, and a browser can block a tab that a script opens.
- `factory_approve` keeps plain notes. Reason: the plugin calls it that
  way, and a Claude Code user names the line in the notes.
- **Owner: commit the Wire UI work before phase 1.** The working tree holds
  it uncommitted. That commit is the base for this change and for the phase
  review.

## What a gate shows, top to bottom

### `approve-contract` (the brief has a contract)

1. **What to check.** The sentences of `brief.check`, as a list.
2. **Counts**, one line: `12 tests · 12 fail on the stubs`.
3. **The contract**, rendered. The section on unclear spec comes first, then
   the rest in source order. Each block has its line number at the left. The
   number is a button that opens a comment form under the block.
   From round 2: a **Show changes** button marks new lines and shows removed
   lines in place. Comments from the last round show under the lines they
   quote, or in a list above the contract if those lines are gone.
   If Crit is installed: **Open in Crit**, then **Use Crit comments**.
4. **Notes**, with its label above it.
5. **What each button does.** `Approve starts plan.` as now, then
   `brief.approve`, then the Reject line.
6. **Approve** and **Reject**.

### `approve-tasks` (the brief has tasks)

1. **What to check.**
2. **The task table**: Task, Owns, Depends on, Retries, Cost cap.
3. Notes, the lines for each button, and the buttons, as above.

### Any other gate, and any old log

What it shows now: the log lines from `/api/runs/{run}/state` under
"From architect", with the same loading, failed and empty lines.

### A decided gate

The decision, the time, the notes, and each comment with its line number
and quote.

## Contracts between the workstreams

Each workstream builds against these names. Do not change one without
telling the coordinator.

### Event types (`internal/event/event.go`)

```go
const (
	MaxContractBytes = 64 << 10 // the most of CONTRACT.md that an event holds
	MaxComments      = 100      // the most comments in one decision
	MaxCommentBytes  = 4000     // the longest comment body
	MaxQuoteBytes    = 2000     // the longest quote; the runtime cuts a longer one
)

// Contract is the architect's CONTRACT.md and the result of the red check.
type Contract struct {
	// Markdown is the file as the architect wrote it, with \n line ends.
	Markdown string `json:"markdown"`
	// Cut means the file was longer than MaxContractBytes and ends early.
	Cut bool `json:"cut,omitzero"`
	// Tests is how many tests ran on the stubs. Red is how many of them failed.
	Tests int `json:"tests"`
	Red   int `json:"red"`
	// Unclear is the section on unclear spec. It is zero if the contract has none.
	Unclear LineRange `json:"unclear,omitzero"`
}

// LineRange is a range of lines of the contract. Lines count from 1, and both ends are in the range.
type LineRange struct {
	Line    int `json:"line"`
	EndLine int `json:"end_line"`
}

// GateBrief is what a person must judge at a gate and what Approve does.
// The runtime writes it, so no reader repeats a rule.
type GateBrief struct {
	// Round counts the times this gate has waited, from 1.
	Round int `json:"round"`
	// Check says what to judge: one sentence for each item, the most important first.
	Check []string `json:"check"`
	// Approve says what Approve commits, beyond the nodes it starts. It can be empty.
	Approve string `json:"approve"`
	// Contract is set at a gate that waits for a node with a contract.
	Contract *Contract `json:"contract,omitempty"`
	// Changes compares Contract with the one the person sent back. It is set from round 2.
	Changes *ContractChanges `json:"changes,omitempty"`
	// Tasks is set at a gate that waits for a plan node.
	Tasks []TaskRow `json:"tasks,omitempty"`
}

type ContractChanges struct {
	// Added lists the lines of this contract that the last one did not have.
	Added []int `json:"added"`
	// Removed lists the lines of the last contract that this one does not have.
	Removed []RemovedLine `json:"removed"`
	// Earlier holds each comment the person sent back, and where its lines are now.
	Earlier []EarlierComment `json:"earlier"`
}

type RemovedLine struct {
	// After is the line of this contract that the removed line came after. 0 means before line 1.
	After int    `json:"after"`
	Text  string `json:"text"`
}

type EarlierComment struct {
	// Line is where the quoted lines start in this contract. 0 means they are
	// gone: the architect changed or removed them.
	Line    int    `json:"line"`
	WasLine int    `json:"was_line"`
	Quote   string `json:"quote"`
	Body    string `json:"body"`
}

type TaskRow struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	// Owns lists the package directories the task may write in.
	Owns []string `json:"owns"`
	// After lists the tasks it waits for. Empty means it starts at once.
	After   []string `json:"after"`
	Retries int      `json:"retries"`
	// CapUSD is the most the task may spend if it uses every retry. 0 means no limit.
	CapUSD float64 `json:"cap_usd"`
}

// GateComment is a person's comment on lines of the contract.
type GateComment struct {
	Line int `json:"line"`
	// EndLine is the last line. 0 means Line.
	EndLine int `json:"end_line,omitzero"`
	// Quote is those lines as the architect wrote them. The runtime sets it
	// and ignores a value in a request.
	Quote string `json:"quote,omitempty"`
	Body  string `json:"body"`
}
```

New fields on the payloads that exist:

```go
type NodeSucceededData struct {
	CostUSD  float64   `json:"cost_usd,omitzero"`
	Contract *Contract `json:"contract,omitempty"`
}
type GateWaitingData struct {
	RevisionsLeft int        `json:"revisions_left"`
	Brief         *GateBrief `json:"brief,omitempty"` // nil in a log from before briefs
}
type GateDecidedData struct {
	Approved bool   `json:"approved"`
	Notes    string `json:"notes,omitempty"`
	// Comments are in line order. A log from before comments has none.
	Comments []GateComment `json:"comments,omitempty"`
	Revise   bool          `json:"revise,omitempty"`
}
```

Functions in `internal/event`:

```go
// NewContract builds a Contract from the file's text. It changes \r\n to \n,
// cuts the text at the last line end at or before MaxContractBytes, and finds Unclear.
func NewContract(markdown string, tests, red int) Contract

// Lines returns the lines of Markdown. A final \n does not start a line.
func (c Contract) Lines() []string

// Feedback is the text the nodes before the gate read after a rejection.
func (d GateDecidedData) Feedback() string
```

**The line rule.** Line N is element N-1 of `Markdown` with one final `\n`
removed, split at `\n`. Go (`Lines`) and the UI use this one rule.

**The unclear rule.** `Unclear` starts at the first heading line (`#` to
`######`, then a space) whose text contains "unclear" in any case, outside a
code fence. It ends before the next heading of the same or a higher level,
or at the last line. Blank lines at its end are not part of it.

**The feedback text.** With no comments, `Feedback` returns exactly what the
fold writes now, so an old log folds to the same state:

```
A person rejected your result at the gate. Their notes: <notes>
```

With comments, it adds the block below. If the notes are blank, the first
line ends after "gate.".

```
A person rejected your result at the gate. Their notes: The empty case is wrong.

Their comments on lines of CONTRACT.md:

Line 5:
> - **Empty query.** The spec does not say what `query ""` returns. The tests expect `ErrEmpty`.
Return every record. An empty query is normal for us.

Lines 21-23:
> 1. `TestPutGet`: a stored record comes back.
> 2. `TestGetMissing`: a missing key gives `ErrNotFound`.
> 3. `TestParseQuery/empty_input`: an empty query gives `ErrEmpty`.
Add a test for a key with a space in it.
```

`internal/executor/claude.go` already adds the rejected `CONTRACT.md` after
this text, so the architect has the file the line numbers refer to.

### State (`event.NodeState`)

```go
Contract *Contract     // an agent: from its last node_succeeded; a send-back clears it
Emitted  []string      // a plan node: the ids of the nodes its graph_updated added, in graph order
Brief    *GateBrief    // a gate: from its last gate_waiting; a send-back keeps it for the next round
Sent     []GateComment // a gate: the comments of its last send-back; gate_waiting clears it
```

`State.Apply` sets them:

- `node_succeeded`: `Contract` from the data.
- `graph_updated` with a plan node: `Emitted` is each node of the new graph
  that the old state did not have.
- `gate_waiting`: `Brief` from the data, and `Sent` to nil.
- `gate_decided` with `revise`: `Sent` from the data. Each node the gate
  waits for gets `LastTaskError = d.Feedback()`.

### Event JSON

`node_succeeded` for the architect:

```json
{"cost_usd": 0.42,
 "contract": {"markdown": "# Contract: records CLI\n\n## Unclear in the spec\n...",
              "tests": 12, "red": 12, "unclear": {"line": 3, "end_line": 7}}}
```

`gate_waiting` at `approve-contract`, round 2:

```json
{"revisions_left": 1,
 "brief": {
   "round": 2,
   "check": ["Read \"Unclear in the spec\" first. Each item is a choice the architect made where the spec does not say. Comment on a choice that is wrong.",
             "Check that the tests ask for what you want. Builders cannot change a test, and a wrong test fails the run."],
   "approve": "Approve locks these tests: no builder can change them.",
   "contract": {"markdown": "...", "tests": 12, "red": 12, "unclear": {"line": 3, "end_line": 7}},
   "changes": {"added": [5, 23],
               "removed": [{"after": 4, "text": "- **Empty query.** The spec does not say what `query \"\"` returns. The tests expect `ErrEmpty`."},
                           {"after": 22, "text": "3. `TestParseQuery/empty_input`: an empty query gives `ErrEmpty`."}],
               "earlier": [{"line": 0, "was_line": 5, "quote": "- **Empty query.** ...", "body": "Return every record."},
                           {"line": 6, "was_line": 6, "quote": "- **Key case.** ...", "body": "Good."}]}}}
```

`gate_waiting` at `approve-tasks`:

```json
{"revisions_left": 0,
 "brief": {
   "round": 1,
   "check": ["Check that each task owns the right packages and waits only for the tasks it needs."],
   "approve": "Each attempt of a task may spend up to $3.00. The run starts no new node after it has spent $12.00; it has spent $0.42.",
   "tasks": [{"id": "pkg-store", "title": "internal/store", "owns": ["internal/store"], "after": [], "retries": 2, "cap_usd": 9},
             {"id": "cmd-cli", "title": "cmd/cli", "owns": ["cmd/cli"], "after": ["pkg-store", "pkg-api"], "retries": 2, "cap_usd": 9}]}}
```

`gate_decided` for a send-back:

```json
{"approved": false, "notes": "The empty case is wrong.", "revise": true,
 "comments": [{"line": 5, "quote": "- **Empty query.** ...", "body": "Return every record. An empty query is normal for us."},
              {"line": 21, "end_line": 23, "quote": "1. `TestPutGet`: ...\n2. ...\n3. ...", "body": "Add a test for a key with a space in it."}]}
```

An old log has `gate_waiting` with no data or with only `revisions_left`,
`gate_decided` with no `comments`, and `node_succeeded` with no `contract`.
All three must decode and fold as before.

### The sentences of the brief (the runner writes them)

Contract gate, `check`, in this order:

| When | Sentence |
|---|---|
| `Unclear` is set | `Read "Unclear in the spec" first. Each item is a choice the architect made where the spec does not say. Comment on a choice that is wrong.` |
| `Unclear` is zero | `The contract names nothing unclear in the spec. Check that the spec leaves nothing open.` |
| always | `Check that the tests ask for what you want. Builders cannot change a test, and a wrong test fails the run.` |
| `Red < Tests` | `N of M tests pass on the stubs. A test that passes on a stub can test nothing.` (N is `Tests - Red`) |
| `Cut` | `The gate shows the first 64 KB of the contract. Read the rest in CONTRACT.md in the run's repo.` |
| round 2 or later, nothing added or removed | `The architect changed nothing in the contract.` |

Contract gate, `approve`: `Approve locks these tests: no builder can change them.`

Task gate, `check`: `Check that each task owns the right packages and waits only for the tasks it needs.`

Task gate, `approve`, the parts that apply, with a space between them:

- `budget.node_usd` more than 0: `Each attempt of a task may spend up to $3.00.`
- `budget.run_usd` more than 0: `The run starts no new node after it has spent $12.00; it has spent $0.42.`

### Runner (`internal/runner`)

```go
// Decide records a human decision on a gate that is waiting.
func (r *Runner) Decide(run, node string, approved bool, notes string, comments []event.GateComment) error

// gateBrief says what a person must judge at the gate. It reads only the state.
func gateBrief(st *event.State, gate string) *event.GateBrief
```

`gateBrief` rules:

- `Round` is 1, or one more than the round of the gate's `Brief` in the state.
- If a node the gate waits for has a `Contract`, the brief has that contract.
- Else, if a node the gate waits for is a plan node with `Emitted`, the
  brief has one `TaskRow` for each emitted node. `Owns` is the node's
  scope. `After` is the node's deps that are also emitted nodes. `CapUSD`
  is `budget.node_usd × (1 + retries)`.
- Else the brief has only `Round`.
- `Changes` is set when the gate's last brief had a contract and this one
  has too. `Added` and `Removed` come from a longest-common-subsequence
  diff of whole lines. Drop the common first and last lines before the
  diff. If the two middles have more than 4,000,000 line pairs, count the
  whole old middle as removed and the whole new middle as added.
- For each comment in the gate's `Sent`, `Earlier` has one entry. Take the
  commented lines from the last contract. Find them as one run of equal
  lines in this contract. `Line` is the start nearest to `WasLine`, the
  smaller on a tie, or 0 if there is none.

`decide` rules, in this order:

1. The node must be a waiting gate, as now. Else the error is not
   `ErrInvalid`.
2. With comments, the gate's brief must have a contract. Each comment needs
   `Line` from 1 to the number of lines, `EndLine` 0 or from `Line` to the
   number of lines, and a body that is not blank and at most
   `MaxCommentBytes`. There are at most `MaxComments`. A broken rule returns
   an error that wraps `ErrInvalid` and names the comment by its position,
   such as `comment 3: line 400 is past the end of the contract (120 lines)`.
3. The runtime sets each `Quote` to the commented lines, joined with `\n`
   and cut at `MaxQuoteBytes` on a rune boundary. It sorts the comments by
   `Line`, and keeps the order of equal lines.
4. `Revise` is true when the decision is a rejection, the notes are not
   blank or there is a comment, and `revisionsLeft` is more than 0.

`complete` copies `executor.Result.Contract` into `node_succeeded`.

### Executor (`internal/executor`)

```go
type Result struct {
	Nodes   []graph.Node
	CostUSD float64
	// Contract is what an architect wrote, for the gate after it.
	Contract *event.Contract
}

type Step struct {
	// ...the fields it has now
	Contract string `yaml:"contract"` // CONTRACT.md; a node with one returns a contract
	Revised  string `yaml:"revised"`  // CONTRACT.md for an attempt after a send-back
	Tests    int    `yaml:"tests"`    // tests that ran on the stubs
	Red      int    `yaml:"red"`      // tests that failed on them
}
```

### HTTP API (`internal/server`)

```go
type DecideRequest struct {
	Approved bool                `json:"approved"`
	Notes    string              `json:"notes"`
	Comments []event.GateComment `json:"comments,omitempty"`
}

type GateSnapshot struct {
	Node    string           `json:"node"`
	Title   string           `json:"title,omitempty"`
	Context []string         `json:"context"`
	Brief   *event.GateBrief `json:"brief,omitempty"`
}

// CritStatus is the answer of the Crit routes.
type CritStatus struct {
	// URL is where Crit shows the contract. It is empty when Crit is not open.
	URL string `json:"url,omitempty"`
	// Comments are the open line comments in Crit's review file.
	Comments []event.GateComment `json:"comments,omitempty"`
	// Notes holds Crit's comments that name no line of this contract.
	Notes string `json:"notes,omitempty"`
}

// New returns the API handler. A nil crit turns the Crit routes off.
func New(store *event.Store, r *runner.Runner, rails guardrails.Guardrails, uiDir string, crit *crit.Sessions) http.Handler
```

| Route | Request | Answer |
|---|---|---|
| `POST /api/runs/{run}/gates/{node}` | `DecideRequest` as JSON. `{"approved":true,"notes":""}` still works. | 204. 400 for a body that is not JSON or is over 1 MB. 415 for another content type. 409 if the node is not a waiting gate or the run is not active. **422** for a comment that breaks a rule; the body is the error text. |
| `GET /api/runs/{run}/state` | none | `Snapshot`. Each waiting gate has `brief` if its log has one. `context` stays. |
| `GET /api/runs/{run}/gates/{node}/crit` | none | 200 `CritStatus`. 404 if `crit` is not installed or the run does not exist. 409 if the node is not a waiting gate with a contract. |
| `POST /api/runs/{run}/gates/{node}/crit` | exactly `{}` with `Content-Type: application/json` | 200 `CritStatus` with `url`. 400 for a query string or any member in the body. 415 for another content type. 404 and 409 as for GET. 500 if Crit does not start; the body says why. |

All routes stay behind `localOnly`. Both POST routes read their body with
`readJSON`, so a web page cannot send them without a preflight that the
server never answers.

### Generated TypeScript (`ui/src/gen/events.ts`)

`go generate ./...` writes these from the Go types above. Do not edit the
file. `cmd/gen-ts` must also list `server.CritStatus`.

```ts
export interface Contract { markdown: string; cut?: boolean; tests: number; red: number; unclear?: LineRange; }
export interface LineRange { line: number; end_line: number; }
export interface GateBrief { round: number; check: string[]; approve: string; contract?: Contract; changes?: ContractChanges; tasks?: TaskRow[]; }
export interface ContractChanges { added: number[]; removed: RemovedLine[]; earlier: EarlierComment[]; }
export interface RemovedLine { after: number; text: string; }
export interface EarlierComment { line: number; was_line: number; quote: string; body: string; }
export interface TaskRow { id: string; title?: string; owns: string[]; after: string[]; retries: number; cap_usd: number; }
export interface GateComment { line: number; end_line?: number; quote?: string; body: string; }
export interface GateWaitingData { revisions_left: number; brief?: GateBrief; }
export interface GateDecidedData { approved: boolean; notes?: string; comments?: GateComment[]; revise?: boolean; }
export interface NodeSucceededData { cost_usd?: number; contract?: Contract; }
export interface DecideRequest { approved: boolean; notes: string; comments?: GateComment[]; }
export interface GateSnapshot { node: string; title?: string; context: string[]; brief?: GateBrief; }
export interface CritStatus { url?: string; comments?: GateComment[]; notes?: string; }
```

### UI state (`ui/src/state.ts`)

```ts
interface NodeView {
  // ...the fields it has now
  brief?: GateBrief; // from gate_waiting; undefined for an old log
}
```

`decision` already copies `gate_decided`, so it gains `comments`. `describe`
adds the comment count: `approve-contract sent back: <notes> (2 comments)`.
`state.ts` stays a mapping with no rules.

### UI modules and props

```ts
// ui/src/Markdown.tsx
export type Block =
  | { kind: "heading"; line: number; endLine: number; level: number; text: string }
  | { kind: "para"; line: number; endLine: number; text: string }
  | { kind: "item"; line: number; endLine: number; depth: number; marker: string; text: string }
  | { kind: "code"; line: number; endLine: number; text: string }   // one line of a fenced block
  | { kind: "row"; line: number; endLine: number; head: boolean; cells: string[] }
  | { kind: "quote"; line: number; endLine: number; text: string }
  | { kind: "rule"; line: number; endLine: number };
export function parseBlocks(lines: string[]): Block[];
export function Inline(props: { text: string }): JSX.Element; // `code`, **bold**, *italic*, _italic_; the rest is text

// ui/src/ContractView.tsx
export function ContractView(props: {
  contract: Contract;
  changes?: ContractChanges;
  showChanges: boolean;
  // Leave out onComment to show line numbers as text, as replay does.
  onComment?: (line: number, endLine: number) => void;
  // What to show under a block: its comments and the open form.
  threads?: (line: number, endLine: number) => React.ReactNode;
}): JSX.Element;

// ui/src/TaskTable.tsx
export function TaskTable(props: { tasks: TaskRow[] }): JSX.Element;

// ui/src/drafts.ts
export interface Draft { notes: string; comments: GateComment[] }
export function loadDraft(run: string, gate: string, round: number): Draft;
export function saveDraft(run: string, gate: string, round: number, draft: Draft): void; // an empty draft removes the key
```

The markdown subset: headings, paragraphs, bullet and numbered lists with
nesting by indent, fenced code, tables with pipes, block quotes, rules, and
the four inline marks above. A fence line, a table separator line and a
blank line make no block. A list item or paragraph that runs over several
lines is one block. The renderer shows everything else as text: raw HTML,
images, and links (`[text](url)` shows as `text (url)`). It makes no `<a>`,
no `<img>` and never sets HTML.

The DOM that `ContractView` makes, which the gate UI and the browser check
rely on:

```
div.contract
  a.skip[href="#gate-notes"]            "Skip to your decision", visible on focus
  section[aria-label="Unclear in the spec"]      only if contract.unclear is set
  section[aria-label="The rest of the contract"] (aria-label="The contract" with no unclear section)
    div.md-line[data-line][.added]      one for each block
      button.md-gutter                  the line number; aria-label "Comment on line 5"
      div.md-block.md-<kind>
    (what threads(line, endLine) returns)
    div.md-line.removed                 with showChanges; plain text with a "−" mark
```

The draft key in `localStorage` is `galvantula:draft:<run>:<gate>:<round>`.
`round` is `brief.round`, or 0 with no brief. Storage that is off or full
must not break the form: keep the draft in memory.

### UI text

| Where | Text |
|---|---|
| counts | `12 tests · 12 fail on the stubs` |
| changes button | `Show changes` / `Hide changes` (`aria-pressed`) |
| a new line, for a screen reader | `new` before the block; a visible `+` in the gutter |
| earlier comment, lines still there | `Round 1 · these lines did not change` |
| earlier comments, lines gone, list heading | `Your round 1 comments on lines the architect changed` |
| comment form label | `Comment on line 5` or `Comment on lines 21 to 23` |
| comment form buttons | `Save comment`, `Cancel` |
| a saved draft's buttons | `Edit`, `Delete` |
| blur, empty body | `Write a comment, or press Cancel.` |
| blur, comment over `MaxCommentBytes` | `Shorten the comment. The limit is 4,000 bytes.` |
| the whole request over 1 MB | `Shorten the notes or the comments. Together they must be under 1 MB.` |
| live region | `Comment saved on line 5.` `Comment deleted.` `Added 3 comments from Crit.` |
| Reject, revisions left, feedback | `Reject sends this back to architect with your notes and 2 comments. 2 revisions left.` Name only what there is: `your notes`, `1 comment`, or both. |
| Reject, revisions left, no feedback, contract | `Reject ends the run. Add notes or a comment to send this back to architect.` |
| Reject, other cases | as now |
| Approve with draft comments | `Approve keeps your 2 comments in the log. The architect does not see them.` |
| 422 from the server | the server's text, then `Fix the comment, then try again.` |
| task table headings | `Task`, `Owns`, `Depends on`, `Retries`, `Cost cap` |
| task with no deps | `starts at once` |
| cost cap of 0 | `no limit` |
| Crit buttons | `Open in Crit` (busy: `Starting Crit…`), then the link `Open Crit` and the button `Use Crit comments` |
| Crit did not start | `Crit did not start. Run crit --version in a terminal, then try again.` |
| Crit has no comments | `Crit has no comments yet. Add one in Crit, then press Use Crit comments.` |

The lines that exist now for loading, a failed load, an empty log and a
server that does not answer stay as they are.

### CSS tokens

Use only the Wire tokens in `ui/src/styles.css`. Add none.

| Token | Use in this change |
|---|---|
| `--you` | Approve and the focus ring, as now. Nothing new: a comment is not "your turn". |
| `--surface`, `--line` | comment threads, the comment form, code lines, table cells |
| `--line-strong` | the gutter button's border on hover and focus; a block that has a draft comment |
| `--muted` | line numbers, the round label of an earlier comment |
| `--ok` | a line the new round added. The `+` mark carries the meaning too. |
| `--bad` | a line the new round removed, with a `−` mark and a line through it; errors |

Sizes, corners, lines and reduced motion follow the Wire rules.

### Crit: the process and its folder

The routes, the command, the environment and the folder are fixed. The Go
API between `internal/server` and `internal/crit` belongs to workstream G.

- Folder for one round of one gate: `<runs>/<run>/crit/<gate>/round-<N>/`.
  `<runs>` is `runner.Root`. The run id and the gate id come from the folded
  state after the run and the waiting gate are found, never from the request
  path as text. `N` is `brief.round`.
- The server makes the folder and `home` inside it, and writes
  `CONTRACT.md` there from `brief.contract.markdown`. It reads no file that
  an agent wrote.
- Command: the path of `crit` from the start of the server, with the
  arguments `--no-open CONTRACT.md` and nothing else. The working directory
  is the round folder. No shell.
- Environment, exactly: `HOME=<round folder>/home`, `PATH` as the server has
  it, `CRIT_SHARE_URL=` (empty), `CRIT_NO_UPDATE_CHECK=1`,
  `CRIT_NO_INTEGRATION_CHECK=1`. Nothing else from the server's environment.
- The address: read the command's standard error until a line has
  `crit daemon at http://…`. Parse the URL. Refuse it unless its host is
  `localhost` or a loopback address. With no such line in 15 seconds, stop
  the command and answer 500.
- The review file: `<round folder>/home/.crit/reviews/*/review.json`. There
  is at most one.
- Stop Crit for a gate when the gate is decided, by any client, and stop
  every one when the server stops: send SIGTERM to the command, then run
  `crit stop --all` with the same folder and environment, with a 5 second
  limit.

Mapping a review file to `CritStatus`:

| In `review.json` | Goes to |
|---|---|
| a comment in `files["CONTRACT.md"].comments` with `start_line` from 1 to the number of lines | a `GateComment`: `line` is `start_line`; `end_line` is `end_line` if it is larger; `body` is `body`. If the comment has `quote`, the body starts with `On "<quote>": `. |
| the same with `start_line` past the end, or `drifted: true` | `notes`, as `Line 12: <body>` |
| a comment with `scope: "file"`, and each entry of `review_comments` | `notes`, the body alone |
| a comment with `resolved: true`; `replies`; any other file | nowhere |

Parts of `notes` are joined with a blank line. The runtime still sets each
`quote` when the person decides, from the contract of the gate.

## Workstreams

Each one owns its files. No other workstream of the same phase may change
them. If a check fails on a file you do not own, report it and do not fix
it. Every workstream ends with the checks it lists, then reviews its own
change with ponytail-review and its own prose against the Orwell rules.

Nobody changes `guardrails.yaml`, `AGENTS.md`, `ui/src/App.tsx`,
`ui/src/GraphView.tsx`, `ui/src/styles.css` or `ui/package.json`. This
change adds no setting and no package.

### Phase 1, one workstream

**A. Types, fold and plumbing.** Goal: every shared type and signature
exists and compiles, so phase 2 can run in parallel.

Owns: `internal/event/event.go`, `internal/event/event_test.go`,
`internal/executor/executor.go`, `internal/runner/runner.go`,
`internal/runner/runner_test.go`, `internal/server/server.go`,
`internal/server/snapshot.go`, `internal/server/server_test.go`,
`cmd/gen-ts/**`, `ui/src/gen/events.ts` (generated), `ui/src/state.ts`.
Reads: the rest.

1. Add the types, constants and functions of "Event types" and "State".
   Change `State.Apply` as "State" says.
2. Add `Contract` to `executor.Result`. Change nothing else in that file.
3. Change `Runner.Decide` to the new signature. Pass the comments into
   `gate_decided` as they come; workstream B adds the rules. Make `complete`
   copy the contract into `node_succeeded`. Fix the calls in the tests.
4. Add `Comments` to `DecideRequest` and pass it to `Decide`. Answer 422
   when the error is `runner.ErrInvalid`. Add `Brief` to `GateSnapshot` and
   fill it from the state. Add the type `CritStatus`. Do not change `New`.
5. List `server.CritStatus` in `cmd/gen-ts`. Run `go generate ./...`.
6. Add `brief` to `NodeView` and the comment count to `describe`.

Tests (`internal/event/event_test.go`):

- `NewContract`: `\r\n` becomes `\n`; a text over the limit is cut at a line
  end and `Cut` is set; `Unclear` is found for any case of "unclear", ends
  before the next heading of the same or a higher level, skips a heading in
  a code fence, and is zero with no such heading.
- An old log folds: `gate_waiting` with no data and with only
  `revisions_left`, `gate_decided` with no comments, `node_succeeded` with
  no contract. The brief is nil. After a send-back with the notes `x`, the
  architect's `LastTaskError` is exactly
  `A person rejected your result at the gate. Their notes: x`.
- `Feedback` with comments gives the text of "The feedback text", byte for
  byte, for one line and for a range, with and without notes.
- The fold keeps what a gate needs: the contract after `node_succeeded` and
  none after a send-back; `Emitted` after a plan's `graph_updated`; the
  brief after `gate_waiting`; the brief and `Sent` after a send-back.

Checks: `gofmt -l .`, `go vet ./...`, `GOFLAGS=-buildvcs=false go fix -diff ./...`,
`go test -race ./...`, `go generate ./...` (no diff after it),
`cd ui && npm run build`.

### Phase 2, four at once

**B. Runner: the brief, the comment rules and the changes.** Goal: each
`gate_waiting` has its brief, and each decision has checked comments.

Owns: `internal/runner/runner.go`, new `internal/runner/brief.go`,
`internal/runner/runner_test.go`, new `internal/runner/brief_test.go`.
Reads: `internal/event`, `internal/executor/executor.go`, `fakes/factory.yaml`.

1. Write `gateBrief` in `brief.go`, with the sentences of "The sentences of
   the brief". Emit it in `gate_waiting`, in the one place that event is made.
2. Write the line diff and the search for earlier comments in `brief.go`.
3. Add the rules of `decide`, in the order given.
4. Keep `TestFactoryDemoRun` green. Workstream C changes the fake script at
   the same time, so run it again when C is done.

Tests:

- A contract gate: the brief has round 1, the contract, the exact check
  sentences and the approve sentence. `Red < Tests` adds its sentence. A
  contract with no unclear section changes the first sentence. A cut
  contract adds its sentence.
- A task gate: one row for each emitted node. `After` lists only tasks, not
  the gate. `CapUSD` is `node_usd × (1 + retries)`. The approve sentence
  has both parts, one part, or is empty, as the budgets say.
- A gate after a tool node: the brief has only the round.
- A restart: fold the events up to the architect's success in a new runner.
  The gate's brief equals the brief of an unbroken run.
- The comment rules, as a table: line 0, a line past the end, `EndLine`
  less than `Line`, a blank body, a body over the limit, 101 comments, and
  a comment at a gate with no contract. Each gives `ErrInvalid` and no
  event. A valid decision has each quote set, cut at the limit, and the
  comments in line order. A quote in the request is replaced.
- A rejection with one comment and no notes sends the work back. The
  architect's next `Task.Feedback` has `Line 5:` and the quoted line.
- Round 2: the brief has round 2, the added and removed lines, an earlier
  comment on an unchanged line with its new line number, and one on a
  changed line with line 0. A contract that did not change adds its sentence.
- The line diff, as a table: equal texts, one line changed, a line added at
  the start, a line removed at the end, and texts over the size limit.

Checks: the Go checks of workstream A, for `./internal/runner/...`.

**C. Executor and the fake.** Goal: the architect returns its contract, and
the demo shows a real one.

Owns: `internal/executor/**`, `fakes/factory.yaml`, new `fakes/hostile.yaml`.
Reads: `internal/event`, `graphs/factory.yaml`.

1. Make the red check's counts reach the architect's `after` hook. The hook
   reads `CONTRACT.md` from main, builds the contract with
   `event.NewContract`, and returns it. `agent` puts it in the result.
   Refuse a `CONTRACT.md` that is not a regular file. Read at most
   `MaxContractBytes` and one byte more.
2. Replace the per-line log of the contract with one line:
   `contract: 12 tests, 12 fail on the stubs (the full file is <path>)`.
3. Change the sentence about `CONTRACT.md` in `architectTemplate` to:
   `CONTRACT.md. A person reads this file to approve the contract, so keep
   it short and plain. Start it with a section headed "## Unclear in the
   spec": one list item for each place where spec.md is unclear, with the
   choice your tests make. Write "Nothing." there if spec.md leaves nothing
   open. After it, give the packages and their interfaces, and what the
   tests check.`
4. Add the four fields to `Step`. `Fake.Run` returns a contract when the
   step has one. It uses `Revised` when the task is a revision and
   `Revised` is set. On a revision it logs the feedback it got, as one line.
5. In `fakes/factory.yaml`, give `architect` the contract below, `tests: 12`
   and `red: 12`. Give it a `revised` text with two changes: the first list
   item becomes `- **Empty query.** An empty query returns every record.
   The tests expect no error.`, and test 3 becomes
   `` 3. `TestParseQuery/empty_input`: an empty query matches every record. ``
   Give each emitted node its scope: `[internal/store]`, `[internal/api]`,
   `[cmd/cli]`. Change the header comment of the file to say what it shows.
6. Write `fakes/hostile.yaml` for `graphs/factory.yaml`. Its contract holds
   these four lines, each as its own paragraph, and nothing else of note:
   `<img src=x onerror="document.title='pwned'">`,
   `<script>document.title='pwned'</script>`,
   `[open](javascript:document.title='pwned')`,
   `![pixel](https://example.invalid/pixel.png)`.

The demo contract (`contract:` of `architect`):

````markdown
# Contract: records CLI

## Unclear in the spec

- **Empty query.** The spec does not say what `query ""` returns. The tests expect `ErrEmpty`.
- **Key case.** The spec does not say if keys are case-sensitive. The tests treat `A` and `a` as two keys.
- **Missing key.** `get` of a key that is not stored: the tests expect exit code 1 and `not found` on stderr.

## Packages and interfaces

| Package | What it does |
|---|---|
| `internal/store` | `Put(key, value string) error` and `Get(key string) (string, error)` |
| `internal/api` | `ParseQuery(q string) (Query, error)` |
| `cmd/cli` | `run(args []string, stdin io.Reader, stdout, stderr io.Writer) int` |

## What the tests check

12 tests. Each one fails on the stubs.

1. `TestPutGet`: a stored record comes back.
2. `TestGetMissing`: a missing key gives `ErrNotFound`.
3. `TestParseQuery/empty_input`: an empty query gives `ErrEmpty`.

```go
func ParseQuery(q string) (Query, error)
```
````

Tests:

- The architect's result has the contract, the test count and the red
  count. Its log has the summary line and not the contract's lines.
- A `CONTRACT.md` over the limit comes back cut, with `Cut` set.
- The architect's brief names the heading (extend `TestArchitectBriefStatesTheRule`).
- The fake returns a contract from a step that has one, the revised text on
  a revision, and none from a step with no contract.
- `fakes/factory.yaml` and `fakes/hostile.yaml` parse, and an unknown field
  is still an error.

Checks: the Go checks, for `./internal/executor/...`.

**D. MCP server and the plugin skill.** Goal: `factory_status` shows the
brief, so a Claude Code user decides on the same facts.

Owns: `internal/mcp/**`, `plugin/skills/factory/SKILL.md`.
Reads: `internal/server/snapshot.go`, `internal/event`.

1. Read `brief` from the snapshot's gates. For a gate with a contract or
   tasks, print the brief in place of the context lines. Keep the context
   lines for every other gate.
2. The text for a contract gate, in this order: the line that exists now,
   with the round; `What to check:` and each sentence; the counts; the
   approve sentence; `The contract (CONTRACT.md):` and each line with its
   number; from round 2, `Lines the architect added: 5, 18`, and
   `Comments from the last round:` with each comment, its old line, and its
   new line or `its lines are gone`.
3. The text for a task gate: the check sentence, one line for each task
   (id, owns, after, retries, cost cap), and the approve sentence.
4. `factory_approve` keeps `notes` as plain text. Change its description
   and the skill to say: name the line number and quote the line in the notes.

Tests (`internal/mcp/mcp_test.go`): the status text for a snapshot with a
contract brief of round 2, with a task brief, and with no brief, where the
text must stay as it is now.

Checks: the Go checks, for `./internal/mcp/...`.

**E. Contract and task views.** Goal: the rendered contract with line
numbers, and the task table, as parts that the gate UI puts together.

Owns: new `ui/src/Markdown.tsx`, new `ui/src/ContractView.tsx`, new
`ui/src/TaskTable.tsx`, new `ui/src/contract.css`.
Reads: `ui/src/gen/events.ts`, `ui/src/styles.css`, `fakes/factory.yaml`.

1. Write `parseBlocks` and `Inline` for the subset in "UI modules and
   props". Use the line rule. A line the parser does not know is a paragraph.
2. Write `ContractView` with the DOM in the contract. Show the unclear
   range first, then the lines before it, then the lines after it. With no
   unclear range, show one section.
3. A gutter number is a `<button>` when `onComment` is set, and text when
   it is not. `aria-expanded` is true while `threads` returns something for
   its block.
4. With `showChanges`, mark each block that holds an added line, and put
   each removed line before the first block that starts after its `after`
   line. Put the rest at the end.
5. Write `TaskTable` as a real `<table>` with `<th scope="col">`.
6. In dev mode only (`import.meta.env.DEV`), check `parseBlocks` once
   against a short text that has every block kind, with `console.assert`.
   The UI has no test runner, and this plan adds none.
7. Write a `shortcut:` comment for the table rows: they are grids, not a
   `<table>`, because a comment thread must fit between two rows.

Checks: `cd ui && npm run build`.

### Phase 3, three at once

**F. Gate review.** Goal: the gate of "What a gate shows", with drafts,
comments, the decision and the Crit buttons.

Owns: `ui/src/GateReview.tsx`, `ui/src/NodePanel.tsx`, `ui/src/gate.css`,
new `ui/src/drafts.ts`.
Reads: the files of workstream E, `ui/src/state.ts`, `ui/src/gen/events.ts`.

1. Read the brief from `run.nodes[id].brief`. With a contract or tasks,
   build the new layout and do not fetch `/state`. With neither, keep the
   path that exists now, unchanged. The contract shows in replay too, with
   no comment buttons.
2. Keep notes and comments in a draft. Load it when the gate mounts, save
   it on each change, and remove it when the server answers 204.
3. The comment form opens under its block. The label is above the field.
   Focus goes to the field. Escape and Cancel close it. Save and Cancel
   put focus back on the gutter button. Validate on blur; measure the body
   in UTF-8 bytes, as `notesProblem` does now. A saved comment has Edit and
   Delete. Say each change in one `aria-live="polite"` region.
4. Send `comments` in the `DecideRequest`, for Approve and for Reject. Check
   the size of the whole body before the request. Show a 422 as the table
   says. Keep the loading, disabled, focus and error states of the form.
5. Show the lines for each button with the texts in "UI text".
6. From round 2, show **Show changes**, the earlier comments under their
   lines, and the list of earlier comments whose lines are gone.
7. Crit: when a live gate with a contract waits, `GET …/crit`. Show the
   Crit buttons only for a 200. **Open in Crit** sends `POST …/crit` with
   the body `{}`. Then show the answer's `url` as a link that opens a new
   tab (`rel="noreferrer"`). **Use Crit comments** sends the GET again,
   adds each comment the draft does not have (same line, end line and
   body), and adds the notes if the Notes field does not hold them.
8. A decided gate shows its comments with line numbers and quotes.
9. Widen the panel for a contract in `gate.css`, as `.gate-work` does now.

Checks: `cd ui && npm run build`.

**G. Crit and the server.** Goal: the Crit routes, the adapter, and the
tests of the server's new behavior.

Owns: new `internal/crit/**`, `internal/server/**`, `cmd/factory/main.go`.
Reads: `internal/event`, `internal/runner`, `internal/sandbox`.

1. First, check the installed Crit against "What was checked about Crit":
   run `crit --version`, then the four `crit comment` commands of that
   section in a temporary folder with a temporary `HOME`. Compare the file
   with the recorded one. Report a difference to the coordinator before you
   build on it.
2. Write `internal/crit`: find the program on `PATH` once, start and stop
   it by the rules of "Crit: the process and its folder", and read a
   review file by the mapping table.
3. Add the `crit` argument to `server.New` and the two routes. Stop a
   gate's Crit after a decision that the runner accepts.
4. In `cmd/factory/main.go`, find `crit` at start, log whether it is there,
   pass it to `server.New`, and stop every Crit when the server stops.
5. Save the recorded review file as `internal/crit/testdata/review.json`.
   Add to it one resolved comment, one comment with a `quote`, and one
   comment on line 400.

Tests:

- The adapter, on the recorded file: lines 5 and 10 to 12 become comments;
  the file comment and the review comment go to the notes; the resolved
  comment is left out; the quoted one starts with `On "…": `; line 400 goes
  to the notes as `Line 400: …`. A file with no `files` key gives nothing.
- With no `crit`: GET and POST answer 404.
- With a stand-in `crit` (a shell script in a temporary folder that writes
  its arguments, folder and environment to a file, prints
  `Started crit daemon at http://localhost:1 (session abc, PID 1)` on
  standard error, and waits): POST with `{}` answers 200 and the URL. The
  arguments are exactly `--no-open CONTRACT.md`. The folder is the round
  folder. `CRIT_SHARE_URL` is set and empty. `HOME` is under the round
  folder. A variable the test set in the server's environment is not there.
- The route refuses a request that carries arguments: `?file=x` answers
  400; the body `{"file":"/etc/passwd"}` answers 400; `text/plain` answers
  415; the host `evil.example` answers 403. None starts the stand-in.
- A stand-in that prints `http://example.com:80` gives 500 and is stopped.
- A decision on the gate stops its Crit.
- `POST …/gates/{node}`: 204 with comments, and the event has the quotes;
  422 for a bad comment; 409 for a gate that does not wait; the old body
  with no `comments` works; a body over 1 MB answers 400.
- `GET …/state` has the brief of a waiting gate.

Checks: the Go checks, for `./...`.

**H. Docs.** Goal: the README says what the gates show and how Crit fits.

Owns: `README.md`.
Reads: this plan, the code of phases 1 and 2.

1. "Run the demo": say what each gate shows, how to comment on a line, and
   what a send-back shows in the next round.
2. "Run a real agent": the gate shows the contract, not the log. A
   rejection needs notes or a comment.
3. A short section "Review a contract in Crit": it is optional; the server
   finds `crit` on `PATH` at start; it runs Crit on a copy of the contract
   with sharing off and its own home folder; `crit stop --all` stops one
   that a killed server left.
4. The tool table (`factory_status`) and "How the parts connect"
   (`internal/crit`).

Checks: read the text against the Orwell rules.

### Phase 4, the coordinator

1. Run every check in `AGENTS.md`:
   `gofmt -l .`, `go vet ./...`, `GOFLAGS=-buildvcs=false go fix -diff ./...`,
   `go test -race ./...`, `go generate ./...` (it must change nothing),
   `cd ui && npm run build`.
2. Run the browser scenarios below against the fake demo.
3. Review the whole change with ponytail-review. Review the prose
   (README, comments, UI text, the brief's sentences, error strings)
   against the Orwell rules. Fix every Must fix and Should fix finding.
4. `AGENTS.md` also asks for `go run ./cmd/phasereview` at the end of a
   phase. The owner runs it: it needs a different reviewer model and it
   spends money. The owner picks the phase name and gives the base commit.
   The owner also decides when to run the evals again, since the
   architect's brief changed.

## Tests

The Go tests are listed with each workstream. Together they cover: the brief
for each kind of gate, an old log, the comment rules and the feedback text,
the Crit adapter against a recorded review file, and the route that refuses
a request with arguments.

The UI has no test runner, and this plan adds none. The coordinator checks
the UI with a scripted headless browser. Do not use the owner's server on
port 7070 for this, except to look. Start a second one:

```
go run ./cmd/factory serve -addr localhost:7071 -db <tmp>/factory.db -runs <tmp>/runs
go run ./cmd/factory start -server http://localhost:7071 -fake fakes/factory.yaml graphs/factory.yaml
```

Do not copy the owner's `factory.db` for this: a second server would
resume its unfinished runs.

1. **The contract gate.** The panel shows the two check sentences,
   `12 tests · 12 fail on the stubs`, and the rendered contract. The first
   section is "Unclear in the spec". The DOM has a heading, list items, a
   grid of table cells, a code line, `<code>` and `<strong>`. The lines for
   the buttons say `Approve starts plan.` and
   `Approve locks these tests: no builder can change them.`
2. **A comment by keyboard.** Tab to the gutter button of line 5. Press
   Enter. The field has focus and its label says `Comment on line 5`. Blur
   it empty: the error shows. Type a comment, save it. Focus is on the
   gutter button. The Reject line now names `1 comment`. Open `architect`
   and come back: the comment is still there. Reload the page: it is still
   there.
3. **A send-back with line comments.** Add a second comment by mouse and
   notes. Press Reject. The gate shows `Sent back`, the notes and both
   comments with their quotes. Each quote equals the text of its line.
   Open `architect`: its log holds the feedback with `Line 5:` and the
   quoted line.
4. **The next round.** The gate waits again. It shows round 2 and one
   revision left. **Show changes** marks the two new lines and shows the
   two removed lines. The comment on line 5 is in the list of comments on
   changed lines. The other comment is under its line. The draft is empty.
5. **The task gate.** Approve the contract. `approve-tasks` shows the check
   sentence and a table with three rows: `pkg-store` owns `internal/store`
   and starts at once; `cmd-cli` depends on `pkg-store, pkg-api`; retries 2
   and the cost cap from the guardrails. The Reject line says that it ends
   the run. Approve: the run goes on and ends.
6. **Replay.** Move the slider to a time when the contract gate waited.
   The contract shows, with no comment buttons and a disabled form.
7. **Hostile text.** Start a run with `fakes/hostile.yaml`. The four lines
   show as text. The page title never holds `pwned`. `.contract` has no
   `img`, `script` or `a` element. The browser made no request to
   `example.invalid`.
8. **A gate with no contract.** Start a run on a graph with a gate after a
   tool node (write the graph in the scratch folder). The gate shows the
   log lines under "From …", as before this change.
9. **An old log.** After `npm run build`, the owner's server on port 7070
   serves the new UI with its old binary. Open it and look only: any gate
   there, waiting or decided, shows as before this change, with no error in
   the console and no Crit button.
10. **A failed load and a failed send.** In scenario 8, stop the server on
    7071, then open the gate: `Could not load this. Open … to read its log.`
    In scenario 1, stop the server, then press Reject with a comment: the
    line about `factory serve` shows, and the draft is still there.
11. **Crit.** With `crit` on `PATH`: **Open in Crit** shows the link, and
    the link opens Crit with the contract. Add a comment in Crit on line 6.
    **Use Crit comments** adds it under line 6. Press it again: no second
    copy. Decide the gate: the Crit page stops answering. Start the server
    with a `PATH` that has no `crit`: the gate has no Crit button.

## What was checked about Crit

Checked on 2026-11-11.

- **Where it lives.** https://crit.md names the repository
  https://github.com/tomasz-tomczyk/crit.
- **License.** MIT, copyright 2026 Tomasz Tomczyk (the `LICENSE` file and
  the GitHub page).
- **Versions.** The newest release is v0.23.2. The source read for this plan
  is v0.18.2 (in the Go module cache) and a clone of `main` from August
  2026. The `crit` on this machine reports its version as `dev`.
- **Commands this plan relies on.**
  - `crit --no-open <file>` starts a local server for the file and waits
    until the person ends the review. `--no-open` is in the README's flag
    table.
  - It prints its address on standard error:
    `Started crit daemon at http://localhost:<port> (session <key>, PID <n>)`,
    or `Connected to crit daemon at …` if one runs already (v0.18.2 source,
    `internal/session/review_cli.go`; the `/crit` skill quotes the first
    form).
  - The server listens on `127.0.0.1` unless `--host` or `CRIT_HOST` says
    otherwise (README, "Local by default").
  - An empty `CRIT_SHARE_URL` turns sharing off for that process (README on
    `main`; `ResolveShareURL` in v0.18.2).
  - `CRIT_NO_UPDATE_CHECK=1` stops the one request Crit makes at start.
    `CRIT_NO_INTEGRATION_CHECK=1` stops its check of agent integrations.
  - A SIGTERM to the command stops the server it started (v0.18.2 source,
    `installDaemonSignalHandler`). `crit stop --all` stops the servers of
    the current `HOME` (help text).
  - `crit comment <path>:<line[-end]> <body>` writes a comment with no
    server. The plan uses it only to record a test file.
- **Where the review file is.** `$HOME/.crit/reviews/<12 hex digits>/review.json`.
  Seen by running `crit comment` with a temporary `HOME`.
- **The review file.** Recorded with the four commands below, in a folder
  that held a 12-line `CONTRACT.md`, with `HOME` set to an empty folder:

  ```
  crit comment --author Reviewer CONTRACT.md:5 'Return an empty list instead.'
  crit comment --author Reviewer CONTRACT.md:10-12 'Add a test for a missing key.'
  crit comment --author Reviewer CONTRACT.md 'The file is short. Good.'
  crit comment --author Reviewer 'Overall: close.'
  ```

  ```json
  {
    "branch": "",
    "base_ref": "",
    "updated_at": "2026-11-11T19:51:24Z",
    "review_round": 1,
    "review_comments": [
      {"id": "r_deeba9", "start_line": 0, "end_line": 0, "body": "Overall: close.",
       "author": "Reviewer", "scope": "review",
       "created_at": "2026-11-11T19:51:24Z", "updated_at": "2026-11-11T19:51:24Z"}
    ],
    "files": {
      "CONTRACT.md": {
        "status": "modified",
        "file_hash": "",
        "comments": [
          {"id": "c_851725", "start_line": 5, "end_line": 5, "body": "Return an empty list instead.",
           "anchor": "- An empty query: the spec does not say. The tests expect ErrEmpty.",
           "author": "Reviewer",
           "created_at": "2026-11-11T19:51:24Z", "updated_at": "2026-11-11T19:51:24Z"},
          {"id": "c_c97525", "start_line": 10, "end_line": 12, "body": "Add a test for a missing key.",
           "anchor": "| Test | Checks |\n|---|---|\n| TestPutGet | a stored record comes back |",
           "author": "Reviewer",
           "created_at": "2026-11-11T19:51:24Z", "updated_at": "2026-11-11T19:51:24Z"},
          {"id": "c_1b2bae", "start_line": 0, "end_line": 0, "body": "The file is short. Good.",
           "author": "Reviewer", "scope": "file",
           "created_at": "2026-11-11T19:51:24Z", "updated_at": "2026-11-11T19:51:24Z"}
        ]
      }
    }
  }
  ```

  A line comment has no `scope`. Other fields the source defines and the
  adapter reads: `resolved`, `quote`, `drifted`. The adapter must ignore
  every field it does not know.

**Not verified.**

- No Crit server ran for this plan. The address line, the effect of
  `--no-open`, of the empty `CRIT_SHARE_URL` and of SIGTERM come from the
  source and the docs, not from a run.
- How soon the server writes a new comment to `review.json`. The source
  says the write is delayed by a timer. If the delay is long, **Use Crit
  comments** finds nothing at first, and the UI says what to do.
- The behavior of v0.23.2, the newest release. `main` now has
  `share_targets` beside `share_url`; the README still says that an empty
  `CRIT_SHARE_URL` turns sharing off.
- How Crit's page treats raw HTML in the markdown it renders.
- `crit install <agent>` and sharing to crit.md or a crit-web server. This
  plan uses neither.

## Security review of step 3

- The API has no login and answers only on localhost. The start route sits
  behind `localOnly` and `readJSON`, like the decision route, so a web page
  cannot call it.
- The route takes nothing from the request but the run and the gate. Both
  must name a waiting gate with a contract in the log. The command, its
  arguments and its environment are fixed. No request text reaches a
  shell, an argument or a path.
- Crit gets a copy of the contract that the server writes from the event
  log. The server opens no path that an agent wrote, so the symlink risk
  that `noLinks` guards against does not arise. The round folder is outside
  every folder the sandbox gives an agent.
- Crit runs outside the sandbox, as `git` and the server do. It is the
  owner's program, and it runs no agent code.
- Crit's home folder is inside the round folder. It has no Crit config, so it has
  no login token for crit.md, no `agent_cmd` and no hooks. Sharing is off
  twice: by the empty `CRIT_SHARE_URL`, and because there is no token.
- Crit's working directory is not the product repo, so a
  `.crit.config.json` that an agent wrote there is never read. Crit does
  read that file at the root of the git repo that holds the runs folder.
  That repo is the owner's.
- The server gives the UI only an address on this machine.
- Crit's page is another origin. Script in it cannot send JSON to the
  factory API.

## Not in this plan

- Comments through `factory_approve`. It keeps plain notes.
- Comments on the task table, on `spec.md` or on a test file.
- Replies to a comment, and resolving one.
- A range of blocks chosen by drag in the UI. A comment covers one block. A
  range comes only from Crit.
- A side-by-side diff. Removed lines show in place, as text.
- Clickable links and images in the contract.
- Draft comments in the event log, or shared between browsers.
- Keyboard shortcuts beyond Tab, Enter and Escape.
- A UI test runner. A new npm package. A setting in `guardrails.yaml`.
- Sharing a review to crit.md or crit-web, `crit install`, Crit's story
  mode, and its "Send to agent".
- A check that fails the architect for a missing "Unclear in the spec"
  section.

## Risks

- **The log changes.** An old log has no brief, no contract and no
  comments. If a decode fails, an old run does not load. Guard: the old-log
  test in workstream A, and browser scenario 9.
- **The contract is in the log twice for each round.** Three rounds of a
  64 KB contract add about 400 KB to one run's stream. Guard: the 64 KB
  limit. The per-line log copy goes.
- **The architect's prompt changes.** The eval pass rate or cost can move.
  Guard: the heading is asked for, not checked, so it costs no retry. The
  owner runs the evals again.
- **The architect leaves out the heading.** The gate cannot put the section
  first. Guard: the first check sentence then says that the contract names
  nothing unclear, and the whole contract shows.
- **The renderer misreads a construct.** A nested table or a setext heading
  shows as plain text. Guard: text is still readable in a monospace UI; the
  renderer cannot run what it shows (scenario 7).
- **The UI and Go count lines differently.** A comment would quote the wrong
  line. Guard: one line rule; the runtime sets the quote; scenario 3
  compares each quote with its line.
- **A long contract has many tab stops.** A keyboard user must pass each
  line number to reach the form. Guard: the skip link at the top of the
  contract.
- **A draft outlives its round.** Guard: the round is in the storage key,
  and a 204 removes the draft.
- **Two UI agents work at once in phase 3, and four agents in phase 2.** A
  type check can fail on a file that another agent is in the middle of.
  Each agent reports that and does not fix a file it does not own.
- **Workstream B and workstream C both touch the demo run.** B's test reads
  `fakes/factory.yaml` while C changes it. Guard: B runs
  `TestFactoryDemoRun` again when C is done; the coordinator runs the whole
  suite in phase 4.
- **`npm run build` replaces `ui/dist` under the owner's demo server.** The
  page on port 7070 gets the new UI with an old server on reload. Guard:
  the new UI must work against a server with no brief and no Crit route
  (scenario 9).
- **Crit changes.** A new version prints its address in another form, or
  moves the review file. Then **Open in Crit** fails, or **Use Crit
  comments** finds nothing. Guard: workstream G checks the installed
  version first; both failures tell the person what to do; step 2 does not
  need Crit.
- **A Crit server outlives a killed factory server.** It keeps one port on
  localhost and shows one contract. Guard: the next **Open in Crit** for
  that round connects to it; a decision stops it; the README gives the
  command to stop it by hand.
- **The person comments in Crit and in the gate.** The same remark can
  arrive twice. Guard: **Use Crit comments** skips a comment the draft has.
