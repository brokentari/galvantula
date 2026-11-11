package runner

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/brokentari/galvantula/internal/event"
	"github.com/brokentari/galvantula/internal/executor"
	"github.com/brokentari/galvantula/internal/graph"
	"github.com/brokentari/galvantula/internal/guardrails"
)

const (
	sentUnclear = `Read "Unclear in the spec" first. Each item is a choice the architect made where the spec does not say. Comment on a choice that is wrong.`
	sentNone    = "The contract names nothing unclear in the spec. Check that the spec leaves nothing open."
	sentTests   = "Check that the tests ask for what you want. Builders cannot change a test, and a wrong test fails the run."
	sentRed     = "2 of 12 tests pass on the stubs. A test that passes on a stub can test nothing."
	sentCut     = "The gate shows the first 64 KB of the contract. Read the rest in CONTRACT.md in the run's repo."
	sentSame    = "The architect changed nothing in the contract."
	sentLock    = "Approve locks these tests: no builder can change them."
)

// contractState is a run with an architect that has a contract and the gate after it.
func contractState(c *event.Contract, gate *event.NodeState) *event.State {
	return &event.State{
		Graph: graph.Graph{Nodes: []graph.Node{{ID: "a", Kind: graph.Agent}, {ID: "g", Kind: graph.Gate, Deps: []string{"a"}}}},
		Nodes: map[string]*event.NodeState{"a": {Contract: c}, "g": gate},
	}
}

func contractOf(md string) *event.Contract {
	c := event.NewContract(md, 12, 12)
	return &c
}

func TestContractGateBrief(t *testing.T) {
	for name, tt := range map[string]struct {
		edit  func(*event.Contract)
		check []string
	}{
		"clean":      {func(c *event.Contract) {}, []string{sentUnclear, sentTests}},
		"no unclear": {func(c *event.Contract) { c.Unclear = event.LineRange{} }, []string{sentNone, sentTests}},
		"red":        {func(c *event.Contract) { c.Red = 10 }, []string{sentUnclear, sentTests, sentRed}},
		"cut":        {func(c *event.Contract) { c.Cut = true }, []string{sentUnclear, sentTests, sentCut}},
	} {
		t.Run(name, func(t *testing.T) {
			c := contractOf("# C\n\n## Unclear in the spec\n\n- x\n")
			tt.edit(c)
			b := gateBrief(contractState(c, &event.NodeState{}), "g")
			if b.Round != 1 || b.Contract != c || b.Changes != nil || b.Tasks != nil || b.Approve != sentLock || !slices.Equal(b.Check, tt.check) {
				t.Errorf("got %+v", b)
			}
		})
	}
}

func TestTaskGateBrief(t *testing.T) {
	st := &event.State{
		CostUSD: 0.42,
		Graph: graph.Graph{Nodes: []graph.Node{
			{ID: "plan", Kind: graph.Plan},
			{ID: "store", Title: "internal/store", Kind: graph.Agent, Scope: []string{"internal/store"}, Retries: 2, Deps: []string{"plan", "gate0"}},
			{ID: "cli", Kind: graph.Agent, Scope: []string{"cmd/cli"}, Deps: []string{"store", "gate0", "other"}},
			{ID: "gate0", Kind: graph.Gate},
			{ID: "g", Kind: graph.Gate, Deps: []string{"plan"}},
		}},
		Nodes: map[string]*event.NodeState{"plan": {Emitted: []string{"store", "cli"}}, "g": {}},
	}
	rows := func(b *event.GateBrief) string { return fmt.Sprintf("%+v", b.Tasks) }
	for name, tt := range map[string]struct {
		budget  guardrails.Budget
		approve string
		cap     float64
	}{
		"both":  {guardrails.Budget{NodeUSD: 3, RunUSD: 12}, "Each attempt of a task may spend up to $3.00. The run starts no new node after it has spent $12.00; it has spent $0.42.", 9},
		"node":  {guardrails.Budget{NodeUSD: 3}, "Each attempt of a task may spend up to $3.00.", 9},
		"run":   {guardrails.Budget{RunUSD: 12}, "The run starts no new node after it has spent $12.00; it has spent $0.42.", 0},
		"empty": {guardrails.Budget{}, "", 0},
	} {
		t.Run(name, func(t *testing.T) {
			st.Rails.Budget = tt.budget
			b := gateBrief(st, "g")
			want := fmt.Sprintf("[{ID:store Title:internal/store Owns:[internal/store] After:[] Retries:2 CapUSD:%g} {ID:cli Title: Owns:[cmd/cli] After:[store] Retries:0 CapUSD:%g}]", tt.cap, tt.budget.NodeUSD)
			if rows(b) != want || b.Approve != tt.approve || b.Contract != nil || len(b.Check) != 1 || !strings.HasPrefix(b.Check[0], "Check that each task owns") {
				t.Errorf("got %s / %q / %v", rows(b), b.Approve, b.Check)
			}
		})
	}
}

