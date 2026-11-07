package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/brokentari/galvantula/internal/graph"
)

// put writes a file in a folder that may not exist yet.
func put(name, body string) string {
	return "mkdir -p " + filepath.Dir(name) + "\n" + writeFile(name, body)
}

// A product with two packages, so that two nodes can build at the same time.
func stubsFor(pkg, fn string) string {
	return fmt.Sprintf("package %s\n\nfunc %s(n int) int { return 0 }\n", pkg, fn)
}

func doneFor(pkg, fn, body string) string {
	return fmt.Sprintf("package %s\n\nfunc %s(n int) int { return %s }\n", pkg, fn, body)
}

// testsFor returns 3 tests of fn, each wrong on the stub.
func testsFor(pkg, fn, want string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\nimport \"testing\"\n", pkg)
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&b, "\nfunc Test%s%d(t *testing.T) {\n\tn := %d\n\tif got := %s(n); got != %s {\n\t\tt.Fatalf(\"%s(%%d) = %%d\", n, got)\n\t}\n}\n", fn, i, i, fn, want, fn)
	}
	return b.String()
}

// twoPackages is the architect's work for a product of pkga and pkgb.
func twoPackages() string {
	return put("pkga/a.go", stubsFor("pkga", "A")) + put("pkga/a_test.go", testsFor("pkga", "A", "n+1")) +
		put("pkgb/b.go", stubsFor("pkgb", "B")) + put("pkgb/b_test.go", testsFor("pkgb", "B", "n*2")) +
		writeFile("CONTRACT.md", "# Contract\n\nA adds one. B doubles.\n")
}

func planJSON(tasks ...string) string {
	return `RESULT='{\"tasks\": [` + strings.Join(tasks, ", ") + `]}'`
}

func taskJSON(id, scope string, deps ...string) string {
	d := ""
	if len(deps) > 0 {
		d = `\"` + strings.Join(deps, `\",\"`) + `\"`
	}
	return fmt.Sprintf(`{\"id\": \"%s\", \"title\": \"%s\", \"scope\": [\"%s\"], \"deps\": [%s]}`, id, id, scope, d)
}

func runPlanner(t *testing.T, c Claude) (Result, string, error) {
	t.Helper()
	var logs []string
	res, err := c.Run(t.Context(), Task{Run: "r", Attempt: 1, Node: graph.Node{ID: "plan", Kind: graph.Plan}}, func(l string) { logs = append(logs, l) })
	return res, strings.Join(logs, "\n"), err
}

