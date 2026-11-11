package event

import (
	"slices"
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

func TestNewContract(t *testing.T) {
	long := strings.Repeat("0123456789\n", MaxContractBytes/11+2)
	tests := []struct {
		name, md string
		unclear  LineRange
		cut      bool
	}{
		{"crlf", "# A\r\nb\r\n", LineRange{}, false},
		{"any case", "# A\n\n## UNCLEAR bits\n\n- x\n\n\n## Next\ny\n", LineRange{3, 5}, false},
		{"deeper heading stays inside", "## Unclear\n### Sub\nx\n# Top\n", LineRange{1, 3}, false},
		{"last line", "x\n# Unclear\ny", LineRange{2, 3}, false},
		{"fence", "```\n## Unclear\n```\n", LineRange{}, false},
		{"none", "# A\n## B\n", LineRange{}, false},
		{"cut", long, LineRange{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewContract(tt.md, 3, 2)
			if c.Unclear != tt.unclear || c.Cut != tt.cut || c.Tests != 3 || c.Red != 2 {
				t.Errorf("got %+v, want unclear %v cut %v", c, tt.unclear, tt.cut)
			}
			if strings.Contains(c.Markdown, "\r") {
				t.Error("kept \\r")
			}
			if tt.cut && (len(c.Markdown) > MaxContractBytes || !strings.HasSuffix(c.Markdown, "\n")) {
				t.Errorf("cut at %d bytes, want whole lines within the limit", len(c.Markdown))
			}
		})
	}
	if got := NewContract("a\nb\n", 0, 0).Lines(); len(got) != 2 || got[1] != "b" {
		t.Errorf("lines = %q", got)
	}
}

func TestFeedback(t *testing.T) {
	one := GateComment{Line: 5, Quote: "- **Empty query.** The spec does not say what `query \"\"` returns. The tests expect `ErrEmpty`.", Body: "Return every record. An empty query is normal for us."}
	rng := GateComment{Line: 21, EndLine: 23, Quote: "1. `TestPutGet`: a stored record comes back.\n2. `TestGetMissing`: a missing key gives `ErrNotFound`.\n3. `TestParseQuery/empty_input`: an empty query gives `ErrEmpty`.", Body: "Add a test for a key with a space in it."}
	const head = "A person rejected your result at the gate."
	const block = "\n\nTheir comments on lines of CONTRACT.md:\n\nLine 5:\n> " + "- **Empty query.** The spec does not say what `query \"\"` returns. The tests expect `ErrEmpty`.\nReturn every record. An empty query is normal for us."
	const rangeBlock = "\n\nLines 21-23:\n> 1. `TestPutGet`: a stored record comes back.\n> 2. `TestGetMissing`: a missing key gives `ErrNotFound`.\n> 3. `TestParseQuery/empty_input`: an empty query gives `ErrEmpty`.\nAdd a test for a key with a space in it."
	tests := []struct {
		d    GateDecidedData
		want string
	}{
		{GateDecidedData{Notes: "x"}, head + " Their notes: x"},
		{GateDecidedData{}, head + " Their notes: "},
		{GateDecidedData{Notes: "The empty case is wrong.", Comments: []GateComment{one, rng}}, head + " Their notes: The empty case is wrong." + block + rangeBlock},
		{GateDecidedData{Notes: " ", Comments: []GateComment{rng}}, head + "\n\nTheir comments on lines of CONTRACT.md:" + rangeBlock},
	}
	for i, tt := range tests {
		if got := tt.d.Feedback(); got != tt.want {
			t.Errorf("%d: got %q\nwant %q", i, got, tt.want)
		}
	}
}

func TestFoldGateAndContract(t *testing.T) {
	g := graph.Graph{Name: "g", Nodes: []graph.Node{
		{ID: "arch", Kind: graph.Agent},
		{ID: "gate", Kind: graph.Gate, Deps: []string{"arch"}},
		{ID: "plan", Kind: graph.Plan, Deps: []string{"gate"}},
		{ID: "slot", Kind: graph.Slot, FilledBy: "plan"},
	}}
	g2 := g
	g2.Nodes = append(slices.Clone(g.Nodes), graph.Node{ID: "t1", Kind: graph.Agent}, graph.Node{ID: "t2", Kind: graph.Agent})
	c := NewContract("# C\n", 1, 1)
	brief := &GateBrief{Round: 1, Check: []string{"x"}}
	cm := []GateComment{{Line: 1, Body: "b"}}
	fold := func(evs ...Event) *State {
		t.Helper()
		st, err := Fold(append([]Event{New("r", RunCreated, "", 0, RunCreatedData{Graph: g})}, evs...))
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	st := fold(New("r", NodeSucceeded, "arch", 1, NodeSucceededData{Contract: &c}),
		New("r", GateWaiting, "gate", 0, GateWaitingData{Brief: brief}),
		New("r", GraphUpdated, "plan", 1, GraphUpdatedData{Graph: g2}))
	if st.Nodes["arch"].Contract.Markdown != "# C\n" || st.Nodes["gate"].Brief.Round != 1 || !slices.Equal(st.Nodes["plan"].Emitted, []string{"t1", "t2"}) {
		t.Errorf("fold lost the contract, brief or Emitted: %+v", st.Nodes)
	}

	st = fold(New("r", NodeSucceeded, "arch", 1, NodeSucceededData{Contract: &c}),
		New("r", GateWaiting, "gate", 0, GateWaitingData{Brief: brief}),
		New("r", GateDecided, "gate", 0, GateDecidedData{Notes: "x", Comments: cm, Revise: true}))
	if st.Nodes["arch"].Contract != nil || st.Nodes["gate"].Brief == nil || len(st.Nodes["gate"].Sent) != 1 {
		t.Errorf("after a send-back: %+v", st.Nodes)
	}
	if got := st.Nodes["arch"].LastTaskError; !strings.Contains(got, "Line 1:") {
		t.Errorf("feedback = %q", got)
	}
	st = fold(New("r", GateWaiting, "gate", 0, GateWaitingData{}))
	if st.Nodes["gate"].Sent != nil {
		t.Error("gate_waiting must clear Sent")
	}
}

func TestOldLogFolds(t *testing.T) {
	g := graph.Graph{Name: "g", Nodes: []graph.Node{{ID: "arch", Kind: graph.Agent}, {ID: "gate", Kind: graph.Gate, Deps: []string{"arch"}}}}
	for _, wd := range []any{nil, GateWaitingData{RevisionsLeft: 1}} {
		st, err := Fold([]Event{
			New("r", RunCreated, "", 0, RunCreatedData{Graph: g}),
			New("r", NodeSucceeded, "arch", 1, NodeSucceededData{CostUSD: 1}),
			New("r", GateWaiting, "gate", 0, wd),
			New("r", GateDecided, "gate", 0, GateDecidedData{Notes: "x", Revise: true}),
		})
		if err != nil {
			t.Fatal(err)
		}
		if st.Nodes["gate"].Brief != nil || st.Nodes["arch"].Contract != nil {
			t.Error("an old log has no brief or contract")
		}
		if want := "A person rejected your result at the gate. Their notes: x"; st.Nodes["arch"].LastTaskError != want {
			t.Errorf("LastTaskError = %q", st.Nodes["arch"].LastTaskError)
		}
	}
}
