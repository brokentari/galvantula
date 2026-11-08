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
		{"cycle", "name: g\nnodes:\n  - {id: a, kind: tool, deps: [c]}\n  - {id: b, kind: tool, deps: [a]}\n  - {id: c, kind: tool, deps: [b]}\n  - {id: d, kind: tool}", "depend on one: a, b, c"},
		{"node after a cycle", "name: g\nnodes:\n  - {id: a, kind: tool, deps: [b]}\n  - {id: b, kind: tool, deps: [a]}\n  - {id: e, kind: tool, deps: [a]}", "depend on one: a, b, e"},
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

func TestRolesAndAdvisory(t *testing.T) {
	tests := []struct {
		name, yaml, want string
	}{
		{"agent role on a tool", "name: g\nnodes:\n  - {id: a, kind: tool, role: architect}", `role "architect" is not valid for a tool node`},
		{"unknown role", "name: g\nnodes:\n  - {id: a, kind: agent, role: boss}", `role "boss" is not valid`},
		{"advisory tool", "name: g\nnodes:\n  - {id: a, kind: tool, advisory: true}", "only agent nodes may be advisory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.yaml)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want an error with %q", err, tt.want)
			}
		})
	}
	if _, err := Parse([]byte("name: g\nnodes:\n  - {id: a, kind: agent, role: reviewer, advisory: true}\n  - {id: b, kind: tool, role: verify, deps: [a]}")); err != nil {
		t.Fatal(err)
	}
}

func TestReplan(t *testing.T) {
	g, err := Parse([]byte(`
name: g
nodes:
  - {id: base, kind: agent, role: implementer, scope: [base]}
  - {id: a, kind: agent, role: implementer, scope: [a], deps: [base]}
  - {id: b, kind: agent, role: implementer, scope: [b], deps: [a]}
  - {id: c, kind: agent, role: implementer, scope: [c], deps: [base]}
  - {id: review, kind: agent, role: reviewer, deps: [b, c]}
`))
	if err != nil {
		t.Fatal(err)
	}
	out, removed, err := g.Replan("a", Node{ID: "replan-1", Kind: Plan, Role: "planner"}, "replan-1-tasks")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, n := range removed {
		ids = append(ids, n.ID)
	}
	if !slices.Equal(ids, []string{"a", "b"}) {
		t.Errorf("removed %v, want the failed task and its dependent", ids)
	}
	for id, want := range map[string][]string{
		"c":              {"base"},
		"review":         {"replan-1-tasks", "c"},
		"replan-1":       {"base"},
		"replan-1-tasks": {"replan-1"},
	} {
		n, ok := out.Node(id)
		if !ok || !slices.Equal(n.Deps, want) {
			t.Errorf("%s deps = %v (found %t), want %v", id, n.Deps, ok, want)
		}
	}
	filled, err := out.Fill("replan-1-tasks", []Node{{ID: "a2", Kind: Agent, Role: "implementer", Scope: []string{"a"}}})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := filled.Node("a2"); !slices.Equal(n.Deps, []string{"replan-1"}) {
		t.Errorf("a2 deps = %v, want the plan node", n.Deps)
	}
	if _, _, err := g.Replan("review", Node{ID: "p", Kind: Plan}, "s"); err == nil {
		t.Error("Replan accepted a node that is not a task")
	}
	if _, _, err := g.Replan("a", Node{ID: "p", Kind: Agent}, "s"); err == nil {
		t.Error("Replan accepted a planner that is not a plan node")
	}
}
