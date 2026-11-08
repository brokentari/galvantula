package server

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/brokentari/galvantula/internal/event"
)

// Snapshot is a run as one JSON object, for clients that do not want to
// replay the event stream: the MCP server reads it.
type Snapshot struct {
	ID           string          `json:"id"`
	Graph        string          `json:"graph"`
	Status       event.RunStatus `json:"status"`
	Reason       string          `json:"reason,omitempty"`
	CostUSD      float64         `json:"cost_usd"`
	GraphVersion int             `json:"graph_version"`
	Replans      int             `json:"replans"`
	Revisions    int             `json:"revisions"`
	Nodes        []NodeSnapshot  `json:"nodes"`
	// Gates lists the gates that wait for a decision.
	Gates []GateSnapshot `json:"gates,omitempty"`
}

type NodeSnapshot struct {
	ID        string           `json:"id"`
	Title     string           `json:"title,omitempty"`
	Kind      string           `json:"kind"`
	Status    event.NodeStatus `json:"status"`
	Attempt   int              `json:"attempt,omitzero"`
	LastError string           `json:"last_error,omitempty"`
}

// GateSnapshot is a gate that waits for a person. Context is what the nodes
// before it logged: the contract, or the list of tasks and their cost.
type GateSnapshot struct {
	Node    string   `json:"node"`
	Title   string   `json:"title,omitempty"`
	Context []string `json:"context"`
}

// maxGateContext caps the lines of a gate's context.
const maxGateContext = 300

// snapshot builds a run's snapshot from its events.
func snapshot(evs []event.Event) (Snapshot, error) {
	st, err := event.Fold(evs)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{ID: st.Run, Graph: st.Graph.Name, Status: st.Status, Reason: st.Reason, CostUSD: st.CostUSD,
		GraphVersion: st.GraphVersion, Replans: st.Replans, Revisions: st.Revisions}
	if d, err := evs[0].Decode[event.RunCreatedData](); err == nil {
		snap.Graph = d.Graph.Name // a replan does not rename the graph, and this is the name the run started with
	}
	for _, n := range st.Graph.Nodes {
		ns := st.Nodes[n.ID]
		snap.Nodes = append(snap.Nodes, NodeSnapshot{ID: n.ID, Title: n.Title, Kind: string(n.Kind), Status: ns.Status, Attempt: ns.Attempt, LastError: ns.LastError})
		if ns.Status == event.Waiting {
			snap.Gates = append(snap.Gates, GateSnapshot{Node: n.ID, Title: n.Title, Context: logLines(evs, n.Deps, maxGateContext)})
		}
	}
	return snap, nil
}

// logLines returns the last n log lines of the nodes, oldest first. With no
// nodes it returns the lines of every node, each with its node's id in front.
func logLines(evs []event.Event, nodes []string, n int) []string {
	var lines []string
	for _, e := range evs {
		if e.Type != event.NodeLog || (len(nodes) > 0 && !slices.Contains(nodes, e.Node)) {
			continue
		}
		d, err := e.Decode[event.NodeLogData]()
		if err != nil {
			continue
		}
		if len(nodes) == 0 {
			d.Line = e.Node + ": " + d.Line
		}
		lines = append(lines, d.Line)
	}
	return lines[max(len(lines)-n, 0):]
}

func (s *server) getState(w http.ResponseWriter, r *http.Request) {
	evs, ok := s.runEvents(w, r)
	if !ok {
		return
	}
	snap, err := snapshot(evs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// getLogs returns the last ?tail= (default 50) log lines of ?node=, or of every node.
func (s *server) getLogs(w http.ResponseWriter, r *http.Request) {
	evs, ok := s.runEvents(w, r)
	if !ok {
		return
	}
	tail := 50
	if v := r.URL.Query().Get("tail"); v != "" {
		var err error
		if tail, err = strconv.Atoi(v); err != nil || tail < 1 || tail > 1000 {
			http.Error(w, "tail must be a number from 1 to 1000", http.StatusBadRequest)
			return
		}
	}
	var nodes []string
	if node := r.URL.Query().Get("node"); node != "" {
		nodes = []string{node}
	}
	writeJSON(w, http.StatusOK, map[string][]string{"lines": logLines(evs, nodes, tail)})
}

// runEvents reads the events of the run in the path, or answers 404.
func (s *server) runEvents(w http.ResponseWriter, r *http.Request) ([]event.Event, bool) {
	run := r.PathValue("run")
	evs, err := s.store.Events(run, 0)
	switch {
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, false
	case len(evs) == 0:
		http.Error(w, fmt.Sprintf("no run %q", run), http.StatusNotFound)
		return nil, false
	}
	return evs, true
}
