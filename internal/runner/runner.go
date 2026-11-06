// Package runner is the factory runtime. It drives each run by folding the
// event log into state, starting ready nodes, and recording what happens.
// All state lives in the log, so a restarted runner picks up where it stopped.
package runner

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/brokentari/galvantula/internal/event"
	"github.com/brokentari/galvantula/internal/executor"
	"github.com/brokentari/galvantula/internal/graph"
)

// maxInfraFailures stops a node that keeps failing for reasons outside it.
const maxInfraFailures = 5

// ErrInvalid wraps the errors Start returns for a bad graph or fake script.
var ErrInvalid = errors.New("invalid run")

// Runner drives runs. Each run has one goroutine that owns its state.
type Runner struct {
	ctx     context.Context
	store   *event.Store
	workers int

	// Executor builds the executor for a run from its state. Tests replace it.
	Executor func(st *event.State) (executor.Executor, error)

	wg   sync.WaitGroup
	mu   sync.Mutex
	runs map[string]*active
}

type active struct {
	decisions chan decision
	done      chan struct{}
}

type decision struct {
	node     string
	approved bool
	notes    string
	reply    chan error
}

type outcome struct {
	node    graph.Node
	attempt int
	res     executor.Result
	err     error
}

// New returns a runner that stops driving runs when ctx ends.
// workers limits how many nodes of one run execute at the same time.
func New(ctx context.Context, store *event.Store, workers int) *Runner {
	return &Runner{
		ctx:      ctx,
		store:    store,
		workers:  max(workers, 1),
		Executor: fromState,
		runs:     map[string]*active{},
	}
}

func fromState(st *event.State) (executor.Executor, error) {
	if st.Executor != "fake" {
		return nil, fmt.Errorf("unknown executor %q", st.Executor)
	}
	s, err := executor.ParseScript([]byte(st.FakeScript))
	if err != nil {
		return nil, err
	}
	return executor.Fake{Script: s}, nil
}

// Start records a new run of g with the fake executor and starts driving it.
func (r *Runner) Start(g graph.Graph, fakeScript string) (string, error) {
	if err := g.Validate(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if _, err := executor.ParseScript([]byte(fakeScript)); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	id := "r-" + strings.ToLower(rand.Text()[:10])
	data := event.RunCreatedData{Graph: g, Executor: "fake", FakeScript: fakeScript}
	if _, err := r.store.Append(event.New(id, event.RunCreated, "", 0, data)); err != nil {
		return "", err
	}
	r.launch(id)
	return id, nil
}

// ResumeAll drives every run that has not finished, as after a restart.
func (r *Runner) ResumeAll() error {
	runs, err := r.store.Runs()
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.Status == event.RunRunning {
			slog.Info("resuming run", "run", run.ID)
			r.launch(run.ID)
		}
	}
	return nil
}

// Wait blocks until every run this runner drives, and every node it
// started, has stopped.
func (r *Runner) Wait() { r.wg.Wait() }

// Decide records a human decision on a gate that is waiting.
func (r *Runner) Decide(run, node string, approved bool, notes string) error {
	r.mu.Lock()
	a := r.runs[run]
	r.mu.Unlock()
	if a == nil {
		return fmt.Errorf("run %s is not active", run)
	}
	d := decision{node: node, approved: approved, notes: notes, reply: make(chan error, 1)}
	select {
	case a.decisions <- d:
		return <-d.reply
	case <-a.done:
		return fmt.Errorf("run %s is not active", run)
	}
}

func (r *Runner) launch(id string) {
	a := &active{decisions: make(chan decision), done: make(chan struct{})}
	r.mu.Lock()
	r.runs[id] = a
	r.mu.Unlock()
	r.wg.Go(func() {
		defer func() {
			r.mu.Lock()
			delete(r.runs, id)
			r.mu.Unlock()
			close(a.done)
		}()
		if err := r.drive(id, a); err != nil {
			slog.Error("run stopped", "run", id, "err", err)
		}
	})
}

