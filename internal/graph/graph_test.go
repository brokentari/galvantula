package graph

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestFactoryGraphIsValid(t *testing.T) {
	data, err := os.ReadFile("../../graphs/factory.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(data); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"unknown field", "name: g\nnodes:\n  - {id: a, kind: tool, colour: red}", "field colour not found"},
		{"no nodes", "name: g\nnodes: []", "graph has no nodes"},
		{"bad id", "name: g\nnodes:\n  - {id: A_b, kind: tool}", "id must match"},
		{"duplicate id", "name: g\nnodes:\n  - {id: a, kind: tool}\n  - {id: a, kind: tool}", "duplicate id"},
		{"unknown kind", "name: g\nnodes:\n  - {id: a, kind: robot}", "unknown kind"},
		{"unknown dep", "name: g\nnodes:\n  - {id: a, kind: tool, deps: [b]}", `unknown dependency "b"`},
		{"self dep", "name: g\nnodes:\n  - {id: a, kind: tool, deps: [a]}", "depends on itself"},
		{"cycle", "name: g\nnodes:\n  - {id: a, kind: tool, deps: [c]}\n  - {id: b, kind: tool, deps: [a]}\n  - {id: c, kind: tool, deps: [b]}\n  - {id: d, kind: tool}", "cycle through: a, b, c"},
		{"slot without filled_by", "name: g\nnodes:\n  - {id: s, kind: slot}", "filled_by is required"},
		{"filled_by on agent", "name: g\nnodes:\n  - {id: p, kind: plan}\n  - {id: a, kind: agent, filled_by: p, deps: [p]}", "allowed only there"},
		{"slot before its plan", "name: g\nnodes:\n  - {id: s, kind: slot, filled_by: p}\n  - {id: p, kind: plan}", "must come after its plan"},
		{"slot filled by agent", "name: g\nnodes:\n  - {id: p, kind: agent}\n  - {id: s, kind: slot, filled_by: p, deps: [p]}", "must name a plan node"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got error %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestFill(t *testing.T) {
	g, err := Parse([]byte(`
name: g
nodes:
  - {id: plan, kind: plan}
  - {id: gate, kind: gate, deps: [plan]}
  - {id: tasks, kind: slot, filled_by: plan, deps: [gate]}
  - {id: verify, kind: tool, deps: [tasks]}
`))
	if err != nil {
		t.Fatal(err)
	}
	filled, err := g.Fill("tasks", []Node{
		{ID: "a", Kind: Agent},
		{ID: "b", Kind: Agent},
		{ID: "c", Kind: Agent, Deps: []string{"a", "b"}},
		{ID: "d", Kind: Agent},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := filled.Node("tasks"); ok {
		t.Error("slot is still in the graph")
	}
	for id, want := range map[string][]string{
		"a":      {"gate"},
		"b":      {"gate"},
		"c":      {"a", "b"},
		"d":      {"gate"},
		"verify": {"c", "d"},
	} {
		n, _ := filled.Node(id)
		if !slices.Equal(n.Deps, want) {
			t.Errorf("%s deps = %v, want %v", id, n.Deps, want)
		}
	}
	if verify, _ := g.Node("verify"); !slices.Equal(verify.Deps, []string{"tasks"}) {
		t.Errorf("Fill changed the original graph: verify deps = %v", verify.Deps)
	}
}

func TestFillRejects(t *testing.T) {
	g, err := Parse([]byte(`
name: g
nodes:
  - {id: plan, kind: plan}
  - {id: tasks, kind: slot, filled_by: plan, deps: [plan]}
`))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		nodes []Node
		want  string
	}{
		{"empty", nil, "emitted no nodes"},
		{"nested plan", []Node{{ID: "p2", Kind: Plan}}, "may not emit plan nodes"},
		{"dep outside emission", []Node{{ID: "a", Kind: Agent, Deps: []string{"plan"}}}, "only on each other"},
		{"id clash", []Node{{ID: "plan", Kind: Agent}}, "duplicate id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := g.Fill("tasks", tt.nodes)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got error %v, want one containing %q", err, tt.want)
			}
		})
	}
}
