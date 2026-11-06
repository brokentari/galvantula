package main

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brokentari/galvantula/internal/event"
	"github.com/brokentari/galvantula/internal/graph"
)

// A run the eval loses still counts, as a failed run, so the pass rate
// never leaves it out.
func TestLostRunsCountAsFailures(t *testing.T) {
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer refuse.Close()
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo = filepath.Join(repo, "..", "..")
	inTempRepo(t, repo) // runEvals writes evals/results here
	err = runEvals([]string{"-server", refuse.URL, "-db", "f.db", "-runs", "runs", "-n", "2",
		"-graph", filepath.Join(repo, "graphs", "phase1.yaml"), filepath.Join(repo, "evals", "wordfreq")})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("evals", "results", time.Now().Format(time.DateOnly)+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("recorded %d runs, want 2:\n%s", len(lines), data)
	}
	for _, l := range lines {
		var r evalResult
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatal(err)
		}
		if r.Pass || r.Status != "error" || !strings.Contains(r.Reason, "refused") || r.Eval != "wordfreq" {
			t.Errorf("got %+v, want a failed wordfreq run with status error", r)
		}
		if r.Commit == "" {
			t.Error("the result does not record the factory's commit")
		}
	}
}

// Outside the repo root, the hidden tests cannot be scored. The eval must
// stop before it pays for any run.
func TestEvalNeedsRepoRoot(t *testing.T) {
	t.Chdir(t.TempDir())
	err := runEvals([]string{"-server", "http://127.0.0.1:1", "evals/wordfreq"})
	if err == nil || !strings.Contains(err.Error(), "repo root") {
		t.Fatalf("got %v, want an error about the repo root", err)
	}
}

// After one run reaches the account's usage limit, the eval must start no
// more runs: each one would buy more usage.
func TestEvalStopsAtUsageLimit(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	inTempRepo(t, repo)
	pollEvery = 10 * time.Millisecond
	t.Cleanup(func() { pollEvery = 5 * time.Second })
	store, err := event.Open("f.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var started atomic.Int32
	limits := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		id := fmt.Sprintf("r-%d", started.Add(1))
		g := graph.Graph{Name: "g", Nodes: []graph.Node{{ID: "build", Kind: graph.Agent}}}
		for _, e := range []event.Event{
			event.New(id, event.RunCreated, "", 0, event.RunCreatedData{Graph: g, Executor: "claude", Spec: "x"}),
			event.New(id, event.NodeStarted, "build", 1, nil),
			event.New(id, event.NodeFailed, "build", 1, event.NodeFailedData{Error: "usage limit", Class: event.Limit}),
			event.New(id, event.RunFinished, "", 0, event.RunFinishedData{Status: event.RunFailed, Reason: "usage limit"}),
		} {
			if _, err := store.Append(e); err != nil {
				t.Error(err)
			}
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":%q}`, id)
	}))
	defer limits.Close()
	err = runEvals([]string{"-server", limits.URL, "-db", "f.db", "-runs", "runs", "-n", "3",
		"-graph", filepath.Join(repo, "graphs", "phase1.yaml"), filepath.Join(repo, "evals", "wordfreq")})
	if err != nil {
		t.Fatal(err)
	}
	if n := started.Load(); n != 1 {
		t.Errorf("the eval started %d runs, want 1", n)
	}
}

// inTempRepo moves to a new git repo that holds what runEvals needs from
// the repo root.
func inTempRepo(t *testing.T, repo string) {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := os.WriteFile("go.mod", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join("evals", "evalkit"), os.DirFS(filepath.Join(repo, "evals", "evalkit"))); err != nil {
		t.Fatal(err)
	}
	git := "git init -q && git add -A && git -c user.name=t -c user.email=t@localhost commit -q -m start"
	if out, err := exec.Command("sh", "-c", git).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestMedian(t *testing.T) {
	for _, tt := range []struct {
		xs   []float64
		want float64
	}{{nil, 0}, {[]float64{3, 1, 2}, 2}, {[]float64{4, 1, 3, 2}, 2.5}} {
		if got := median(tt.xs); got != tt.want {
			t.Errorf("median(%v) = %v, want %v", tt.xs, got, tt.want)
		}
	}
}

// The hidden tests score a product in the sandbox. They must still build and
// run there with only the parts of the repo they need mounted.
func TestHiddenTestsScoreAProduct(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap is not installed")
	}
	t.Chdir("../..")
	product := t.TempDir()
	if err := os.WriteFile(filepath.Join(product, "go.mod"), []byte("module product\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(product, os.DirFS("evals/wordfreq/reference")); err != nil {
		t.Fatal(err)
	}
	passed, total, err := hiddenTests(t.Context(), t.TempDir(), "evals/wordfreq", product)
	if err != nil || total == 0 || passed != total {
		t.Fatalf("got %d/%d, %v; want every hidden test to pass", passed, total, err)
	}

	// A product without the program fails, and must not look like a pass.
	if err := os.RemoveAll(filepath.Join(product, "cmd")); err != nil {
		t.Fatal(err)
	}
	if passed, _, err := hiddenTests(t.Context(), t.TempDir(), "evals/wordfreq", product); err == nil && passed > 0 {
		t.Errorf("a product without its program passed %d hidden tests", passed)
	}
}
