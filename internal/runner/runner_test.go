package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brokentari/galvantula/internal/event"
	"github.com/brokentari/galvantula/internal/executor"
	"github.com/brokentari/galvantula/internal/graph"
)

func openStore(t *testing.T) *event.Store {
	t.Helper()
	s, err := event.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// waitUntil polls the log until cond holds for the run's state and events.
func waitUntil(t *testing.T, s *event.Store, run string, what string, cond func(*event.State, []event.Event) bool) (*event.State, []event.Event) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		evs, err := s.Events(run, 0)
		if err != nil {
			t.Fatal(err)
		}
		st, err := event.Fold(evs)
		if err != nil {
			t.Fatal(err)
		}
		if cond(st, evs) {
			return st, evs
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s; run status %q", what, st.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func nodeIs(id string, status event.NodeStatus) func(*event.State, []event.Event) bool {
	return func(st *event.State, _ []event.Event) bool {
		n, ok := st.Nodes[id]
		return ok && n.Status == status
	}
}

func runDone(st *event.State, _ []event.Event) bool { return st.Status != event.RunRunning }

func mustParse(t *testing.T, yaml string) graph.Graph {
	t.Helper()
	g, err := graph.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// fast plays a run's fake script with every step shortened to 1ms.
func fast(st *event.State) (executor.Executor, error) {
	s, err := executor.ParseScript([]byte(st.FakeScript))
	if err != nil {
		return nil, err
	}
	s.Default.Duration = time.Millisecond
	for id, step := range s.Nodes {
		step.Duration = time.Millisecond
		s.Nodes[id] = step
	}
	return executor.Fake{Script: s}, nil
}

func index(evs []event.Event, typ event.Type, node string) int {
	return slices.IndexFunc(evs, func(e event.Event) bool { return e.Type == typ && e.Node == node })
}

func TestFactoryDemoRun(t *testing.T) {
	g, err := os.ReadFile("../../graphs/factory.yaml")
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("../../fakes/factory.yaml")
	if err != nil {
		t.Fatal(err)
	}
	store := openStore(t)
	r := New(t.Context(), store, 3)
	r.Executor = fast

	run, err := r.Start(mustParse(t, string(g)), string(script))
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, store, run, "the contract gate waits", nodeIs("approve-contract", event.Waiting))
	if err := r.Decide(run, "architect", true, ""); err == nil {
		t.Error("Decide accepted a node that is not a waiting gate")
	}
	if err := r.Decide(run, "approve-contract", true, ""); err != nil {
		t.Fatal(err)
	}

	st, _ := waitUntil(t, store, run, "the DAG gate waits", nodeIs("approve-dag", event.Waiting))
	for _, id := range []string{"pkg-store", "pkg-api", "cmd-cli"} {
		if _, ok := st.Graph.Node(id); !ok {
			t.Errorf("plan output %s is not in the graph", id)
		}
	}
	if _, ok := st.Graph.Node("tasks"); ok {
		t.Error("the tasks slot is still in the graph")
	}
	if err := r.Decide(run, "approve-dag", true, "looks right"); err != nil {
		t.Fatal(err)
	}

	st, evs := waitUntil(t, store, run, "the run finishes", runDone)
	if st.Status != event.RunSucceeded {
		t.Fatalf("run %s: %s", st.Status, st.Reason)
	}

	// In parallel: both packages start before either one finishes.
	firstEnd := min(index(evs, event.NodeSucceeded, "pkg-store"), index(evs, event.NodeFailed, "pkg-api"))
	if index(evs, event.NodeStarted, "pkg-store") > firstEnd || index(evs, event.NodeStarted, "pkg-api") > firstEnd {
		t.Error("pkg-store and pkg-api did not run at the same time")
	}
	// Retry: pkg-api failed once, then passed on attempt 2.
	if api := st.Nodes["pkg-api"]; api.TaskFailures != 1 || api.Attempt != 2 {
		t.Errorf("pkg-api: %d task failures, attempt %d; want 1 and 2", api.TaskFailures, api.Attempt)
	}
	// Dependents wait for every dependency.
	if index(evs, event.NodeStarted, "cmd-cli") < index(evs, event.NodeSucceeded, "pkg-api") {
		t.Error("cmd-cli started before pkg-api succeeded")
	}
}

// blockOnce blocks the first attempt of one node until the runner stops.
type blockOnce struct{ node string }

func (b blockOnce) Run(ctx context.Context, t executor.Task, _ func(string)) (executor.Result, error) {
	if t.Node.ID == b.node && t.Attempt == 1 {
		<-ctx.Done()
		return executor.Result{}, ctx.Err()
	}
	return executor.Result{}, nil
}

func TestResumeAfterRestart(t *testing.T) {
	store := openStore(t)
	g := mustParse(t, "name: g\nnodes:\n  - {id: a, kind: tool}\n  - {id: b, kind: tool, deps: [a]}\n  - {id: c, kind: tool, deps: [b]}")

	ctx, stop := context.WithCancel(t.Context())
	first := New(ctx, store, 3)
	first.Executor = func(*event.State) (executor.Executor, error) { return blockOnce{node: "b"}, nil }
	run, err := first.Start(g, "")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, store, run, "b runs", nodeIs("b", event.Running))
	stop()
	first.Wait()
	if st, _ := waitUntil(t, store, run, "the log is read", func(*event.State, []event.Event) bool { return true }); st.Nodes["b"].Status != event.Running {
		t.Fatalf("after shutdown b is %s, want running", st.Nodes["b"].Status)
	}

	second := New(t.Context(), store, 3)
	second.Executor = first.Executor
	if err := second.ResumeAll(); err != nil {
		t.Fatal(err)
	}
	st, evs := waitUntil(t, store, run, "the resumed run finishes", runDone)
	if st.Status != event.RunSucceeded {
		t.Fatalf("run %s: %s", st.Status, st.Reason)
	}
	if b := st.Nodes["b"]; b.InfraFailures != 1 || b.TaskFailures != 0 || b.Attempt != 2 {
		t.Errorf("b: %d infra failures, %d task failures, attempt %d; want 1, 0, 2", b.InfraFailures, b.TaskFailures, b.Attempt)
	}
	starts := 0
	for _, e := range evs {
		if e.Type == event.NodeStarted && e.Node == "a" {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("a started %d times, want 1: finished work must not run again", starts)
	}
}

func TestRunFails(t *testing.T) {
	tests := []struct {
		name   string
		graph  string
		script string
		reject string // gate to reject
		want   string
	}{
		{
			name:   "retries run out",
			graph:  "name: g\nnodes:\n  - {id: a, kind: agent, retries: 1}\n  - {id: b, kind: tool, deps: [a]}",
			script: "nodes:\n  a: {fail_attempts: 5, error: tests fail}",
			want:   "node a failed: tests fail",
		},
		{
			name:   "gate rejected",
			graph:  "name: g\nnodes:\n  - {id: a, kind: agent}\n  - {id: ok, kind: gate, deps: [a]}",
			reject: "ok",
			want:   "node ok failed: rejected: wrong interfaces",
		},
		{
			name:   "plan output invalid",
			graph:  "name: g\nnodes:\n  - {id: p, kind: plan}\n  - {id: s, kind: slot, filled_by: p, deps: [p]}",
			script: "nodes:\n  p:\n    emit:\n      - {id: x, kind: agent, deps: [p]}",
			want:   "plan output rejected",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openStore(t)
			r := New(t.Context(), store, 3)
			r.Executor = fast
			run, err := r.Start(mustParse(t, tt.graph), tt.script)
			if err != nil {
				t.Fatal(err)
			}
			if tt.reject != "" {
				waitUntil(t, store, run, "the gate waits", nodeIs(tt.reject, event.Waiting))
				if err := r.Decide(run, tt.reject, false, "wrong interfaces"); err != nil {
					t.Fatal(err)
				}
			}
			st, _ := waitUntil(t, store, run, "the run finishes", runDone)
			if st.Status != event.RunFailed || !strings.Contains(st.Reason, tt.want) {
				t.Fatalf("run %s (%s), want failed with %q", st.Status, st.Reason, tt.want)
			}
		})
	}
}