func TestToolGateBrief(t *testing.T) {
	st := &event.State{
		Graph: graph.Graph{Nodes: []graph.Node{{ID: "t", Kind: graph.Tool}, {ID: "g", Kind: graph.Gate, Deps: []string{"t"}}}},
		Nodes: map[string]*event.NodeState{"t": {}, "g": {Brief: &event.GateBrief{Round: 2}}},
	}
	b := gateBrief(st, "g")
	if b.Round != 3 || len(b.Check) != 0 || b.Check == nil || b.Approve != "" || b.Contract != nil || b.Tasks != nil {
		t.Errorf("got %+v", b)
	}
}

func TestRoundTwoBrief(t *testing.T) {
	old := contractOf("# C\n\n## Unclear in the spec\n\n- one\n- two\n- three\n")
	sent := []event.GateComment{
		{Line: 5, Quote: "- one", Body: "changed line"},
		{Line: 7, EndLine: 7, Quote: "- three", Body: "kept line"},
	}
	gate := &event.NodeState{Brief: &event.GateBrief{Round: 1, Contract: old}, Sent: sent}
	now := contractOf("# C\n\n## Unclear in the spec\n\n- uno\n- two\n- three\n- four\n")
	b := gateBrief(contractState(now, gate), "g")
	want := &event.ContractChanges{
		Added:   []int{5, 8},
		Removed: []event.RemovedLine{{After: 4, Text: "- one"}},
		Earlier: []event.EarlierComment{
			{Line: 0, WasLine: 5, Quote: "- one", Body: "changed line"},
			{Line: 7, WasLine: 7, Quote: "- three", Body: "kept line"},
		},
	}
	if b.Round != 2 || fmt.Sprint(b.Changes) != fmt.Sprint(want) || slices.Contains(b.Check, sentSame) {
		t.Errorf("got round %d, changes %+v", b.Round, b.Changes)
	}

	// A line that moves is found nearest to where it was; the smaller line wins a tie.
	moved := &event.NodeState{Brief: &event.GateBrief{Round: 1, Contract: contractOf("a\nx\nb\nx\nc\n")}, Sent: []event.GateComment{{Line: 4, Body: "m"}}}
	b = gateBrief(contractState(contractOf("x\nx\nx\nx\nx\n"), moved), "g")
	if got := b.Changes.Earlier[0].Line; got != 4 {
		t.Errorf("nearest run starts at %d, want 4", got)
	}
	tie := &event.NodeState{Brief: &event.GateBrief{Round: 1, Contract: contractOf("x\nb\nc\n")}, Sent: []event.GateComment{{Line: 2, EndLine: 2, Body: "m"}}}
	b = gateBrief(contractState(contractOf("b\nz\nb\n"), tie), "g")
	if got := b.Changes.Earlier[0].Line; got != 1 {
		t.Errorf("tie starts at %d, want 1", got)
	}

	same := gateBrief(contractState(old, &event.NodeState{Brief: &event.GateBrief{Round: 1, Contract: old}}), "g")
	if same.Round != 2 || !slices.Contains(same.Check, sentSame) || len(same.Changes.Added) != 0 || len(same.Changes.Removed) != 0 {
		t.Errorf("unchanged contract: %+v", same)
	}
}

