package executor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brokentari/galvantula/internal/graph"
	"github.com/brokentari/galvantula/internal/guardrails"
)

const goodMain = `package main

import "fmt"

func main() {
	for i := range 3 {
		fmt.Println(i)
	}
}
`

const oldLoop = `package main

import "fmt"

func main() {
	n := 3
	for i := 0; i < n; i++ {
		fmt.Println(i)
	}
}
`

// fakeAgent puts a claude script first on PATH. It records its arguments in
// $HOME/args, runs work in the worktree, and reports a result that cost $0.25.
func fakeAgent(t *testing.T, work string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"$@\" > \"$HOME/args\"\n" + work +
		"\necho '{\"type\":\"result\",\"is_error\":false,\"result\":\"done\",\"total_cost_usd\":0.25}'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeFile(name, body string) string {
	return "cat > " + name + " <<'EOF'\n" + body + "EOF\n"
}

func setUpRun(t *testing.T) (Claude, func(Task) (Result, error)) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap is not installed")
	}
	var rails guardrails.Guardrails
	rails.Go.Version = "1.27"
	rails.Budget.NodeUSD, rails.Budget.RunUSD, rails.Budget.NodeMinutes = 1, 1, 1
	rails.Models.Implementer = guardrails.Role{Model: "m", Effort: "low"}
	c := Claude{Root: t.TempDir(), Rails: rails, Spec: "Print 0, 1 and 2.\n", Auth: "CLAUDE_CODE_OAUTH_TOKEN=test"}
	run := func(task Task) (Result, error) {
		task.Run = "r"
		return c.Run(t.Context(), task, func(string) {})
	}
	if _, err := run(Task{Node: graph.Node{ID: "standards", Kind: graph.Tool}, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	return c, run
}

var impl = graph.Node{ID: "impl", Kind: graph.Agent}

func TestAgentPassesCheckAndMerges(t *testing.T) {
	c, run := setUpRun(t)
	// The agent also builds the program, which leaves a binary behind. It adds
	// a binary test fixture, which must stay, and a link the runtime must not read.
	// Its .gitignore matches main.go, which the check saw, so main must get it.
	fakeAgent(t, writeFile("main.go", goodMain)+writeFile(".gitignore", "main.go\nbuilt\n")+"go build -o built .\n"+
		"mkdir testdata && printf 'a\\000b' > testdata/sample.bin\nln -s /dev/zero zero\n")
	res, err := run(Task{Node: impl, Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.CostUSD != 0.25 {
		t.Errorf("cost = %v, want 0.25", res.CostUSD)
	}
	repo := filepath.Join(c.Root, "r", "repo")
	for _, f := range []string{"main.go", "AGENTS.md", "spec.md", "go.mod"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Errorf("main lacks %s: %v", f, err)
		}
	}
	if left, err := os.ReadDir(BuildCaches(c.Root)); err != nil || len(left) > 0 {
		t.Errorf("build caches outlived their attempt: %v (%v)", left, err)
	}
	for _, bin := range []string{"product", "built"} {
		if _, err := os.Stat(filepath.Join(repo, bin)); err == nil {
			t.Errorf("binary %s reached the product", bin)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "testdata", "sample.bin")); err != nil {
		t.Errorf("the binary fixture did not reach the product: %v", err)
	}
	args, _ := os.ReadFile(filepath.Join(c.Root, "r", "home", "impl", "args"))
	for _, want := range []string{"--safe-mode", "--session-id", "--max-budget-usd 1.00", "--append-system-prompt-file"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("claude args %q lack %s", args, want)
		}
	}
}

func TestCheckFailures(t *testing.T) {
	tests := []struct {
		name, work, want string
	}{
		{"edits a frozen file", writeFile("main.go", goodMain) + "echo changed >> AGENTS.md\n", "must not change: AGENTS.md"},
		{"deletes a frozen file", writeFile("main.go", goodMain) + "rm spec.md\n", "must not change: spec.md"},
		{"old for loop", writeFile("main.go", oldLoop), "go fix -diff"},
		{"unformatted", writeFile("main.go", "package main\nfunc main(){}\n"), "gofmt -l"},
		{"failing test", writeFile("main.go", goodMain) + writeFile("main_test.go", "package main\n\nimport \"testing\"\n\nfunc TestNo(t *testing.T) { t.Fatal(\"no\") }\n"), "go test"},
		{"writes no code", "", "no packages to test"},
		{"test writes to the worktree", writeFile("main.go", goodMain) + writeFile("main_test.go", "package main\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestEdit(t *testing.T) {\n\tif err := os.WriteFile(\"spec.md\", nil, 0o644); err != nil {\n\t\tt.Fatal(err)\n\t}\n}\n"), "read-only file system"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, run := setUpRun(t)
			fakeAgent(t, tt.work)
			res, err := run(Task{Node: impl, Attempt: 1})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want a check failure about %q", err, tt.want)
			}
			if IsInfra(err) {
				t.Error("a failed check counts as infra; it must count toward retries")
			}
			if res.CostUSD != 0.25 {
				t.Errorf("a failed attempt must still report its cost; got %v", res.CostUSD)
			}
			if _, err := os.Stat(filepath.Join(c.Root, "r", "repo", "main.go")); err == nil {
				t.Error("failed work was merged into main")
			}
		})
	}
}

