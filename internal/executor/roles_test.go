package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brokentari/galvantula/internal/graph"
)

// A tiny product for the pipeline tests: count(n) returns n numbers.
const (
	stubMain = `package main

import "errors"

func count(n int) ([]int, error) { return nil, errors.New("not implemented") }

func main() {}
`
	doneMain = `package main

func count(n int) ([]int, error) { return make([]int, n), nil }

func main() {}
`
	contract = "# Contract\n\ncount(n) returns n numbers. The tests call it with 0 to 5.\n"
)

// testsOn returns a test file with n tests. Each fails on the stub.
func testsOn(n int) string {
	var b strings.Builder
	b.WriteString("package main\n\nimport \"testing\"\n")
	for i := range n {
		fmt.Fprintf(&b, "\nfunc TestCount%d(t *testing.T) {\n\tgot, err := count(%d)\n\tif err != nil || len(got) != %d {\n\t\tt.Fatalf(\"count(%d) = %%v, %%v\", got, err)\n\t}\n}\n", i, i, i, i)
	}
	return b.String()
}

// fakeAgents puts a claude on PATH that does different work in each node's
// worktree, chosen by the folder's name. A node with no script fails.
// A script may set RESULT, the text the agent ends with.
func fakeAgents(t *testing.T, scripts map[string]string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("#!/bin/sh\necho \"$@\" > \"$HOME/args\"\nRESULT=done\ncase \"$(basename \"$PWD\")\" in\n")
	for node, work := range scripts {
		fmt.Fprintf(&b, "%s)\n%s\n;;\n", node, work)
	}
	b.WriteString("*) echo 'no script for this node' >&2; exit 1 ;;\nesac\n")
	b.WriteString(`echo "{\"type\":\"result\",\"is_error\":false,\"result\":\"$RESULT\",\"total_cost_usd\":0.25}"` + "\n")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// runNode runs one node of run "r", as the runner would, and collects its log.
func runNode(t *testing.T, c Claude, id, role string) (Result, string, error) {
	t.Helper()
	kind := graph.Agent
	if id == "verify" {
		kind = graph.Tool
	}
	var logs []string
	res, err := c.Run(t.Context(), Task{Run: "r", Attempt: 1, Node: graph.Node{ID: id, Kind: kind, Role: role}}, func(l string) { logs = append(logs, l) })
	return res, strings.Join(logs, "\n"), err
}

// mustRun runs the nodes in order and fails the test if one fails.
func mustRun(t *testing.T, c Claude, nodes ...[2]string) {
	t.Helper()
	for _, n := range nodes {
		if _, _, err := runNode(t, c, n[0], n[1]); err != nil {
			t.Fatalf("%s: %v", n[0], err)
		}
	}
}

var (
	architect = [2]string{"architect", "architect"}
	implement = [2]string{"implement", "implementer"}
)

// architectWork is what a good architect leaves: stubs, tests, a contract.
func architectWork(tests int) string {
	return writeFile("main.go", stubMain) + writeFile("main_test.go", testsOn(tests)) + writeFile("CONTRACT.md", contract)
}

func TestArchitectMergesAContractThatFailsOnItsStubs(t *testing.T) {
	c, _ := setUpRun(t)
	fakeAgents(t, map[string]string{"architect": architectWork(6)})
	res, log, err := runNode(t, c, "architect", "architect")
	if err != nil {
		t.Fatal(err)
	}
	if res.CostUSD != 0.25 {
		t.Errorf("cost %v, want 0.25", res.CostUSD)
	}
	for _, want := range []string{"6 of 6 tests fail on the stubs", "# Contract", "merged into main"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	for _, f := range []string{"main.go", "main_test.go", "CONTRACT.md"} {
		if _, err := os.Stat(filepath.Join(c.Root, "r", "repo", f)); err != nil {
			t.Errorf("main lacks %s", f)
		}
	}
}

// A second agent implements the stubs. The contract merges only if its tests pass.
func TestOracleTrialBuildOfTheTests(t *testing.T) {
	tests := []struct {
		name, oracle, wantErr string
	}{
		{"the tests pass", writeFile("main.go", doneMain), ""},
		{"a test cannot pass", writeFile("DISPUTES.md", "TestCount3 contradicts spec.md\n"), "TestCount3 contradicts spec.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := setUpRun(t)
			c.SkipOracle = false
			fakeAgents(t, map[string]string{"architect": architectWork(6), "oracle": tt.oracle})
			res, log, err := runNode(t, c, "architect", "architect")
			if res.CostUSD != 0.5 {
				t.Errorf("cost %v, want 0.5: the oracle's cost counts", res.CostUSD)
			}
			if tt.wantErr == "" {
				if err != nil || !strings.Contains(log, "merged into main") {
					t.Fatalf("got %v, log:\n%s", err, log)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "could not make your tests pass") || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want a refusal that names %q", err, tt.wantErr)
			}
			if IsInfra(err) || strings.Contains(log, "merged into main") {
				t.Errorf("the architect must retry, and nothing may merge: %v\n%s", err, log)
			}
		})
	}
}

func TestArchitectCheckFailures(t *testing.T) {
	panicking := writeFile("main.go", stubMain+"\nfunc init() { panic(\"stub\") }\n") + writeFile("main_test.go", testsOn(6)) + writeFile("CONTRACT.md", contract)
	skipping := writeFile("main.go", stubMain) + writeFile("main_test.go", testsOn(6)+"\nfunc TestSkipped(t *testing.T) {\n\tt.Skip(\"later\")\n}\n") + writeFile("CONTRACT.md", contract)
	tests := []struct {
		name, work, want string
	}{
		{"tests pass on the stubs", writeFile("main.go", doneMain) + writeFile("main_test.go", testsOn(6)) + writeFile("CONTRACT.md", contract), "only 0 of 6 tests fail"},
		{"too few tests", architectWork(3), "at least 5 must run"},
		{"a stub panics", panicking, "at least 5 must run"},
		{"no contract", writeFile("main.go", stubMain) + writeFile("main_test.go", testsOn(6)), "CONTRACT.md must exist"},
		{"a test skips", skipping, "must not skip tests: main_test.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := setUpRun(t)
			fakeAgents(t, map[string]string{"architect": tt.work})
			_, _, err := runNode(t, c, "architect", "architect")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want a check failure with %q", err, tt.want)
			}
			if IsInfra(err) {
				t.Error("a failed contract counts as infra; it must count toward retries")
			}
			if _, err := os.Stat(filepath.Join(c.Root, "r", "repo", "main_test.go")); err == nil {
				t.Error("a failed contract was merged")
			}
		})
	}
}

