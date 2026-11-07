package plan

import (
	"strings"
	"testing"
)

var packages = []string{"cmd/x", "internal/parse", "internal/render"}

func task(id string, deps []string, scope ...string) Task {
	return Task{ID: id, Title: id, Scope: scope, Deps: deps}
}

func TestParse(t *testing.T) {
	good := `{"tasks": [{"id": "a", "title": "A", "scope": ["x"], "deps": []}]}`
	for name, answer := range map[string]string{
		"bare":        good,
		"in a fence":  "Here is the plan:\n```json\n" + good + "\n```\nDone.",
		"with a note": "Plan: " + good,
	} {
		out, err := Parse(answer)
		if err != nil || len(out.Tasks) != 1 || out.Tasks[0].ID != "a" {
			t.Errorf("%s: got %+v, %v", name, out, err)
		}
	}
	for name, answer := range map[string]string{
		"no JSON":       "I will split it in two.",
		"unknown field": `{"tasks": [], "extra": 1}`,
		"wrong type":    `{"tasks": [{"id": 3}]}`,
	} {
		if _, err := Parse(answer); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := Output{Tasks: []Task{
		task("parse", nil, "internal/parse"),
		task("render", nil, "internal/render"),
		task("cli", []string{"parse", "render"}, "cmd/x"),
	}}
	if err := Validate(ok, packages); err != nil {
		t.Fatalf("a good plan was refused: %v", err)
	}

	tests := []struct {
		name string
		out  Output
		want string
	}{
		{"no tasks", Output{}, "has 0"},
		{"too many", Output{Tasks: slicesOf(7)}, "has 7"},
		{"an unowned package", Output{Tasks: []Task{task("a", nil, "cmd/x", "internal/parse")}}, `"internal/render" is in no task's scope`},
		{"two owners", Output{Tasks: []Task{task("a", nil, "cmd/x", "internal/parse", "internal/render"), task("b", nil, "internal/parse")}}, "both"},
		{"not a package", Output{Tasks: []Task{task("a", nil, "cmd/x", "internal/parse", "internal/render", "internal/new")}}, "not a package of the contract"},
		{"an escaping scope", Output{Tasks: []Task{task("a", nil, "../etc")}}, "clean relative path"},
		{"the factory folder", Output{Tasks: []Task{task("a", nil, ".factory/notes")}}, "clean relative path"},
		{"no scope", Output{Tasks: []Task{task("a", nil)}}, "no scope"},
		{"too deep", Output{Tasks: []Task{
			task("a", nil, "cmd/x"), task("b", []string{"a"}, "internal/parse"), task("c", []string{"b"}, "internal/render"),
			task("d", []string{"c"}, "p4"), task("e", []string{"d"}, "p5"),
		}}, "longest chain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pk := packages
			if tt.name == "too deep" {
				pk = []string{"cmd/x", "internal/parse", "internal/render", "p4", "p5"}
			}
			if err := Validate(tt.out, pk); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want an error with %q", err, tt.want)
			}
		})
	}
}

func slicesOf(n int) []Task {
	var ts []Task
	for i := range n {
		ts = append(ts, task(string(rune('a'+i)), nil, "d"+string(rune('a'+i))))
	}
	return ts
}

func TestNodes(t *testing.T) {
	nodes := Nodes(Output{Tasks: []Task{task("a", nil, "x"), task("b", []string{"a"}, "y")}})
	if len(nodes) != 2 || nodes[1].Deps[0] != "a" || nodes[0].Role != "implementer" || nodes[0].Retries != Retries || nodes[0].Scope[0] != "x" {
		t.Errorf("got %+v", nodes)
	}
}
