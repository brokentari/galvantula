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
	GateWaiting:   nil,
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
}

// GraphUpdatedData is also how a plan node succeeds; CostUSD is that attempt's cost.
type GraphUpdatedData struct {
	Graph   graph.Graph `json:"graph"`
	Reason  string      `json:"reason"`
	CostUSD float64     `json:"cost_usd,omitzero"`
}

type NodeSucceededData struct {
	CostUSD float64 `json:"cost_usd,omitzero"`
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
	// Abandoned means an advisory node failed for good. It counts as done, and the run goes on.
	Abandoned bool    `json:"abandoned,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitzero"`
}

type GateDecidedData struct {
	Approved bool   `json:"approved"`
	Notes    string `json:"notes,omitempty"`
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
	CostUSD       float64
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
	CostUSD     float64
	Status      RunStatus
	Reason      string
	Nodes       map[string]*NodeState
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
			Spec: d.Spec, Rails: d.Guardrails, Commit: d.Commit, AutoApprove: d.AutoApprove, Status: RunRunning, Nodes: map[string]*NodeState{}}
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
		nodes := make(map[string]*NodeState, len(d.Graph.Nodes))
		for _, n := range d.Graph.Nodes {
			nodes[n.ID] = cmp.Or(s.Nodes[n.ID], &NodeState{Status: Pending})
		}
		s.Nodes = nodes
		// A plan node succeeds in the same event that adds its nodes, so a
		// crash cannot leave one write done and the other missing.
		if n, ok := s.Nodes[e.Node]; ok {
			n.Status = Succeeded
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
		n.Status, n.Attempt = Running, e.Attempt
	case NodeSucceeded:
		d, err := e.Decode[NodeSucceededData]()
		if err != nil {
			return err
		}
		n.Status = Succeeded
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
		n.Status = Waiting
	case GateDecided:
		d, err := e.Decode[GateDecidedData]()
		if err != nil {
			return err
		}
		n.Status = Succeeded
		if !d.Approved {
			n.Status = Failed
			n.LastError = "rejected: " + d.Notes
		}
	default:
		return fmt.Errorf("event %d: unknown type %q", e.Seq, e.Type)
	}
	return nil
}
