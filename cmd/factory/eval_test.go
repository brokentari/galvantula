package main

import (
	"bytes"
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
	"github.com/brokentari/galvantula/internal/plan"
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
		"-graph", filepath.Join(repo, "graphs", "single.yaml"), filepath.Join(repo, "evals", "wordfreq")})
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
			event.New(id, event.RunCreated, "", 0, event.RunCreatedData{Graph: g, Executor: "claude", Spec: "x", Commit: "abc1234"}),
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
		"-graph", filepath.Join(repo, "graphs", "single.yaml"), filepath.Join(repo, "evals", "wordfreq")})
	if err != nil {
		t.Fatal(err)
	}
	if n := started.Load(); n != 1 {
		t.Errorf("the eval started %d runs, want 1", n)
	}
	// The result names the server's code, as the run recorded it.
	data, err := os.ReadFile(filepath.Join("evals", "results", time.Now().Format(time.DateOnly)+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var r evalResult
	if err := json.Unmarshal(bytes.TrimSpace(data), &r); err != nil || r.Commit != "abc1234" {
		t.Errorf("result %s (%v); want commit abc1234", data, err)
	}
}

// A run that logs nothing past its time limit is lost and recorded as an
// error. A run that keeps logging is followed to its end, however long.
func TestQuietRunsAreLost(t *testing.T) {
	tests := []struct {
		name       string
		logs       int // node_log events before the run finishes; -1 never finishes
		wantStatus string
		wantReason string
	}{
		{"logs nothing", -1, "error", "logged nothing"},
		{"keeps logging", 60, "failed", "gave up"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, err := filepath.Abs(filepath.Join("..", ".."))
			if err != nil {
				t.Fatal(err)
			}
			inTempRepo(t, repo)
			pollEvery, checkGrace = 10*time.Millisecond, 300*time.Millisecond
			t.Cleanup(func() { pollEvery, checkGrace = 5*time.Second, 10*time.Minute })
			store, err := event.Open("f.db")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			appendTo := func(e event.Event) {
				if _, err := store.Append(e); err != nil {
					t.Error(err)
				}
			}
			done := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				g := graph.Graph{Name: "g", Nodes: []graph.Node{{ID: "build", Kind: graph.Agent}}}
				appendTo(event.New("r-1", event.RunCreated, "", 0, event.RunCreatedData{Graph: g, Executor: "claude", Spec: "x", Commit: "abc1234"}))
				appendTo(event.New("r-1", event.NodeStarted, "build", 1, nil))
				if tt.logs >= 0 {
					// Log for longer than checkGrace in total, but never pause that long.
					go func() {
						defer close(done)
						for range tt.logs {
							time.Sleep(10 * time.Millisecond)
							appendTo(event.New("r-1", event.NodeLog, "build", 1, event.NodeLogData{Line: "working"}))
						}
						appendTo(event.New("r-1", event.RunFinished, "", 0, event.RunFinishedData{Status: event.RunFailed, Reason: "gave up"}))
					}()
				} else {
					close(done)
				}
				w.WriteHeader(http.StatusCreated)
				fmt.Fprint(w, `{"id":"r-1"}`)
			}))
			defer srv.Close()
			err = runEvals([]string{"-server", srv.URL, "-db", "f.db", "-runs", "runs", "-n", "1",
				"-graph", filepath.Join(repo, "graphs", "single.yaml"), filepath.Join(repo, "evals", "wordfreq")})
			<-done
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join("evals", "results", time.Now().Format(time.DateOnly)+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			var r evalResult
			if err := json.Unmarshal(bytes.TrimSpace(data), &r); err != nil {
				t.Fatal(err)
			}
			if r.Commit != "abc1234" {
				t.Errorf("result commit %q, want abc1234: a lost run must still name its code", r.Commit)
			}
			if r.Status != tt.wantStatus || !strings.Contains(r.Reason, tt.wantReason) {
				t.Errorf("got status %q, reason %q; want %q and a reason with %q", r.Status, r.Reason, tt.wantStatus, tt.wantReason)
			}
		})
	}
}

// inTempRepo moves to a new folder that holds what runEvals needs from
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
	passed, total, err := hiddenTests(t.Context(), "evals/wordfreq", product)
	if err != nil || total == 0 || passed != total {
		t.Fatalf("got %d/%d, %v; want every hidden test to pass", passed, total, err)
	}

	// A product without the program fails, and must not look like a pass.
	if err := os.RemoveAll(filepath.Join(product, "cmd")); err != nil {
		t.Fatal(err)
	}
	if passed, _, err := hiddenTests(t.Context(), "evals/wordfreq", product); err == nil && passed > 0 {
		t.Errorf("a product without its program passed %d hidden tests", passed)
	}
}

