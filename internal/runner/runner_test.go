package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brokentari/galvantula/internal/event"
	"github.com/brokentari/galvantula/internal/executor"
	"github.com/brokentari/galvantula/internal/graph"
	"github.com/brokentari/galvantula/internal/guardrails"
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
	r.Commit = "abc1234"

	run, err := r.Start(Run{Graph: mustParse(t, string(g)), Executor: "fake", FakeScript: string(script)})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, store, run, "the contract gate waits", nodeIs("approve-contract", event.Waiting))
	if err := r.Decide(run, "architect", true, "", nil); err == nil {
		t.Error("Decide accepted a node that is not a waiting gate")
	}
	if err := r.Decide(run, "approve-contract", true, "", nil); err != nil {
		t.Fatal(err)
	}

	st, _ := waitUntil(t, store, run, "the task-graph gate waits", nodeIs("approve-tasks", event.Waiting))
	for _, id := range []string{"pkg-store", "pkg-api", "cmd-cli"} {
		if _, ok := st.Graph.Node(id); !ok {
			t.Errorf("plan output %s is not in the graph", id)
		}
	}
	if _, ok := st.Graph.Node("tasks"); ok {
		t.Error("the tasks slot is still in the graph")
	}
	if err := r.Decide(run, "approve-tasks", true, "looks right", nil); err != nil {
		t.Fatal(err)
	}

	st, evs := waitUntil(t, store, run, "the run finishes", runDone)
	if st.Status != event.RunSucceeded {
		t.Fatalf("run %s: %s", st.Status, st.Reason)
	}
	if st.Commit != "abc1234" {
		t.Errorf("run recorded commit %q, want the runner's abc1234", st.Commit)
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
	run, err := first.Start(Run{Graph: g, Executor: "fake"})
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
			name:   "a node fails while a gate waits",
			graph:  "name: g\nnodes:\n  - {id: a, kind: agent}\n  - {id: ok, kind: gate}",
			script: "nodes:\n  a: {fail_attempts: 1, error: tests fail}",
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
			run, err := r.Start(Run{Graph: mustParse(t, tt.graph), Executor: "fake", FakeScript: tt.script})
			if err != nil {
				t.Fatal(err)
			}
			if tt.reject != "" {
				waitUntil(t, store, run, "the gate waits", nodeIs(tt.reject, event.Waiting))
				if err := r.Decide(run, tt.reject, false, "wrong interfaces", nil); err != nil {
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

// tasks records every task it gets, and when. errs[i] fails attempt i+1;
// later attempts succeed. Each attempt costs $1.
type tasks struct {
	mu   sync.Mutex
	seen []executor.Task
	at   []time.Time
	errs []error
}

func (x *tasks) Run(_ context.Context, t executor.Task, _ func(string)) (executor.Result, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.seen = append(x.seen, t)
	x.at = append(x.at, time.Now())
	if i := t.Attempt - 1; i < len(x.errs) {
		return executor.Result{CostUSD: 1}, x.errs[i]
	}
	return executor.Result{CostUSD: 1}, nil
}

func checkFailed(attempt int) error { return fmt.Errorf("check failed on attempt %d", attempt) }

var apiDown = &executor.InfraError{Err: errors.New("api down")}

// An infra failure between a task failure and its retry must not lose the
// check output or the session.
func TestRetryAfterInfraKeepsTaskFeedback(t *testing.T) {
	store := openStore(t)
	x := &tasks{errs: []error{checkFailed(1), apiDown}}
	r := New(t.Context(), store, 1)
	r.Executor = func(*event.State) (executor.Executor, error) { return x, nil }
	run, err := r.Start(Run{Graph: mustParse(t, "name: g\nnodes:\n  - {id: a, kind: agent, retries: 1}"), Executor: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := waitUntil(t, store, run, "the run finishes", runDone); st.Status != event.RunSucceeded {
		t.Fatalf("run %s (%s); want succeeded", st.Status, st.Reason)
	}
	if got := x.seen[2]; !got.Resume || got.Feedback != "check failed on attempt 1" {
		t.Errorf("attempt 3: resume %v, feedback %q; want a resume with the check output", got.Resume, got.Feedback)
	}
}

// The first infra retry starts at once. Later ones wait, and a shutdown
// during the wait stops the node without waiting it out.
func TestInfraRetriesBackOff(t *testing.T) {
	store := openStore(t)
	g := mustParse(t, "name: g\nnodes:\n  - {id: a, kind: agent}")
	x := &tasks{errs: []error{apiDown, apiDown}}
	r := New(t.Context(), store, 1)
	r.infraBackoff = 2 * time.Second
	r.Executor = func(*event.State) (executor.Executor, error) { return x, nil }
	run, err := r.Start(Run{Graph: g, Executor: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := waitUntil(t, store, run, "the run finishes", runDone); st.Status != event.RunSucceeded || len(x.at) != 3 {
		t.Fatalf("run %s (%s) after %d attempts; want succeeded after 3", st.Status, st.Reason, len(x.at))
	}
	if d := x.at[1].Sub(x.at[0]); d >= r.infraBackoff/2 {
		t.Errorf("the first infra retry waited %s; it must start at once", d)
	}
	if d := x.at[2].Sub(x.at[1]); d < r.infraBackoff {
		t.Errorf("the second infra retry waited %s, want at least %s", d, r.infraBackoff)
	}

	ctx, stop := context.WithCancel(t.Context())
	slow := New(ctx, store, 1)
	slow.infraBackoff = time.Hour
	slow.Executor = func(*event.State) (executor.Executor, error) {
		return &tasks{errs: []error{apiDown, apiDown, apiDown, apiDown, apiDown}}, nil
	}
	if run, err = slow.Start(Run{Graph: g, Executor: "fake"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, store, run, "attempt 3 waits", func(st *event.State, _ []event.Event) bool { return st.Nodes["a"].Attempt == 3 })
	stop()
	done := make(chan struct{})
	go func() { slow.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the shutdown waited for the backoff")
	}
}

// The first retry resumes with the failure as feedback; the next starts over.
func TestRetriesResumeThenStartOver(t *testing.T) {
	store := openStore(t)
	x := &tasks{errs: []error{checkFailed(1), checkFailed(2)}}
	r := New(t.Context(), store, 1)
	r.Executor = func(*event.State) (executor.Executor, error) { return x, nil }
	run, err := r.Start(Run{Graph: mustParse(t, "name: g\nnodes:\n  - {id: a, kind: agent, retries: 2}"), Executor: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := waitUntil(t, store, run, "the run finishes", runDone)
	if st.Status != event.RunSucceeded || st.CostUSD != 3 {
		t.Fatalf("run %s (%s), cost %v; want succeeded at 3", st.Status, st.Reason, st.CostUSD)
	}
	want := []struct {
		resume   bool
		feedback string
	}{{false, ""}, {true, "check failed on attempt 1"}, {false, "check failed on attempt 2"}}
	for i, w := range want {
		if got := x.seen[i]; got.Resume != w.resume || got.Feedback != w.feedback {
			t.Errorf("attempt %d: resume %v, feedback %q; want %v, %q", i+1, got.Resume, got.Feedback, w.resume, w.feedback)
		}
	}
}

func TestRunBudgetStopsNewNodes(t *testing.T) {
	store := openStore(t)
	r := New(t.Context(), store, 1)
	r.Executor = fast
	var rails guardrails.Guardrails
	rails.Budget.RunUSD = 4
	run, err := r.Start(Run{
		Graph:      mustParse(t, "name: g\nnodes:\n  - {id: a, kind: agent}\n  - {id: b, kind: agent, deps: [a]}"),
		Executor:   "fake",
		FakeScript: "nodes:\n  a: {cost_usd: 5}",
		Rails:      rails,
	})
	if err != nil {
		t.Fatal(err)
	}
	st, evs := waitUntil(t, store, run, "the run finishes", runDone)
	if st.Status != event.RunFailed || !strings.Contains(st.Reason, "budget") {
		t.Fatalf("run %s (%s); want failed on the budget", st.Status, st.Reason)
	}
	if index(evs, event.NodeStarted, "b") >= 0 {
		t.Error("b started after the budget was spent")
	}
}

// The last node can take the cost past the budget. Its work is merged, so
// the run succeeded.
func TestLastNodeOverBudgetSucceeds(t *testing.T) {
	store := openStore(t)
	r := New(t.Context(), store, 1)
	r.Executor = fast
	var rails guardrails.Guardrails
	rails.Budget.RunUSD = 4
	run, err := r.Start(Run{
		Graph:      mustParse(t, "name: g\nnodes:\n  - {id: a, kind: agent}"),
		Executor:   "fake",
		FakeScript: "nodes:\n  a: {cost_usd: 5}",
		Rails:      rails,
	})
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := waitUntil(t, store, run, "the run finishes", runDone); st.Status != event.RunSucceeded {
		t.Fatalf("run %s (%s); want succeeded", st.Status, st.Reason)
	}
}

// A restart without the credential must not fail a claude run that is
// half done. The run waits for a start that has the credential.
func TestResumeWithoutCredentialKeepsRun(t *testing.T) {
	store := openStore(t)
	data := event.RunCreatedData{Graph: mustParse(t, "name: g\nnodes:\n  - {id: a, kind: agent}"), Executor: "claude", Spec: "x"}
	if _, err := store.Append(event.New("r-1", event.RunCreated, "", 0, data)); err != nil {
		t.Fatal(err)
	}
	r := New(t.Context(), store, 1)
	if err := r.ResumeAll(); err != nil {
		t.Fatal(err)
	}
	r.Wait()
	if st, _ := waitUntil(t, store, "r-1", "the log is read", func(*event.State, []event.Event) bool { return true }); st.Status != event.RunRunning {
		t.Fatalf("run %s (%s); want it still running", st.Status, st.Reason)
	}
}

type limited struct{}

func (limited) Run(context.Context, executor.Task, func(string)) (executor.Result, error) {
	return executor.Result{}, &executor.LimitError{Err: errors.New("You've hit your usage limit")}
}

// A usage limit must stop the run at once: a retry cannot help, and must
// never buy more usage.
func TestUsageLimitStopsWithoutRetry(t *testing.T) {
	store := openStore(t)
	r := New(t.Context(), store, 1)
	r.Executor = func(*event.State) (executor.Executor, error) { return limited{}, nil }
	run, err := r.Start(Run{Graph: mustParse(t, "name: g\nnodes:\n  - {id: a, kind: agent, retries: 2}"), Executor: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := waitUntil(t, store, run, "the run finishes", runDone)
	a := st.Nodes["a"]
	if st.Status != event.RunFailed || a.Attempt != 1 || a.LastClass != event.Limit {
		t.Errorf("run %s, node attempts %d, class %q; want failed after 1 attempt with class limit", st.Status, a.Attempt, a.LastClass)
	}
}

// An advisory node that fails for good does not fail the run. The tests,
// not the review, decide the outcome.
func TestAdvisoryNodeCannotFailTheRun(t *testing.T) {
	store := openStore(t)
	r := New(t.Context(), store, 1)
	r.Executor = fast
	run, err := r.Start(Run{
		Graph:      mustParse(t, "name: g\nnodes:\n  - {id: review, kind: agent, advisory: true, retries: 1}\n  - {id: verify, kind: tool, deps: [review]}"),
		Executor:   "fake",
		FakeScript: "nodes:\n  review: {fail_attempts: 9, error: the review crashed}",
	})
	if err != nil {
		t.Fatal(err)
	}
	st, evs := waitUntil(t, store, run, "the run finishes", runDone)
	if st.Status != event.RunSucceeded {
		t.Fatalf("run %s: %s; an advisory failure must not fail it", st.Status, st.Reason)
	}
	if n := st.Nodes["review"]; n.TaskFailures != 2 || n.LastError != "the review crashed" {
		t.Errorf("review: %d task failures, error %q; want 2 and the crash recorded", n.TaskFailures, n.LastError)
	}
	if index(evs, event.NodeStarted, "verify") < 0 {
		t.Error("verify never started")
	}
}

// A run with AutoApprove gets through its gates with no human, and the log
// says that the runtime decided.
func TestAutoApproveGates(t *testing.T) {
	store := openStore(t)
	r := New(t.Context(), store, 1)
	r.Executor = fast
	run, err := r.Start(Run{
		Graph:       mustParse(t, "name: g\nnodes:\n  - {id: a, kind: tool}\n  - {id: ok, kind: gate, deps: [a]}\n  - {id: b, kind: tool, deps: [ok]}"),
		Executor:    "fake",
		AutoApprove: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	st, evs := waitUntil(t, store, run, "the run finishes", runDone)
	if st.Status != event.RunSucceeded {
		t.Fatalf("run %s: %s", st.Status, st.Reason)
	}
	i := index(evs, event.GateDecided, "ok")
	if i < 0 {
		t.Fatal("the gate was never decided")
	}
	if d, _ := evs[i].Decode[event.GateDecidedData](); !d.Approved || !strings.Contains(d.Notes, "runtime") {
		t.Errorf("decision %+v, want an approval that names the runtime", d)
	}
}

// A crash between GateWaiting and the runtime's own GateDecided must not leave
// an auto-approved run waiting for a human who is not there.
func TestAutoApprovedGateResumesAfterCrash(t *testing.T) {
	store := openStore(t)
	g := mustParse(t, "name: g\nnodes:\n  - {id: a, kind: tool}\n  - {id: ok, kind: gate, deps: [a]}\n  - {id: b, kind: tool, deps: [ok]}")
	for _, e := range []event.Event{
		event.New("r", event.RunCreated, "", 0, event.RunCreatedData{Graph: g, Executor: "fake", AutoApprove: true}),
		event.New("r", event.NodeStarted, "a", 1, nil),
		event.New("r", event.NodeSucceeded, "a", 1, nil),
		event.New("r", event.GateWaiting, "ok", 0, nil),
	} {
		if _, err := store.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	r := New(t.Context(), store, 1)
	r.Executor = fast
	if err := r.ResumeAll(); err != nil {
		t.Fatal(err)
	}
	if st, _ := waitUntil(t, store, "r", "the resumed run finishes", runDone); st.Status != event.RunSucceeded {
		t.Fatalf("run %s: %s", st.Status, st.Reason)
	}
}

const replanGraph = `
name: g
nodes:
  - {id: p, kind: plan}
  - {id: s, kind: slot, filled_by: p, deps: [p]}
  - {id: v, kind: tool, deps: [s]}
`

// replanScript has a plan that emits task a, and b after it. Task a always fails.
// The first replan answers with the nodes in again.
func replanScript(again string) string {
	return `
nodes:
  p:
    emit:
      - {id: a, kind: agent, role: implementer, scope: [pa], retries: 1}
      - {id: b, kind: agent, role: implementer, scope: [pb], deps: [a]}
      - {id: c, kind: agent, role: implementer, scope: [pc]}
  a: {fail_attempts: 99, error: wrong scope}
  replan-1:
    emit: ` + again + `
`
}

func startReplan(t *testing.T, replans int, script string) (*event.Store, string) {
	t.Helper()
	store := openStore(t)
	r := New(t.Context(), store, 3)
	r.Executor = fast
	rails := guardrails.Guardrails{Budget: guardrails.Budget{Replans: replans}}
	id, err := r.Start(Run{Graph: mustParse(t, replanGraph), Executor: "fake", FakeScript: script, Rails: rails})
	if err != nil {
		t.Fatal(err)
	}
	return store, id
}

// A task that fails its last retry is replaced, with its dependents, and the run goes on.
func TestReplanReplacesFailedTask(t *testing.T) {
	store, id := startReplan(t, 1, replanScript(`
      - {id: a2, kind: agent, role: implementer, scope: [pa]}
      - {id: b2, kind: agent, role: implementer, scope: [pb], deps: [a2]}`))
	st, evs := waitUntil(t, store, id, "the run finishes", runDone)
	if st.Status != event.RunSucceeded {
		t.Fatalf("run %s: %s", st.Status, st.Reason)
	}
	if st.Replans != 1 || st.GraphVersion != 4 || !st.Retired["a"] || !st.Retired["b"] {
		t.Errorf("replans %d, version %d, retired %v", st.Replans, st.GraphVersion, st.Retired)
	}
	for _, n := range []string{"a2", "b2", "c", "replan-1"} {
		if st.Nodes[n].Status != event.Succeeded {
			t.Errorf("node %s is %s", n, st.Nodes[n].Status)
		}
	}
	i := slices.IndexFunc(evs, func(e event.Event) bool {
		d, _ := e.Decode[event.GraphUpdatedData]()
		return e.Type == event.GraphUpdated && len(d.Replaced) > 0
	})
	d, _ := evs[i].Decode[event.GraphUpdatedData]()
	if !slices.Equal(d.Replaced, []string{"a", "b"}) || !strings.Contains(d.Reason, "wrong scope") {
		t.Errorf("replan event: replaced %v, reason %q", d.Replaced, d.Reason)
	}
}

// Past the limit, a failed task fails the run.
func TestReplansAreBounded(t *testing.T) {
	store, id := startReplan(t, 1, replanScript(`
      - {id: a, kind: agent, role: implementer, scope: [pa]}`)+`
  a2: {fail_attempts: 99}
`)
	st, _ := waitUntil(t, store, id, "the run finishes", runDone)
	if st.Status != event.RunFailed {
		t.Fatalf("run %s, want failed", st.Status)
	}
}

// A new task may not reuse the id of a task that a replan replaced.
func TestReplanRejectsRetiredIDs(t *testing.T) {
	store, id := startReplan(t, 1, replanScript(`
      - {id: a, kind: agent, role: implementer, scope: [pa]}`))
	st, evs := waitUntil(t, store, id, "the run finishes", runDone)
	if st.Status != event.RunFailed || st.Nodes["replan-1"].Status != event.Failed {
		t.Fatalf("run %s, replan-1 %s: %s", st.Status, st.Nodes["replan-1"].Status, st.Reason)
	}
	if !slices.ContainsFunc(evs, func(e event.Event) bool {
		return e.Type == event.NodeFailed && strings.Contains(string(e.Data), "replaced")
	}) {
		t.Error("no failure names the retired id")
	}
}

// A replan is not an answer to a failure that is not the task's: a usage limit
// stops the run.
func TestReplanIgnoresLimitFailures(t *testing.T) {
	store := openStore(t)
	g := mustParse(t, "name: g\nnodes:\n  - {id: a, kind: agent, role: implementer, scope: [pa]}")
	r := New(t.Context(), store, 3)
	r.Executor = func(*event.State) (executor.Executor, error) { return limited{}, nil }
	rails := guardrails.Guardrails{Budget: guardrails.Budget{Replans: 2}}
	id, err := r.Start(Run{Graph: g, Executor: "fake", Rails: rails})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := waitUntil(t, store, id, "the run finishes", runDone)
	if st.Status != event.RunFailed || st.Replans != 0 {
		t.Errorf("run %s with %d replans", st.Status, st.Replans)
	}
}

// A task that stays may fail while a replan waits to start. The planner must
// not wait for it, and the failure gets its own replan.
func TestReplanWhileAKeptTaskFails(t *testing.T) {
	store := openStore(t)
	r := New(t.Context(), store, 3)
	r.Executor = fast
	script := `
nodes:
  p:
    emit:
      - {id: r, kind: agent, role: implementer, scope: [pr]}
      - {id: k, kind: agent, role: implementer, scope: [pk]}
      - {id: d, kind: agent, role: implementer, scope: [pd], deps: [r, k]}
  r: {fail_attempts: 99}
  k: {fail_attempts: 99}
  replan-1:
    emit:
      - {id: r2, kind: agent, role: implementer, scope: [pr]}
      - {id: d2, kind: agent, role: implementer, scope: [pd], deps: [r2]}
  replan-2:
    emit:
      - {id: k2, kind: agent, role: implementer, scope: [pk]}
`
	rails := guardrails.Guardrails{Budget: guardrails.Budget{Replans: 2}}
	id, err := r.Start(Run{Graph: mustParse(t, replanGraph), Executor: "fake", FakeScript: script, Rails: rails})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := waitUntil(t, store, id, "the run finishes", runDone)
	if st.Status != event.RunSucceeded || st.Replans != 2 {
		t.Fatalf("run %s with %d replans: %s", st.Status, st.Replans, st.Reason)
	}
}

// A crash after a task's last failure and before the replan must not lose the replan.
func TestReplanAfterRestart(t *testing.T) {
	store := openStore(t)
	g := mustParse(t, "name: g\nnodes:\n  - {id: a, kind: agent, role: implementer, scope: [pa]}\n  - {id: v, kind: tool, deps: [a]}")
	script := "nodes:\n  replan-1:\n    emit:\n      - {id: a2, kind: agent, role: implementer, scope: [pa]}\n"
	for _, e := range []event.Event{
		event.New("r", event.RunCreated, "", 0, event.RunCreatedData{Graph: g, Executor: "fake", FakeScript: script, Guardrails: guardrails.Guardrails{Budget: guardrails.Budget{Replans: 1}}}),
		event.New("r", event.NodeStarted, "a", 1, nil),
		event.New("r", event.NodeFailed, "a", 1, event.NodeFailedData{Error: "boom", Class: event.Task}),
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
	st, _ := waitUntil(t, store, "r", "the resumed run finishes", runDone)
	if st.Status != event.RunSucceeded || st.Replans != 1 {
		t.Fatalf("run %s with %d replans: %s", st.Status, st.Replans, st.Reason)
	}
}

const reviseGraph = `
name: g
nodes:
  - {id: a, kind: agent, role: architect}
  - {id: g, kind: gate, deps: [a]}
  - {id: v, kind: tool, deps: [g]}
`

func startRevise(t *testing.T, revisions int, x *tasks) (*Runner, *event.Store, string) {
	t.Helper()
	store := openStore(t)
	r := New(t.Context(), store, 3)
	r.Executor = func(*event.State) (executor.Executor, error) { return x, nil }
	rails := guardrails.Guardrails{Budget: guardrails.Budget{Revisions: revisions}}
	id, err := r.Start(Run{Graph: mustParse(t, reviseGraph), Executor: "fake", Rails: rails})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, store, id, "the gate waits", nodeIs("g", event.Waiting))
	return r, store, id
}

// A rejection with notes sends the work back to the node before the gate.
func TestRejectionWithNotesSendsWorkBack(t *testing.T) {
	x := &tasks{}
	r, store, id := startRevise(t, 1, x)
	if err := r.Decide(id, "g", false, "split the parser out", nil); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, store, id, "the gate waits again", func(st *event.State, _ []event.Event) bool {
		return st.Revisions == 1 && st.Nodes["g"].Status == event.Waiting && st.Nodes["a"].Attempt == 2
	})
	if err := r.Decide(id, "g", true, "", nil); err != nil {
		t.Fatal(err)
	}
	st, _ := waitUntil(t, store, id, "the run finishes", runDone)
	if st.Status != event.RunSucceeded {
		t.Fatalf("run %s: %s", st.Status, st.Reason)
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	var second executor.Task
	for _, t := range x.seen {
		if t.Node.ID == "a" && t.Attempt == 2 {
			second = t
		}
	}
	if !second.Revision || second.Resume || !strings.Contains(second.Feedback, "split the parser out") {
		t.Errorf("the second attempt got %+v", second)
	}
}

// gate_waiting says how many revisions are left: the budget after an agent,
// one less after a send-back, and 0 after a tool node, which a rejection cannot send back.
func TestGateWaitingSaysRevisionsLeft(t *testing.T) {
	left := func(evs []event.Event) []int {
		var out []int
		for _, e := range evs {
			if e.Type == event.GateWaiting {
				d, err := e.Decode[event.GateWaitingData]()
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, d.RevisionsLeft)
			}
		}
		return out
	}
	r, store, id := startRevise(t, 2, &tasks{})
	if err := r.Decide(id, "g", false, "split the parser out", nil); err != nil {
		t.Fatal(err)
	}
	_, evs := waitUntil(t, store, id, "the gate waits again", func(st *event.State, _ []event.Event) bool {
		return st.Revisions == 1 && st.Nodes["g"].Status == event.Waiting
	})
	if got := left(evs); !slices.Equal(got, []int{2, 1}) {
		t.Errorf("after an agent node, revisions left %v, want [2 1]", got)
	}

	store = openStore(t)
	r = New(t.Context(), store, 3)
	r.Executor = func(*event.State) (executor.Executor, error) { return &tasks{}, nil }
	rails := guardrails.Guardrails{Budget: guardrails.Budget{Revisions: 2}}
	id, err := r.Start(Run{Graph: mustParse(t, "name: g\nnodes:\n  - {id: a, kind: tool}\n  - {id: g, kind: gate, deps: [a]}"), Executor: "fake", Rails: rails})
	if err != nil {
		t.Fatal(err)
	}
	_, evs = waitUntil(t, store, id, "the gate waits", nodeIs("g", event.Waiting))
	if got := left(evs); !slices.Equal(got, []int{0}) {
		t.Errorf("after a tool node, revisions left %v, want [0]", got)
	}
}

// Without notes, or past the limit, a rejection ends the run.
func TestRejectionEndsTheRun(t *testing.T) {
	for name, tt := range map[string]struct {
		revisions int
		notes     string
	}{"no notes": {1, " "}, "no revisions left": {0, "try again"}} {
		t.Run(name, func(t *testing.T) {
			r, store, id := startRevise(t, tt.revisions, &tasks{})
			if err := r.Decide(id, "g", false, tt.notes, nil); err != nil {
				t.Fatal(err)
			}
			st, _ := waitUntil(t, store, id, "the run finishes", runDone)
			if st.Status != event.RunFailed || st.Revisions != 0 {
				t.Errorf("run %s with %d revisions", st.Status, st.Revisions)
			}
		})
	}
}
