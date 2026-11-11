package executor

import (
	"os"
	"strings"
	"testing"

	"github.com/brokentari/galvantula/internal/graph"
)

func TestFakeContract(t *testing.T) {
	s, err := ParseScript([]byte("nodes:\n  a:\n    contract: first\n    revised: second\n    tests: 4\n    red: 3\n  b: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(id string, revision bool) (Result, string) {
		var logs []string
		task := Task{Node: graph.Node{ID: id}, Attempt: 1, Revision: revision, Feedback: "Line 5:\n> x"}
		res, err := Fake{Script: s}.Run(t.Context(), task, func(l string) { logs = append(logs, l) })
		if err != nil {
			t.Fatal(err)
		}
		return res, strings.Join(logs, "\n")
	}
	if res, _ := run("a", false); res.Contract == nil || res.Contract.Markdown != "first" || res.Contract.Tests != 4 || res.Contract.Red != 3 {
		t.Errorf("first attempt: %+v", res.Contract)
	}
	res, log := run("a", true)
	if res.Contract == nil || res.Contract.Markdown != "second" {
		t.Errorf("revision: %+v", res.Contract)
	}
	if !strings.Contains(log, "feedback: Line 5: > x") {
		t.Errorf("the revision did not log its feedback on one line:\n%s", log)
	}
	if res, _ := run("b", false); res.Contract != nil {
		t.Errorf("a step with no contract returned %+v", res.Contract)
	}
	if _, err := ParseScript([]byte("nodes:\n  a:\n    contrat: x\n")); err == nil {
		t.Error("an unknown field must be an error")
	}
}

func TestShippedFakesParse(t *testing.T) {
	for _, f := range []string{"factory", "hostile"} {
		data, err := os.ReadFile("../../fakes/" + f + ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		s, err := ParseScript(data)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if a := s.Nodes["architect"]; a.Contract == "" || a.Tests == 0 {
			t.Errorf("%s: the architect has no contract", f)
		}
	}
}