func TestDiffLines(t *testing.T) {
	long := func(p string) []string {
		var s []string
		for i := range 2100 {
			s = append(s, fmt.Sprint(p, i))
		}
		return s
	}
	for name, tt := range map[string]struct {
		old, now []string
		added    []int
		removed  []event.RemovedLine
	}{
		"equal":          {[]string{"a", "b"}, []string{"a", "b"}, []int{}, []event.RemovedLine{}},
		"one changed":    {[]string{"a", "b", "c"}, []string{"a", "x", "c"}, []int{2}, []event.RemovedLine{{After: 1, Text: "b"}}},
		"added at start": {[]string{"a", "b"}, []string{"n", "a", "b"}, []int{1}, []event.RemovedLine{}},
		"removed at end": {[]string{"a", "b", "c"}, []string{"a", "b"}, []int{}, []event.RemovedLine{{After: 2, Text: "c"}}},
		"over the limit": {long("o"), long("n")[:2100], func() []int {
			var s []int
			for i := range 2100 {
				s = append(s, i+1)
			}
			return s
		}(), func() []event.RemovedLine {
			var s []event.RemovedLine
			for _, l := range long("o") {
				s = append(s, event.RemovedLine{Text: l})
			}
			return s
		}()},
	} {
		t.Run(name, func(t *testing.T) {
			added, removed := diffLines(tt.old, tt.now)
			if !slices.Equal(added, tt.added) || !slices.Equal(removed, tt.removed) {
				t.Errorf("got added %v, removed %v", added, removed)
			}
		})
	}
}

func TestCheckComments(t *testing.T) {
	c := contractOf("one\ntwo\nthree\n")
	c3 := func(cs ...event.GateComment) []event.GateComment { return cs }
	many := make([]event.GateComment, event.MaxComments+1)
	for i := range many {
		many[i] = event.GateComment{Line: 1, Body: "x"}
	}
	for name, tt := range map[string]struct {
		in   []event.GateComment
		c    *event.Contract
		want string
	}{
		"line 0":       {c3(event.GateComment{Line: 1, Body: "x"}, event.GateComment{Line: 0, Body: "x"}), c, "comment 2: line 0 is not a line of the contract (lines count from 1)"},
		"past the end": {c3(event.GateComment{Line: 4, Body: "x"}), c, "comment 1: line 4 is past the end of the contract (3 lines)"},
		"end before":   {c3(event.GateComment{Line: 3, EndLine: 2, Body: "x"}), c, "comment 1: end line 2 is before line 3"},
		"end past":     {c3(event.GateComment{Line: 3, EndLine: 9, Body: "x"}), c, "comment 1: end line 9 is past the end of the contract (3 lines)"},
		"blank body":   {c3(event.GateComment{Line: 1, Body: " \n"}), c, "comment 1: the comment is empty"},
		"long body":    {c3(event.GateComment{Line: 1, Body: strings.Repeat("x", event.MaxCommentBytes+1)}), c, "comment 1: the comment is 4001 bytes long; the most is 4000"},
		"101 comments": {many, c, "101 comments is too many: the most is 100"},
		"no contract":  {c3(event.GateComment{Line: 1, Body: "x"}), nil, "this gate has no contract, so it takes no comments on lines"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := checkComments(tt.in, tt.c)
			if !errors.Is(err, ErrInvalid) || err.Error() != tt.want {
				t.Errorf("got %v", err)
			}
		})
	}

	long := contractOf(strings.Repeat("é", event.MaxQuoteBytes) + "\nb\nc\n") // 2 bytes a rune
	got, err := checkComments(c3(
		event.GateComment{Line: 3, Quote: "forged", Body: "x"},
		event.GateComment{Line: 1, EndLine: 2, Body: "y"},
		event.GateComment{Line: 1, Body: "z"}), long)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Body != "y" || got[1].Body != "z" || got[2].Body != "x" || got[2].Quote != "c" ||
		got[0].Quote != strings.Repeat("é", event.MaxQuoteBytes/2) || got[1].Quote != got[0].Quote {
		t.Errorf("got %+v", got)
	}
}