// The whole pipeline: architect, implementer, a review that finds something,
// the fix, and verify.
func TestPipeline(t *testing.T) {
	c, _ := setUpRun(t)
	fakeAgents(t, map[string]string{
		"architect": architectWork(6),
		"implement": writeFile("main.go", doneMain) + writeFile("NOTES.md", "Counts.\n"),
		"review":    "touch probe 2>/dev/null\nRESULT='Verdict: fix 1 first.'",
		"fix":       writeFile("NOTES.md", "Counts.\nOne finding fixed.\n"),
	})
	mustRun(t, c, architect, implement)

	res, log, err := runNode(t, c, "review", "reviewer")
	if err != nil || res.CostUSD != 0.25 || !strings.Contains(log, "review: fix 1 first.") {
		t.Fatalf("review: %+v, %v, log %q", res, err, log)
	}
	repo := filepath.Join(c.Root, "r", "repo")
	if report, err := os.ReadFile(filepath.Join(repo, ".factory", "review.md")); err != nil || !strings.Contains(string(report), "fix 1 first") {
		t.Fatalf("review report on main: %q, %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(c.Root, "r", "nodes", "review", "probe")); err == nil {
		t.Error("the reviewer could write to its worktree")
	}

	mustRun(t, c, [2]string{"fix", "fixer"}, [2]string{"verify", "verify"})
	if notes, _ := os.ReadFile(filepath.Join(repo, "NOTES.md")); !strings.Contains(string(notes), "One finding fixed") {
		t.Errorf("the fix was not merged: %q", notes)
	}
}

func TestImplementerCannotChangeTheContract(t *testing.T) {
	skipping := "package main\n\nimport \"testing\"\n\nfunc TestExtra(t *testing.T) {\n\tt.Skip(\"no\")\n}\n"
	tests := []struct {
		name, work, want string
	}{
		{"edits a test", writeFile("main.go", doneMain) + "echo '// edited' >> main_test.go\n", "must not change: main_test.go"},
		{"edits the contract", writeFile("main.go", doneMain) + "echo more >> CONTRACT.md\n", "must not change: CONTRACT.md"},
		{"deletes a test", writeFile("main.go", doneMain) + "rm main_test.go\n", "must not change: main_test.go"},
		// A TestMain that exits with 1 after the tests pass fails no test.
		{"TestMain exits with 1", writeFile("main.go", doneMain) + writeFile("main_exit_test.go", "package main\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) {\n\tm.Run()\n\tos.Exit(1)\n}\n"), "exited with an error"},
		// An init that exits makes go test print ok with no test run.
		{"exits before the tests run", writeFile("main.go", doneMain+"\nfunc init() { os.Exit(0) }\n") + "sed -i 's/^package main$/package main\\n\\nimport \"os\"/' main.go\n", "0 tests ran"},
		{"skips in a new test", writeFile("main.go", doneMain) + writeFile("extra_test.go", skipping), "must not skip tests: extra_test.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := setUpRun(t)
			fakeAgents(t, map[string]string{"architect": architectWork(6), "implement": tt.work})
			mustRun(t, c, architect)
			_, _, err := runNode(t, c, "implement", "implementer")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want a check failure with %q", err, tt.want)
			}
		})
	}
}

