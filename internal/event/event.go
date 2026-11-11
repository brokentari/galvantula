// Package event holds the append-only event log and the run state it folds into.
// Events record decisions as well as facts (for example, whether a failed node
// will retry), so any reader can rebuild the state without the runtime's rules.
package event

//go:generate go run ../../cmd/gen-ts -o ../../ui/src/gen/events.ts

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"
	"time"

	"github.com/brokentari/galvantula/internal/graph"
	"github.com/brokentari/galvantula/internal/guardrails"
)

// Type names an event.
type Type string

const (
	RunCreated    Type = "run_created"
	GraphUpdated  Type = "graph_updated"
	NodeStarted   Type = "node_started"
	NodeLog       Type = "node_log"
	NodeSucceeded Type = "node_succeeded"
	NodeFailed    Type = "node_failed"
	GateWaiting   Type = "gate_waiting"
	GateDecided   Type = "gate_decided"
	RunFinished   Type = "run_finished"
)

// Payloads maps each event type to its data type. A nil value means the
// event carries no data. cmd/gen-ts reads this map to write the UI's types.
var Payloads = map[Type]any{
	RunCreated:    RunCreatedData{},
	GraphUpdated:  GraphUpdatedData{},
	NodeStarted:   nil,
	NodeLog:       NodeLogData{},
	NodeSucceeded: NodeSucceededData{},
	NodeFailed:    NodeFailedData{},
	GateWaiting:   GateWaitingData{},
	GateDecided:   GateDecidedData{},
	RunFinished:   RunFinishedData{},
}

// Event is one entry in the log. Seq and At are set by the store.
type Event struct {
	Seq     int64          `json:"seq"`
	Run     string         `json:"run"`
	At      time.Time      `json:"at"`
	Type    Type           `json:"type"`
	Node    string         `json:"node,omitempty"`
	Attempt int            `json:"attempt,omitzero"`
	Data    jsontext.Value `json:"data,omitzero"`
}

type RunCreatedData struct {
	Graph      graph.Graph           `json:"graph"`
	Executor   string                `json:"executor"` // "fake" or "claude"
	FakeScript string                `json:"fake_script,omitempty"`
	Spec       string                `json:"spec,omitempty"`
	Guardrails guardrails.Guardrails `json:"guardrails,omitzero"`
	Commit     string                `json:"commit,omitempty"` // the server's code, with -dirty for uncommitted changes
	// AutoApprove makes the runtime approve every gate itself, as eval runs
	// need, since no human is there. The decision's notes say so.
	AutoApprove bool `json:"auto_approve,omitempty"`
	// PlantedPlan is the planner's JSON answer for an eval that plants a flawed plan.
	PlantedPlan string `json:"planted_plan,omitempty"`
}

// GraphUpdatedData is also how a plan node succeeds; CostUSD is that attempt's cost.
type GraphUpdatedData struct {
	Graph  graph.Graph `json:"graph"`
	Reason string      `json:"reason"`
	// Version numbers the graphs of a run. The graph in run_created is
	// version 1, and each graph_updated event is the next one.
	Version int `json:"version,omitzero"`
	// Replaced lists the tasks this update took out because a task failed.
	// An update with no Replaced is a plan filling a slot. Each one that has
	// Replaced counts against the run's replans.
	Replaced []string `json:"replaced,omitempty"`
	CostUSD  float64  `json:"cost_usd,omitzero"`
}

type NodeSucceededData struct {
	CostUSD  float64   `json:"cost_usd,omitzero"`
	Contract *Contract `json:"contract,omitempty"`
}

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

