package event

import (
	"strings"
	"testing"

	"github.com/brokentari/galvantula/internal/graph"
)

// A stray byte in tool output must not cost the event its data.
func TestNewKeepsDataWithInvalidUTF8(t *testing.T) {
	e := New("r", NodeLog, "a", 1, NodeLogData{Line: "ok \xff done"})
	d, err := e.Decode[NodeLogData]()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(d.Line, "ok ") || !strings.HasSuffix(d.Line, " done") {
		t.Errorf("line = %q, want the text around the bad byte kept", d.Line)
	}
}

func TestReplanUpdateResetsNodes(t *testing.T) {
	g := func(ids ...string) graph.Graph {
		out := graph.Graph{Name: "g"}
		for _, id := range ids {
			out.Nodes = append(out.Nodes, graph.Node{ID: id, Kind: graph.Agent})
		}
		return out
	}
	evs := []Event{
		New("r", RunCreated, "", 0, RunCreatedData{Graph: g("a", "b")}),
		New("r", NodeFailed, "a", 1, NodeFailedData{Error: "boom", Class: Task}),
		New("r", GraphUpdated, "", 0, GraphUpdatedData{Graph: g("b", "a2"), Version: 2, Replaced: []string{"a"}}),
	}
	st, err := Fold(evs)
	if err != nil {
		t.Fatal(err)
	}
	if st.GraphVersion != 2 || st.Replans != 1 || !st.Retired["a"] {
		t.Errorf("version %d, replans %d, retired %v", st.GraphVersion, st.Replans, st.Retired)
	}
	if _, ok := st.Nodes["a"]; ok || st.Nodes["a2"].Status != Pending {
		t.Errorf("nodes after the replan: %v", st.Nodes)
	}
}