// A crash right after a plan node's graph_updated event must not run the
// plan again: that one event records both the new nodes and the success.
func TestResumeAfterPlanEmitted(t *testing.T) {
	store := openStore(t)
	g := mustParse(t, "name: g\nnodes:\n  - {id: p, kind: plan}\n  - {id: s, kind: slot, filled_by: p, deps: [p]}\n  - {id: v, kind: tool, deps: [s]}")
	filled, err := g.Fill("s", []graph.Node{{ID: "x", Kind: graph.Agent}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []event.Event{
		event.New("r", event.RunCreated, "", 0, event.RunCreatedData{Graph: g, Executor: "fake"}),
		event.New("r", event.NodeStarted, "p", 1, nil),
		event.New("r", event.GraphUpdated, "p", 1, event.GraphUpdatedData{Graph: filled, Reason: "p emitted 1 nodes"}),
	} {
		if _, err := store.Append(e); err != nil {
			t.Fatal(err)
		}
	}

	r := New(t.Context(), store, 3)
	r.Executor = fast
	if err := r.ResumeAll(); err != nil {
		t.Fatal(err)
	}
	st, evs := waitUntil(t, store, "r", "the resumed run finishes", runDone)
	if st.Status != event.RunSucceeded {
		t.Fatalf("run %s: %s", st.Status, st.Reason)
	}
	if i := index(evs, event.NodeStarted, "p"); slices.ContainsFunc(evs[i+1:], func(e event.Event) bool { return e.Type == event.NodeStarted && e.Node == "p" }) {
		t.Error("the plan node ran again after its graph_updated event")
	}
}