// NewContract builds a Contract from the file's text. It changes \r\n to \n,
// cuts the text at the last line end at or before MaxContractBytes, and finds Unclear.
func NewContract(markdown string, tests, red int) Contract {
	c := Contract{Markdown: strings.ReplaceAll(markdown, "\r\n", "\n"), Tests: tests, Red: red}
	if len(c.Markdown) > MaxContractBytes {
		c.Cut = true
		head := c.Markdown[:MaxContractBytes]
		if i := strings.LastIndexByte(head, '\n'); i >= 0 {
			c.Markdown = head[:i+1]
		} else {
			c.Markdown = strings.ToValidUTF8(head, "") // shortcut: one line over the limit is cut mid-line
		}
	}
	c.Unclear = findUnclear(c.Lines())
	return c
}

// Lines returns the lines of Markdown. A final \n does not start a line.
func (c Contract) Lines() []string {
	if c.Markdown == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(c.Markdown, "\n"), "\n")
}

// heading returns the level and text of a markdown heading line, or 0.
func heading(line string) (int, string) {
	n := len(line) - len(strings.TrimLeft(line, "#"))
	if n < 1 || n > 6 || !strings.HasPrefix(line[n:], " ") {
		return 0, ""
	}
	return n, line[n+1:]
}

func findUnclear(lines []string) LineRange {
	var r LineRange
	level, fence := 0, false
	end := 0 // the last line before the next heading of the same or a higher level
	for i, l := range lines {
		t := strings.TrimLeft(l, " ")
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = !fence
		}
		n, text := heading(l)
		if fence || n == 0 {
			continue
		}
		if r.Line == 0 && strings.Contains(strings.ToLower(text), "unclear") {
			r.Line, level = i+1, n
		} else if r.Line != 0 && n <= level {
			end = i
			break
		}
	}
	if r.Line == 0 {
		return r
	}
	if end == 0 {
		end = len(lines)
	}
	for end > r.Line && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	r.EndLine = end
	return r
}

type NodeLogData struct {
	Line string `json:"line"`
}

type FailureClass string

const (
	// Task failures are the node's own fault and count toward its retries.
	Task FailureClass = "task"
	// Infra failures (crash, restart, rate limit) do not.
	Infra FailureClass = "infra"
	// Limit failures mean the account reached its usage limit. They never retry.
	Limit FailureClass = "limit"
)

type NodeFailedData struct {
	Error     string       `json:"error"`
	Class     FailureClass `json:"class"`
	WillRetry bool         `json:"will_retry"`
	// Abandoned means an advisory node failed and will not retry. The node counts as done, and the run goes on.
	Abandoned bool    `json:"abandoned,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitzero"`
}

// GateWaitingData says what a rejection will do, so no reader repeats the runtime's rules.
type GateWaitingData struct {
	// RevisionsLeft is how many more times a rejection with notes sends the
	// work back to the nodes the gate waits for. At 0, a rejection ends the run.
	RevisionsLeft int        `json:"revisions_left"`
	Brief         *GateBrief `json:"brief,omitempty"` // nil in a log from before briefs
}

type GateDecidedData struct {
	Approved bool   `json:"approved"`
	Notes    string `json:"notes,omitempty"`
	// Comments are in line order. A log from before comments has none.
	Comments []GateComment `json:"comments,omitempty"`
	// Revise means the runtime sends a rejection back instead of failing the
	// run: the gate and the nodes it waits for run again, and the notes are
	// their feedback. The runtime decides this, so no reader repeats its rules.
	Revise bool `json:"revise,omitempty"`
}

// Feedback is the text the nodes before the gate read after a rejection.
func (d GateDecidedData) Feedback() string {
	var b strings.Builder
	b.WriteString("A person rejected your result at the gate.")
	if strings.TrimSpace(d.Notes) != "" || len(d.Comments) == 0 {
		b.WriteString(" Their notes: " + d.Notes)
	}
	if len(d.Comments) > 0 {
		b.WriteString("\n\nTheir comments on lines of CONTRACT.md:")
	}
	for _, c := range d.Comments {
		b.WriteString("\n\n")
		if c.EndLine > c.Line {
			fmt.Fprintf(&b, "Lines %d-%d:", c.Line, c.EndLine)
		} else {
			fmt.Fprintf(&b, "Line %d:", c.Line)
		}
		if c.Quote != "" {
			for l := range strings.SplitSeq(c.Quote, "\n") {
				b.WriteString("\n> " + l)
			}
		}
		b.WriteString("\n" + c.Body)
	}
	return b.String()
}