// drive runs one run until it finishes or the runner's context ends.
func (r *Runner) drive(id string, a *active) error {
	events, err := r.store.Events(id, 0)
	if err != nil {
		return err
	}
	st, err := event.Fold(events)
	if err != nil {
		return err
	}
	emit := func(e event.Event) error {
		e, err := r.store.Append(e)
		if err != nil {
			return err
		}
		return st.Apply(e)
	}

	exec, err := r.Executor(st)
	if err != nil {
		return emit(event.New(id, event.RunFinished, "", 0,
			event.RunFinishedData{Status: event.RunFailed, Reason: err.Error()}))
	}

	// A node still marked running was cut off by a crash or a shutdown.
	for _, n := range st.Graph.Nodes {
		if ns := st.Nodes[n.ID]; ns.Status == event.Running {
			err := &executor.InfraError{Err: errors.New("interrupted: the runtime restarted")}
			if err := r.fail(st, emit, n, ns.Attempt, err); err != nil {
				return err
			}
		}
	}

	// Buffered so that workers never block, even after drive returns.
	results := make(chan outcome, r.workers)
	running := 0
	for st.Status == event.RunRunning {
		failed := firstFailed(st)
		if failed == "" {
			for _, n := range ready(st) {
				switch n.Kind {
				case graph.Gate:
					if err := emit(event.New(id, event.GateWaiting, n.ID, 0, nil)); err != nil {
						return err
					}
					continue
				case graph.Slot:
					// Validation puts a slot after its plan node, so this means the plan never filled it.
					d := event.NodeFailedData{Error: "slot was never filled", Class: event.Task}
					if err := emit(event.New(id, event.NodeFailed, n.ID, 0, d)); err != nil {
						return err
					}
					continue
				}
				if running == r.workers {
					continue // gates later in the list still need to wait
				}
				attempt := st.Nodes[n.ID].Attempt + 1
				if err := emit(event.New(id, event.NodeStarted, n.ID, attempt, nil)); err != nil {
					return err
				}
				running++
				r.wg.Go(func() {
					log := func(line string) {
						// Log lines do not change state, so workers append them directly.
						e := event.New(id, event.NodeLog, n.ID, attempt, event.NodeLogData{Line: line})
						if _, err := r.store.Append(e); err != nil {
							slog.Warn("drop log line", "run", id, "node", n.ID, "err", err)
						}
					}
					res, err := exec.Run(r.ctx, executor.Task{Run: id, Node: n, Attempt: attempt}, log)
					results <- outcome{node: n, attempt: attempt, res: res, err: err}
				})
			}
		}

		if running == 0 {
			if status, reason, done := finished(st, failed); done {
				return emit(event.New(id, event.RunFinished, "", 0,
					event.RunFinishedData{Status: status, Reason: reason}))
			}
		}

		select {
		case o := <-results:
			running--
			if r.ctx.Err() != nil {
				// Leave the node marked running, so a restart retries it.
				return nil
			}
			if err := r.complete(st, emit, o); err != nil {
				return err
			}
		case d := <-a.decisions:
			d.reply <- decide(st, emit, d)
		case <-r.ctx.Done():
			return nil
		}
	}
	return nil
}

func (r *Runner) complete(st *event.State, emit func(event.Event) error, o outcome) error {
	if o.err == nil && o.node.Kind == graph.Plan {
		g, err := fillSlot(st.Graph, o.node.ID, o.res.Nodes)
		if err == nil {
			// This one event also marks the plan node succeeded.
			d := event.GraphUpdatedData{Graph: g, Reason: fmt.Sprintf("%s emitted %d nodes", o.node.ID, len(o.res.Nodes))}
			return emit(event.New(st.Run, event.GraphUpdated, o.node.ID, o.attempt, d))
		}
		o.err = fmt.Errorf("plan output rejected: %w", err)
	}
	if o.err == nil {
		return emit(event.New(st.Run, event.NodeSucceeded, o.node.ID, o.attempt, nil))
	}
	return r.fail(st, emit, o.node, o.attempt, o.err)
}

// fail records a failed attempt and decides whether the node will retry.
func (r *Runner) fail(st *event.State, emit func(event.Event) error, n graph.Node, attempt int, err error) error {
	ns := st.Nodes[n.ID]
	d := event.NodeFailedData{Error: err.Error(), Class: event.Task}
	if executor.IsInfra(err) {
		d.Class = event.Infra
		// shortcut: infra retries start again at once; add backoff when real executors hit rate limits (phase 1).
		d.WillRetry = ns.InfraFailures+1 < maxInfraFailures
	} else {
		d.WillRetry = ns.TaskFailures+1 <= n.Retries
	}
	return emit(event.New(st.Run, event.NodeFailed, n.ID, attempt, d))
}

func decide(st *event.State, emit func(event.Event) error, d decision) error {
	if ns, ok := st.Nodes[d.node]; !ok || ns.Status != event.Waiting {
		return fmt.Errorf("node %s is not a waiting gate", d.node)
	}
	data := event.GateDecidedData{Approved: d.approved, Notes: d.notes}
	return emit(event.New(st.Run, event.GateDecided, d.node, 0, data))
}

func fillSlot(g graph.Graph, planID string, nodes []graph.Node) (graph.Graph, error) {
	i := slices.IndexFunc(g.Nodes, func(n graph.Node) bool { return n.Kind == graph.Slot && n.FilledBy == planID })
	if i < 0 {
		return graph.Graph{}, errors.New("no slot is filled by this plan node")
	}
	return g.Fill(g.Nodes[i].ID, nodes)
}

// ready lists pending nodes whose deps have all succeeded, in graph order.
func ready(st *event.State) []graph.Node {
	var out []graph.Node
	for _, n := range st.Graph.Nodes {
		if st.Nodes[n.ID].Status != event.Pending {
			continue
		}
		if !slices.ContainsFunc(n.Deps, func(d string) bool { return st.Nodes[d].Status != event.Succeeded }) {
			out = append(out, n)
		}
	}
	return out
}

func firstFailed(st *event.State) string {
	i := slices.IndexFunc(st.Graph.Nodes, func(n graph.Node) bool { return st.Nodes[n.ID].Status == event.Failed })
	if i < 0 {
		return ""
	}
	return st.Graph.Nodes[i].ID
}

// finished decides the run's outcome. Call it only when nothing is running.
func finished(st *event.State, failed string) (event.RunStatus, string, bool) {
	if failed != "" {
		return event.RunFailed, fmt.Sprintf("node %s failed: %s", failed, st.Nodes[failed].LastError), true
	}
	var stuck []string
	for _, n := range st.Graph.Nodes {
		switch st.Nodes[n.ID].Status {
		case event.Waiting:
			return "", "", false
		case event.Succeeded:
		default:
			stuck = append(stuck, n.ID)
		}
	}
	if len(stuck) > 0 {
		return event.RunFailed, "nodes can never start: " + strings.Join(stuck, ", "), true
	}
	return event.RunSucceeded, "", true
}