// A new test file is allowed, and so is editing a stub: only the contract is frozen.
func TestImplementerMayAddTests(t *testing.T) {
	c, _ := setUpRun(t)
	extra := "package main\n\nimport \"testing\"\n\nfunc TestExtra(t *testing.T) {\n\tif got, _ := count(2); len(got) != 2 {\n\t\tt.Fatal(got)\n\t}\n}\n"
	fakeAgents(t, map[string]string{"architect": architectWork(6), "implement": writeFile("main.go", doneMain) + writeFile("extra_test.go", extra)})
	mustRun(t, c, architect, implement)
	if _, err := os.Stat(filepath.Join(c.Root, "r", "repo", "extra_test.go")); err != nil {
		t.Error("the new test file was not merged")
	}
}

// The fix node does nothing, and costs nothing, when the review is clean or missing.
func TestFixSkipsWithoutFindings(t *testing.T) {
	for name, report := range map[string]string{"clean": "Looks good. Ship.\n", "none": ""} {
		t.Run(name, func(t *testing.T) {
			c, _ := setUpRun(t)
			fakeAgents(t, map[string]string{}) // any agent that starts fails
			if report != "" {
				path := filepath.Join(c.Root, "r", "repo", ".factory", "review.md")
				if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			res, log, err := runNode(t, c, "fix", "fixer")
			if err != nil || res.CostUSD != 0 || !strings.Contains(log, "skipped") {
				t.Errorf("got %+v, %v, log %q; want a skip with no cost", res, err, log)
			}
		})
	}
}

func TestVerifyFailures(t *testing.T) {
	tests := []struct {
		name, file, body, want string
	}{
		{"a module is required", "go.mod", "module product\n\ngo 1.27\n\nrequire x v1.0.0\n", "go.mod names a module"},
		{"wrong go version", "go.mod", "module product\n\ngo 1.20\n", "go.mod must say go 1.27"},
		{"a failing test", "no_test.go", "package main\n\nimport \"testing\"\n\nfunc TestNo(t *testing.T) {\n\tt.Fatal(\"no\")\n}\n", "go test -race"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := setUpRun(t)
			repo := filepath.Join(c.Root, "r", "repo")
			for f, body := range map[string]string{"main.go": doneMain, tt.file: tt.body} {
				if err := os.WriteFile(filepath.Join(repo, f), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := runNode(t, c, "verify", "verify")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want a failure with %q", err, tt.want)
			}
		})
	}
}

// Every role that the graph accepts must be one that the executor runs.
func TestEveryAgentRoleHasAPlan(t *testing.T) {
	for _, role := range graph.Roles[graph.Agent] {
		if _, ok := plans[role]; !ok {
			t.Errorf("graph.Roles accepts %q, but plans has no plan for it", role)
		}
	}
}

// A reviewer that ends without a report fails; as an advisory node it is
// abandoned, and the fix node then has no report and skips itself.
func TestReviewerWithoutAReportFails(t *testing.T) {
	c, _ := setUpRun(t)
	fakeAgents(t, map[string]string{"review": "RESULT=''"})
	if _, _, err := runNode(t, c, "review", "reviewer"); err == nil || !strings.Contains(err.Error(), "without a report") {
		t.Fatalf("got %v, want an error about the missing report", err)
	}
	if _, err := os.Stat(filepath.Join(c.Root, "r", "repo", ".factory", "review.md")); err == nil {
		t.Error("an empty report was saved")
	}
}

// passingTests returns n tests that pass on the stubs.
func passingTests(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "\nfunc TestPassesOnStubs%d(t *testing.T) {}\n", i)
	}
	return b.String()
}