type RunStatus string

const (
	RunRunning   RunStatus = "running"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
)

type RunFinishedData struct {
	Status RunStatus `json:"status"`
	Reason string    `json:"reason,omitempty"`
}

// New builds an event, encoding data as JSON. Pass nil for no data.
func New(run string, t Type, node string, attempt int, data any) Event {
	e := Event{Run: run, Type: t, Node: node, Attempt: attempt}
	if data != nil {
		// The payloads hold only strings, numbers, bools and slices of them,
		// so the only way to fail is invalid UTF-8, such as a stray byte in
		// tool output. Replace it rather than lose the event's data.
		e.Data, _ = json.Marshal(data, jsontext.AllowInvalidUTF8(true))
	}
	return e
}

// Decode reads an event's data into a payload of type T. An event with no
// data gives T's zero value.
func (e Event) Decode[T any]() (T, error) {
	var v T
	if len(e.Data) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(e.Data, &v); err != nil {
		return v, fmt.Errorf("event %d (%s): %w", e.Seq, e.Type, err)
	}
	return v, nil
}

type NodeStatus string

const (
	Pending   NodeStatus = "pending"
	Running   NodeStatus = "running"
	Waiting   NodeStatus = "waiting" // a gate waiting for a decision
	Succeeded NodeStatus = "succeeded"
	Failed    NodeStatus = "failed"
)

type NodeState struct {
	Status        NodeStatus
	Attempt       int // the last attempt that started
	TaskFailures  int
	InfraFailures int
	LastError     string
	LastClass     FailureClass
	LastTaskError string // the last failure that was the node's own, the feedback for its retry
	Revised       bool   // a person sent the node's work back; set until the node starts again
	CostUSD       float64
	Contract      *Contract     // an agent: from its last node_succeeded; a send-back clears it
	Emitted       []string      // a plan node: the ids of the nodes its graph_updated added, in graph order
	Brief         *GateBrief    // a gate: from its last gate_waiting; a send-back keeps it for the next round
	Sent          []GateComment // a gate: the comments of its last send-back; gate_waiting clears it
}

// State is a run as its events describe it.
type State struct {
	Run         string
	Graph       graph.Graph
	Executor    string
	FakeScript  string
	Spec        string
	Rails       guardrails.Guardrails
	Commit      string
	AutoApprove bool
	PlantedPlan string
	CostUSD     float64
	Status      RunStatus
	Reason      string
	Nodes       map[string]*NodeState
	// GraphVersion is the version of Graph. Replans counts the updates that
	// replaced tasks. Retired holds the id of every node that an update
	// replaced: a later node may not reuse one, because its worktree, branch
	// and session belong to the old node.
	GraphVersion int
	Replans      int
	// Revisions counts the rejections that sent work back.
	Revisions int
	// ReplanReason is why the last replan happened.
	ReplanReason string
	Retired      map[string]bool
}

// gateDeps returns the ids the gate waits for.
func (s *State) gateDeps(gate string) []string {
	if n, ok := s.Graph.Node(gate); ok {
		return n.Deps
	}
	return nil
}

func (s *State) spend(n *NodeState, usd float64) {
	n.CostUSD += usd
	s.CostUSD += usd
}