func TestPlannerEmitsAValidatedTaskGraph(t *testing.T) {
	c, _ := setUpRun(t)
	fakeAgents(t, map[string]string{
		"architect": twoPackages(),
		"plan":      planJSON(taskJSON("a", "pkga"), taskJSON("b", "pkgb")),
	})
	mustRun(t, c, architect)
	res, log, err := runPlanner(t, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 2 || res.Nodes[0].ID != "a" || res.Nodes[0].Role != "implementer" || res.Nodes[1].Scope[0] != "pkgb" {
		t.Fatalf("nodes %+v", res.Nodes)
	}
	for _, want := range []string{"task a (starts at once): pkga", "task b (starts at once): pkgb", "2 tasks. One attempt of each costs at most $2.00"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

func TestPlannerRejections(t *testing.T) {
	tests := []struct {
		name, answer, want string
	}{
		{"a package without an owner", planJSON(taskJSON("a", "pkga")), `package "pkgb" is in no task's scope`},
		{"a package with two owners", planJSON(taskJSON("a", "pkga"), taskJSON("b", "pkga"), taskJSON("c", "pkgb")), "both"},
		{"not JSON", "RESULT='I would split it in two.'", "no JSON object"},
		{"a dependency on nothing", planJSON(taskJSON("a", "pkga", "ghost"), taskJSON("b", "pkgb")), ""}, // graph.Fill rejects it later
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := setUpRun(t)
			fakeAgents(t, map[string]string{"architect": twoPackages(), "plan": tt.answer})
			mustRun(t, c, architect)
			_, _, err := runPlanner(t, c)
			if tt.want == "" {
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want a rejection with %q", err, tt.want)
			}
			if IsInfra(err) {
				t.Error("a bad plan counts as infra; it must count toward retries")
			}
		})
	}
}

// Two nodes with different scopes work at the same time from the same main,
// and both merge. Neither runs the other's failing tests in its check.
func TestScopedNodesRunInParallelAndMerge(t *testing.T) {
	c, _ := setUpRun(t)
	fakeAgents(t, map[string]string{
		"architect": twoPackages(),
		"a":         put("pkga/a.go", doneFor("pkga", "A", "n + 1")) + "mkdir -p .factory/notes && echo a > .factory/notes/a.md",
		"b":         put("pkgb/b.go", doneFor("pkgb", "B", "n * 2")) + "mkdir -p .factory/notes && echo b > .factory/notes/b.md",
	})
	mustRun(t, c, architect)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, n := range [][2]string{{"a", "implementer"}, {"b", "implementer"}} {
		wg.Go(func() {
			x := Task{Run: "r", Attempt: 1, Node: graph.Node{ID: n[0], Kind: graph.Agent, Role: n[1], Scope: []string{"pkg" + n[0]}}}
			_, errs[i] = c.Run(t.Context(), x, func(string) {})
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("node %d: %v", i, err)
		}
	}
	repo := filepath.Join(c.Root, "r", "repo")
	for _, f := range []string{"pkga/a.go", "pkgb/b.go", ".factory/notes/a.md", ".factory/notes/b.md"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Errorf("main lacks %s", f)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "pkga", "a.go")); !strings.Contains(string(got), "n + 1") {
		t.Errorf("pkga/a.go on main: %q", got)
	}
	// The whole suite now passes on the merged result.
	mustRun(t, c, [2]string{"verify", "verify"})
}

func TestScopeViolations(t *testing.T) {
	tests := []struct {
		name, work, want string
	}{
		{"writes in another package", put("pkga/a.go", doneFor("pkga", "A", "n + 1")) + put("pkgb/b.go", doneFor("pkgb", "B", "n * 2")), "outside your scope (pkga): pkgb/b.go"},
		{"adds a file at the root", put("pkga/a.go", doneFor("pkga", "A", "n + 1")) + writeFile("extra.go", "package main\n"), "extra.go"},
		{"adds a subfolder", put("pkga/a.go", doneFor("pkga", "A", "n + 1")) + "mkdir pkga/sub && echo 'package sub' > pkga/sub/s.go", "pkga/sub/s.go"},
		{"writes another node's notes", put("pkga/a.go", doneFor("pkga", "A", "n + 1")) + "mkdir -p .factory/notes && echo x > .factory/notes/b.md", ".factory/notes/b.md"},
		{"writes NOTES.md", put("pkga/a.go", doneFor("pkga", "A", "n + 1")) + "echo x > NOTES.md", "NOTES.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := setUpRun(t)
			fakeAgents(t, map[string]string{"architect": twoPackages(), "a": tt.work})
			mustRun(t, c, architect)
			x := Task{Run: "r", Attempt: 1, Node: graph.Node{ID: "a", Kind: graph.Agent, Role: "implementer", Scope: []string{"pkga"}}}
			_, err := c.Run(t.Context(), x, func(string) {})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want a refusal that names %q", err, tt.want)
			}
			if _, err := os.Stat(filepath.Join(c.Root, "r", "repo", "pkga", "a.go")); err == nil && !strings.Contains(tt.name, "stub") {
				// A failed node must not reach main.
				if got, _ := os.ReadFile(filepath.Join(c.Root, "r", "repo", "pkga", "a.go")); strings.Contains(string(got), "n + 1") {
					t.Error("refused work was merged")
				}
			}
		})
	}
}