// Two tasks that overlap in time count as 2 in parallel; a failure that
// names a merge conflict is counted.
func TestParallelism(t *testing.T) {
	g := graph.Graph{Name: "g", Nodes: []graph.Node{{ID: "architect", Kind: graph.Agent}, {ID: "plan", Kind: graph.Plan}}}
	filled := graph.Graph{Name: "g", Nodes: []graph.Node{{ID: "architect", Kind: graph.Agent}, {ID: "a", Kind: graph.Agent}, {ID: "b", Kind: graph.Agent}}}
	ev := func(typ event.Type, node string, data any) event.Event { return event.New("r", typ, node, 1, data) }
	evs := []event.Event{
		ev(event.RunCreated, "", event.RunCreatedData{Graph: g}),
		ev(event.NodeStarted, "architect", nil),
		ev(event.NodeSucceeded, "architect", nil),
		ev(event.GraphUpdated, "plan", event.GraphUpdatedData{Graph: filled, Reason: "x"}),
		ev(event.NodeStarted, "a", nil),
		ev(event.NodeStarted, "b", nil), // a and b run at once
		ev(event.NodeSucceeded, "a", nil),
		ev(event.NodeFailed, "b", event.NodeFailedData{Error: "merge conflict: node b", Class: event.Task}),
	}
	if maxRunning, conflicts, attempts := parallelism(evs, g); maxRunning != 2 || conflicts != 1 || attempts != 3 {
		t.Errorf("parallelism = %d, %d, %d; want 2, 1 and 3 attempts", maxRunning, conflicts, attempts)
	}
	if maxRunning, _, _ := parallelism(evs[:5], g); maxRunning != 1 {
		t.Errorf("with only a started, parallelism = %d, want 1", maxRunning)
	}
}

// A planted plan that the planner would refuse tells the eval nothing, so
// each one must pass the plan rules for the eval's packages.
func TestPlantedPlansAreValid(t *testing.T) {
	packages := map[string][]string{"logq": {"cmd/logq", "internal/filter", "internal/parse", "internal/render"}}
	files, _ := filepath.Glob(filepath.Join("..", "..", "evals", "*", "planted", "*.json"))
	if len(files) == 0 {
		t.Fatal("no planted plans")
	}
	for _, f := range files {
		eval := filepath.Base(filepath.Dir(filepath.Dir(f)))
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out, err := plan.Parse(string(data))
		if err == nil {
			err = plan.Validate(out, packages[eval])
		}
		if err == nil {
			_, err = graph.Graph{Name: "g", Nodes: []graph.Node{{ID: "p", Kind: graph.Plan}, {ID: "s", Kind: graph.Slot, FilledBy: "p", Deps: []string{"p"}}}}.Fill("s", plan.Nodes(out))
		}
		if err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

// -planted sends the file's plan with each run, and a missing file stops the eval before any run starts.
func TestPlantedPlanReachesTheServer(t *testing.T) {
	var bodies atomic.Pointer[string]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		buf.ReadFrom(r.Body)
		s := buf.String()
		bodies.Store(&s)
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer srv.Close()
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	inTempRepo(t, repo)
	args := func(name string) []string {
		return []string{"-server", srv.URL, "-db", "f.db", "-runs", "runs", "-n", "1", "-planted", name,
			"-graph", filepath.Join(repo, "graphs", "factory.yaml"), filepath.Join(repo, "evals", "logq")}
	}
	// single has no plan node, so a planted plan would never be used.
	noPlanner := append(args("cli-first")[:10:10], "-graph", filepath.Join(repo, "graphs", "single.yaml"), filepath.Join(repo, "evals", "logq"))
	if err := runEvals(noPlanner); err == nil || !strings.Contains(err.Error(), "plan node") || bodies.Load() != nil {
		t.Fatalf("a graph with no plan node: error %v", err)
	}
	if err := runEvals(args("nope")); err == nil || bodies.Load() != nil {
		t.Fatalf("a missing planted plan: error %v, run started %t", err, bodies.Load() != nil)
	}
	if err := runEvals(args("cli-first")); err != nil {
		t.Fatal(err)
	}
	if body := bodies.Load(); body == nil || !strings.Contains(*body, `\"id\": \"cli\"`) {
		t.Errorf("the request did not carry the plan: %v", body)
	}
	data, _ := os.ReadFile(filepath.Join("evals", "results", time.Now().Format(time.DateOnly)+".jsonl"))
	if !strings.Contains(string(data), `"planted":"cli-first"`) {
		t.Errorf("the result does not name the planted plan:\n%s", data)
	}
}