// After the agent exits, host git works in its worktree. The agent can
// replace the .git link with a repo of its own, with a hook and an
// fsmonitor command. Host git must run neither.
func TestAgentGitDirIsIgnored(t *testing.T) {
	c, run := setUpRun(t)
	marker := filepath.Join(t.TempDir(), "escaped")
	fakeAgent(t, writeFile("main.go", goodMain)+
		"rm .git && mkdir -p .git/objects .git/refs .git/hooks && echo 'ref: refs/heads/main' > .git/HEAD\n"+
		"printf '[core]\\n\\trepositoryformatversion = 0\\n\\tfsmonitor = touch "+marker+"\\n' > .git/config\n"+
		"printf '#!/bin/sh\\ntouch "+marker+"\\n' > .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit\n")
	if _, err := run(Task{Node: impl, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("host git ran a command from the agent's .git")
	}
	if _, err := os.Stat(filepath.Join(c.Root, "r", "repo", "main.go")); err != nil {
		t.Errorf("main lacks main.go: %v", err)
	}
}

// The class of a failed attempt decides whether the runner retries it, and
// its cost counts toward the budget.
func TestAgentFailureClasses(t *testing.T) {
	c, run := setUpRun(t)
	fakeAgent(t, `echo '{"type":"result","is_error":true,"result":"You have hit your usage limit"}'; exit 0`)
	if _, err := run(Task{Node: impl, Attempt: 1}); !errors.As(err, new(*LimitError)) {
		t.Errorf("usage limit: got %v, want a LimitError", err)
	}

	fakeAgent(t, "exit 3") // crashed: no result line
	if res, err := run(Task{Node: impl, Attempt: 2}); !IsInfra(err) || res.CostUSD != 0 {
		t.Errorf("crash: got %v at $%v, want an infra failure at $0", err, res.CostUSD)
	}

	c.timeout = 500 * time.Millisecond
	fakeAgent(t, "sleep 60")
	res, err := c.Run(t.Context(), Task{Run: "r", Node: impl, Attempt: 3}, func(string) {})
	if err == nil || IsInfra(err) || errors.As(err, new(*LimitError)) || !strings.Contains(err.Error(), "ran past") {
		t.Errorf("timeout: got %v, want a task failure", err)
	}
	if res.CostUSD != c.Rails.Budget.NodeUSD {
		t.Errorf("timeout: cost %v, want the node budget %v", res.CostUSD, c.Rails.Budget.NodeUSD)
	}
}

func TestFirstRetryResumesThenStartsOver(t *testing.T) {
	c, run := setUpRun(t)
	fakeAgent(t, writeFile("main.go", oldLoop))
	_, err := run(Task{Node: impl, Attempt: 1})
	if err == nil {
		t.Fatal("attempt 1 should fail")
	}
	args := filepath.Join(c.Root, "r", "home", "impl", "args")
	first, _ := os.ReadFile(args)

	fakeAgent(t, writeFile("main.go", goodMain))
	if _, err := run(Task{Node: impl, Attempt: 2, Resume: true, Feedback: err.Error()}); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(args)
	id := strings.Fields(strings.SplitAfter(string(first), "--session-id ")[1])[0]
	if !strings.Contains(string(second), "--resume "+id) {
		t.Errorf("attempt 2 args %q do not resume session %s", second, id)
	}
}