// The architect's rule is at least 80% of the tests failing: 4 of 5 passes
// the check, and 3 of 5 does not.
func TestArchitectRedThreshold(t *testing.T) {
	tests := []struct {
		name  string
		fails int
		want  string // empty: the contract is accepted
	}{
		{"4 of 5 fail", 4, ""},
		{"3 of 5 fail", 3, "only 3 of 5 tests fail"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := setUpRun(t)
			work := writeFile("main.go", stubMain) + writeFile("main_test.go", testsOn(tt.fails)+passingTests(5-tt.fails)) + writeFile("CONTRACT.md", contract)
			fakeAgents(t, map[string]string{"architect": work})
			_, _, err := runNode(t, c, "architect", "architect")
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("got %v, want the contract accepted", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("got %v, want a failure with %q", err, tt.want)
			}
		})
	}
}

// The brief tells the architect the rule that the check enforces.
func TestArchitectBriefStatesTheRule(t *testing.T) {
	for _, want := range []string{"at least 5 tests", "at least 80% failing"} {
		if !strings.Contains(architectBrief, want) {
			t.Errorf("the architect brief lacks %q", want)
		}
	}
}

// The runtime reads and writes some files that agents make, outside the
// sandbox. A link there must fail the check at once. It must not hang the
// runtime, as a read of /dev/zero would, or redirect the review report.
func TestLinksFailTheCheck(t *testing.T) {
	for name, link := range map[string]string{
		"a link named like a test":    "ln -s /dev/zero zero_test.go",
		"a link in place of a report": "mkdir -p .factory && ln -s /etc/hostname .factory/review.md",
		"a link to a directory":       "ln -s /tmp tmplink",
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := setUpRun(t)
			fakeAgents(t, map[string]string{"architect": architectWork(6), "implement": writeFile("main.go", doneMain) + link + "\n"})
			mustRun(t, c, architect)
			_, _, err := runNode(t, c, "implement", "implementer")
			if err == nil || !strings.Contains(err.Error(), "symbolic links are not allowed") {
				t.Fatalf("got %v, want a refusal of the link", err)
			}
		})
	}
}