// architect returns a contract per attempt.
type architect struct{ md []string }

func (a architect) Run(_ context.Context, t executor.Task, _ func(string)) (executor.Result, error) {
	if t.Node.ID != "a" {
		return executor.Result{}, nil
	}
	c := event.NewContract(a.md[min(t.Attempt, len(a.md))-1], 3, 3)
	return executor.Result{Contract: &c}, nil
}

func startArchitect(t *testing.T, revisions int, x executor.Executor) (*Runner, *event.Store, string) {
	t.Helper()
	store := openStore(t)
	r := New(t.Context(), store, 3)
	r.Executor = func(*event.State) (executor.Executor, error) { return x, nil }
	rails := guardrails.Guardrails{Budget: guardrails.Budget{Revisions: revisions}}
	id, err := r.Start(Run{Graph: mustParse(t, reviseGraph), Executor: "fake", Rails: rails})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, store, id, "the gate waits", nodeIs("g", event.Waiting))
	return r, store, id
}

func TestRestartKeepsTheBrief(t *testing.T) {
	_, store, id := startArchitect(t, 1, architect{[]string{"# C\n\n## Unclear in the spec\n\n- x\n"}})
	evs, err := store.Events(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	i := index(evs, event.GateWaiting, "g")
	waiting, err := evs[i].Decode[event.GateWaitingData]()
	if err != nil {
		t.Fatal(err)
	}
	st, err := event.Fold(evs[:i])
	if err != nil {
		t.Fatal(err)
	}
	if got := gateBrief(st, "g"); got.Contract == nil || !reflect.DeepEqual(got, waiting.Brief) {
		t.Errorf("rebuilt %+v, logged %+v", got, waiting.Brief)
	}
}

func TestCommentsAtAGate(t *testing.T) {
	r, store, id := startArchitect(t, 1, architect{[]string{"one\ntwo\nthree\nfour\nfive\n", "one\ntwo\nthree\nfour\nfive\nsix\n"}})
	before, _ := store.Events(id, 0)
	err := r.Decide(id, "g", false, "", []event.GateComment{{Line: 9, Body: "x"}})
	if !errors.Is(err, ErrInvalid) || err.Error() != "comment 1: line 9 is past the end of the contract (5 lines)" {
		t.Fatalf("got %v", err)
	}
	if after, _ := store.Events(id, 0); len(after) != len(before) {
		t.Error("an invalid decision wrote an event")
	}

	// One comment and no notes sends the work back.
	if err := r.Decide(id, "g", false, "", []event.GateComment{{Line: 5, Quote: "forged", Body: "fix five"}}); err != nil {
		t.Fatal(err)
	}
	st, evs := waitUntil(t, store, id, "the gate waits again", func(st *event.State, _ []event.Event) bool {
		return st.Revisions == 1 && st.Nodes["g"].Status == event.Waiting && st.Nodes["a"].Attempt == 2
	})
	d, err := evs[index(evs, event.GateDecided, "g")].Decode[event.GateDecidedData]()
	if err != nil || !d.Revise || d.Comments[0].Quote != "five" {
		t.Errorf("decision %+v, err %v", d, err)
	}
	if fb := d.Feedback(); !strings.Contains(fb, "Line 5:\n> five") {
		t.Errorf("feedback %q", fb)
	}
	b := st.Nodes["g"].Brief
	if b.Round != 2 || b.Changes == nil || fmt.Sprint(b.Changes.Added) != "[6]" || len(b.Changes.Earlier) != 1 || b.Changes.Earlier[0].Line != 5 {
		t.Errorf("round 2 brief %+v", b)
	}
}
