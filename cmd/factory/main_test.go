package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brokentari/galvantula/graphs"
	"github.com/brokentari/galvantula/internal/event"
	"github.com/brokentari/galvantula/internal/graph"
)

// A second server on a busy port must stop before it resumes runs that the
// first server drives; two drivers on one run corrupt its log.
func TestServeStopsBeforeResumeWhenPortIsBusy(t *testing.T) {
	db := filepath.Join(t.TempDir(), "events.db")
	store, err := event.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	g := graph.Graph{Name: "g", Nodes: []graph.Node{{ID: "a", Kind: graph.Tool}}}
	for _, e := range []event.Event{
		event.New("r", event.RunCreated, "", 0, event.RunCreatedData{Graph: g, Executor: "fake"}),
		event.New("r", event.NodeStarted, "a", 1, nil),
	} {
		if _, err := store.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if err := serve([]string{"-addr", busy.Addr().String(), "-db", db, "-ui", ""}); err == nil {
		t.Fatal("serve started on a busy port")
	}

	store, err = event.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if evs, _ := store.Events("r", 0); len(evs) != 2 {
		t.Errorf("the log has %d events, want 2: serve resumed the run before it found the port busy", len(evs))
	}
}

// The commit names the code that ran each eval. Result files must not make
// it dirty; any other change must.
func TestFactoryCommit(t *testing.T) {
	t.Chdir(t.TempDir())
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if got := factoryCommit(); got != "" {
		t.Errorf("outside a repo: got %q, want no commit", got)
	}
	git("init", "-q")
	write("a.go")
	git("add", "a.go")
	git("commit", "-q", "-m", "start")

	clean := factoryCommit()
	if clean == "" || strings.HasSuffix(clean, "-dirty") {
		t.Fatalf("clean tree: got %q, want a bare hash", clean)
	}
	write("evals/results/x.jsonl")
	if got := factoryCommit(); got != clean {
		t.Errorf("with a new results file: got %q, want %q", got, clean)
	}
	write("b.go")
	if got := factoryCommit(); got != clean+"-dirty" {
		t.Errorf("with another new file: got %q, want %q", got, clean+"-dirty")
	}
}

// The MCP server starts runs from the graphs built into the program, so
// each one must be valid and phase4, its default, must be there.
func TestBuiltInGraphsAreValid(t *testing.T) {
	files, err := graphs.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		data, err := graphs.FS.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := graph.Parse(data); err != nil {
			t.Errorf("%s: %v", f.Name(), err)
		}
		found = found || f.Name() == "phase4.yaml"
	}
	if !found {
		t.Error("phase4.yaml is not built in")
	}
}