// Fold rebuilds a run's state from its events, in order.
func Fold(events []Event) (*State, error) {
	s := &State{}
	for _, e := range events {
		if err := s.Apply(e); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Apply updates the state with one event.
func (s *State) Apply(e Event) error {
	switch e.Type {
	case RunCreated:
		d, err := e.Decode[RunCreatedData]()
		if err != nil {
			return err
		}
		*s = State{Run: e.Run, Graph: d.Graph, Executor: d.Executor, FakeScript: d.FakeScript,
			Spec: d.Spec, Rails: d.Guardrails, Commit: d.Commit, AutoApprove: d.AutoApprove, PlantedPlan: d.PlantedPlan, Status: RunRunning, Nodes: map[string]*NodeState{}, GraphVersion: 1, Retired: map[string]bool{}}
		for _, n := range d.Graph.Nodes {
			s.Nodes[n.ID] = &NodeState{Status: Pending}
		}
		return nil
	case GraphUpdated:
		d, err := e.Decode[GraphUpdatedData]()
		if err != nil {
			return err
		}
		s.Graph = d.Graph
		s.GraphVersion = cmp.Or(d.Version, s.GraphVersion+1) // logs from before versions have none
		if len(d.Replaced) > 0 {
			s.Replans++
			s.ReplanReason = d.Reason
		}
		for _, id := range d.Replaced {
			s.Retired[id] = true
			delete(s.Nodes, id)
		}
		nodes := make(map[string]*NodeState, len(d.Graph.Nodes))
		var emitted []string
		for _, n := range d.Graph.Nodes {
			if s.Nodes[n.ID] == nil {
				emitted = append(emitted, n.ID)
			}
			nodes[n.ID] = cmp.Or(s.Nodes[n.ID], &NodeState{Status: Pending})
		}
		s.Nodes = nodes
		// A plan node succeeds in the same event that adds its nodes, so a
		// crash cannot leave one write done and the other missing.
		if n, ok := s.Nodes[e.Node]; ok {
			n.Status, n.Emitted = Succeeded, emitted
			s.spend(n, d.CostUSD)
		}
		return nil
	case RunFinished:
		d, err := e.Decode[RunFinishedData]()
		if err != nil {
			return err
		}
		s.Status, s.Reason = d.Status, d.Reason
		return nil
	case NodeLog:
		return nil
	}

	n, ok := s.Nodes[e.Node]
	if !ok {
		return fmt.Errorf("event %d (%s): unknown node %q", e.Seq, e.Type, e.Node)
	}
	switch e.Type {
	case NodeStarted:
		n.Status, n.Attempt, n.Revised = Running, e.Attempt, false
	case NodeSucceeded:
		d, err := e.Decode[NodeSucceededData]()
		if err != nil {
			return err
		}
		n.Status, n.Contract = Succeeded, d.Contract
		s.spend(n, d.CostUSD)
	case NodeFailed:
		d, err := e.Decode[NodeFailedData]()
		if err != nil {
			return err
		}
		switch d.Class {
		case Infra:
			n.InfraFailures++
		case Task:
			n.TaskFailures++
			n.LastTaskError = d.Error
		}
		n.LastError, n.LastClass = d.Error, d.Class
		s.spend(n, d.CostUSD)
		n.Status = Failed
		switch {
		case d.WillRetry:
			n.Status = Pending
		case d.Abandoned:
			n.Status = Succeeded
		}
	case GateWaiting:
		d, err := e.Decode[GateWaitingData]()
		if err != nil {
			return err
		}
		n.Status, n.Brief, n.Sent = Waiting, d.Brief, nil
	case GateDecided:
		d, err := e.Decode[GateDecidedData]()
		if err != nil {
			return err
		}
		n.Status = Succeeded
		switch {
		case d.Approved:
		case d.Revise:
			s.Revisions++
			n.Status, n.Sent = Pending, d.Comments
			for _, id := range s.gateDeps(e.Node) {
				if dep := s.Nodes[id]; dep != nil {
					*dep = NodeState{Status: Pending, Attempt: dep.Attempt, CostUSD: dep.CostUSD, Revised: true,
						LastTaskError: d.Feedback()}
				}
			}
		default:
			n.Status = Failed
			n.LastError = "rejected: " + d.Notes
		}
	default:
		return fmt.Errorf("event %d: unknown type %q", e.Seq, e.Type)
	}
	return nil
}
