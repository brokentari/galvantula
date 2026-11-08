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
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/brokentari/galvantula/internal/event"
	"github.com/brokentari/galvantula/internal/executor"
	"github.com/brokentari/galvantula/internal/graph"
	"github.com/brokentari/galvantula/internal/guardrails"
	"github.com/brokentari/galvantula/internal/plan"
)

const (
	// maxInfraFailures stops a node that keeps failing for reasons outside it.
	maxInfraFailures = 5
	// maxBackoff caps the wait between infra retries.
	maxBackoff = 2 * time.Minute
)

// autoApproved is the note on a gate decision that the runtime made, for a run with no human.
const autoApproved = "approved by the runtime: this run has no human"

// ErrInvalid wraps the errors Start returns for a run that cannot start.
var ErrInvalid = errors.New("invalid run")

// Runner drives runs. Each run has one goroutine that owns its state.
type Runner struct {
	ctx     context.Context
	store   *event.Store
	workers int
	// infraBackoff is the wait before the second retry after infra failures.
	// It doubles each time, up to maxBackoff, so a rate limit can pass.
	infraBackoff time.Duration

	// Executor builds the executor for a run from its state. Tests replace it.
	Executor func(st *event.State) (executor.Executor, error)
	// Root holds each run's product repo and worktrees.
	Root string
	// Auth is the credential for sandboxed agents:
	// CLAUDE_CODE_OAUTH_TOKEN=... or ANTHROPIC_API_KEY=...
	Auth string
	// Commit names the code this runner runs. Each run records it.
	Commit string

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
	r := &Runner{
		ctx:          ctx,
		store:        store,
		workers:      max(workers, 1),
		infraBackoff: 5 * time.Second,
		Root:         "runs",
		runs:         map[string]*active{},
	}
	r.Executor = r.fromState
	return r
}

func (r *Runner) fromState(st *event.State) (executor.Executor, error) {
	switch st.Executor {
	case "fake":
		s, err := executor.ParseScript([]byte(st.FakeScript))
		if err != nil {
			return nil, err
		}
		return executor.Fake{Script: s}, nil
	case "claude":
		if r.Auth == "" {
			return nil, errNoAuth
		}
		return executor.Claude{Root: r.Root, Rails: st.Rails, Spec: st.Spec, PlantedPlan: st.PlantedPlan, Auth: r.Auth}, nil
	}
	return nil, fmt.Errorf("unknown executor %q", st.Executor)
}

var errNoAuth = errors.New("no credential for agents: set CLAUDE_CODE_OAUTH_TOKEN (from claude setup-token) or ANTHROPIC_API_KEY before factory serve")

// Run says how to start a run. Executor is "fake", which plays FakeScript,
// or "claude", which builds Spec under Rails.
type Run struct {
	Graph      graph.Graph
	Executor   string
	FakeScript string
	Spec       string
	Rails      guardrails.Guardrails
	// AutoApprove approves every gate without a human. Eval runs use it.
	AutoApprove bool
	// PlantedPlan is a plan.Output as JSON. The claude executor uses it for the
	// first plan node. Eval runs use it.
	PlantedPlan string
}

// Start records a new run and starts driving it.
func (r *Runner) Start(run Run) (string, error) {
	invalid := func(err error) (string, error) { return "", fmt.Errorf("%w: %w", ErrInvalid, err) }
	if err := run.Graph.Validate(); err != nil {
		return invalid(err)
	}
	switch run.Executor {
	case "fake":
		if _, err := executor.ParseScript([]byte(run.FakeScript)); err != nil {
			return invalid(err)
		}
	case "claude":
		if strings.TrimSpace(run.Spec) == "" {
			return invalid(errors.New("the claude executor needs a spec"))
		}
		if err := run.Rails.Validate(); err != nil {
			return invalid(err)
		}
		if run.PlantedPlan != "" {
			if _, err := plan.Parse(run.PlantedPlan); err != nil {
				return invalid(fmt.Errorf("planted plan: %w", err))
			}
		}
		if r.Auth == "" {
			return invalid(errNoAuth)
		}
	default:
		return invalid(fmt.Errorf("unknown executor %q", run.Executor))
	}
	id := "r-" + strings.ToLower(rand.Text()[:10])
	data := event.RunCreatedData{Graph: run.Graph, Executor: run.Executor, FakeScript: run.FakeScript, Spec: run.Spec, Guardrails: run.Rails, Commit: r.Commit, AutoApprove: run.AutoApprove, PlantedPlan: run.PlantedPlan}
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
	if errors.Is(err, errNoAuth) {
		// Leave the run as it is. It resumes at the next start that has a credential.
		slog.Warn("cannot resume run", "run", id, "err", err)
		return nil
	}
	if err != nil {
		return emit(event.New(id, event.RunFinished, "", 0,
			event.RunFinishedData{Status: event.RunFailed, Reason: err.Error()}))
	}

	// A gate still waiting in an auto-approved run lost its decision to a crash.
	if st.AutoApprove {
		for _, n := range st.Graph.Nodes {
			if st.Nodes[n.ID].Status == event.Waiting {
				d := event.GateDecidedData{Approved: true, Notes: autoApproved}
				if err := emit(event.New(id, event.GateDecided, n.ID, 0, d)); err != nil {
					return err
				}
			}
		}
	}

	// A node still marked running was cut off by a crash or a shutdown.
	for _, n := range st.Graph.Nodes {
		if ns := st.Nodes[n.ID]; ns.Status == event.Running {
			err := &executor.InfraError{Err: errors.New("interrupted: the runtime restarted")}
			if err := r.fail(st, emit, n, ns.Attempt, err, 0); err != nil {
				return err
			}
		}
	}

	// Buffered so that workers never block, even after drive returns.
	results := make(chan outcome, r.workers)
	running := 0
	for st.Status == event.RunRunning {
		if replanned, err := replan(st, emit); err != nil {
			return emit(event.New(id, event.RunFinished, "", 0,
				event.RunFinishedData{Status: event.RunFailed, Reason: err.Error()}))
		} else if replanned {
			continue
		}
		stop := stopReason(st)
		decided := false // a gate was decided here, so more nodes may be ready
		if stop == "" {
			for _, n := range ready(st) {
				switch n.Kind {
				case graph.Gate:
					if err := emit(event.New(id, event.GateWaiting, n.ID, 0, nil)); err != nil {
						return err
					}
					if st.AutoApprove {
						d := event.GateDecidedData{Approved: true, Notes: autoApproved}
						if err := emit(event.New(id, event.GateDecided, n.ID, 0, d)); err != nil {
							return err
						}
						decided = true
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
				ns := st.Nodes[n.ID]
				task := executor.Task{Run: id, Node: n, Attempt: ns.Attempt + 1}
				if ns.Attempt > 0 {
					// An infra failure says nothing about the code, so it changes neither.
					task.Feedback = ns.LastTaskError
					task.Resume = ns.TaskFailures == 1
				}
				task.Revision = ns.Revised
				if strings.HasPrefix(n.ID, replanPrefix) {
					task.Replan = replanBrief(st)
				}
				// The first infra retry starts at once: a restart is not a rate limit.
				var wait time.Duration
				if ns.LastClass == event.Infra && ns.InfraFailures > 1 {
					wait = min(r.infraBackoff<<(ns.InfraFailures-2), maxBackoff)
				}
				attempt := task.Attempt
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
					var res executor.Result
					err := executor.Sleep(r.ctx, wait)
					if err == nil {
						res, err = exec.Run(r.ctx, task, log)
					}
					results <- outcome{node: n, attempt: attempt, res: res, err: err}
				})
			}
		}

		if decided {
			continue
		}
		if running == 0 {
			if status, reason, done := finished(st, stop); done {
				return emit(event.New(id, event.RunFinished, "", 0,
					event.RunFinishedData{Status: status, Reason: reason}))
			}
		}

		select {
		case o := <-results:
			running--
			if r.ctx.Err() != nil {
				// Leave the node marked running, so a restart retries it.
				// shortcut: the attempt's cost is lost, so restarts can let a run spend past
				// run_usd. Record it with the restart's infra failure if restarts become common.
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
		g, err := fillSlot(st, o.node.ID, o.res.Nodes)
		if err == nil {
			// This one event also marks the plan node succeeded.
			d := event.GraphUpdatedData{Graph: g, Version: st.GraphVersion + 1, Reason: fmt.Sprintf("%s emitted %d nodes", o.node.ID, len(o.res.Nodes)), CostUSD: o.res.CostUSD}
			return emit(event.New(st.Run, event.GraphUpdated, o.node.ID, o.attempt, d))
		}
		o.err = fmt.Errorf("plan output rejected: %w", err)
	}
	if o.err == nil {
		return emit(event.New(st.Run, event.NodeSucceeded, o.node.ID, o.attempt, event.NodeSucceededData{CostUSD: o.res.CostUSD}))
	}
	return r.fail(st, emit, o.node, o.attempt, o.err, o.res.CostUSD)
}

// fail records a failed attempt and decides whether the node will retry.
func (r *Runner) fail(st *event.State, emit func(event.Event) error, n graph.Node, attempt int, err error, cost float64) error {
	ns := st.Nodes[n.ID]
	d := event.NodeFailedData{Error: err.Error(), Class: event.Task, CostUSD: cost}
	if _, ok := errors.AsType[*executor.LimitError](err); ok {
		d.Class = event.Limit // WillRetry stays false: retrying cannot help, and must not buy usage
		return emit(event.New(st.Run, event.NodeFailed, n.ID, attempt, d))
	}
	if executor.IsInfra(err) {
		d.Class = event.Infra
		d.WillRetry = ns.InfraFailures+1 < maxInfraFailures
	} else {
		d.WillRetry = ns.TaskFailures+1 <= n.Retries
	}
	d.Abandoned = !d.WillRetry && n.Advisory
	return emit(event.New(st.Run, event.NodeFailed, n.ID, attempt, d))
}

func decide(st *event.State, emit func(event.Event) error, d decision) error {
	if ns, ok := st.Nodes[d.node]; !ok || ns.Status != event.Waiting {
		return fmt.Errorf("node %s is not a waiting gate", d.node)
	}
	data := event.GateDecidedData{Approved: d.approved, Notes: d.notes}
	// Notes are what the node needs to do better, so a rejection without them ends the run.
	data.Revise = !d.approved && strings.TrimSpace(d.notes) != "" && st.Revisions < st.Rails.Budget.Revisions && revisable(st, d.node)
	return emit(event.New(st.Run, event.GateDecided, d.node, 0, data))
}

func fillSlot(st *event.State, planID string, nodes []graph.Node) (graph.Graph, error) {
	for _, n := range nodes {
		if st.Retired[n.ID] {
			return graph.Graph{}, fmt.Errorf("node %q: this id belongs to a task that a replan replaced, so a new task needs a new id", n.ID)
		}
	}
	g := st.Graph
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

// stopReason says why the run must start no more nodes, or "" if it may.
func stopReason(st *event.State) string {
	if i := slices.IndexFunc(st.Graph.Nodes, func(n graph.Node) bool {
		return st.Nodes[n.ID].Status == event.Failed && !awaitsReplan(st, n)
	}); i >= 0 {
		id := st.Graph.Nodes[i].ID
		return fmt.Sprintf("node %s failed: %s", id, st.Nodes[id].LastError)
	}
	if budget := st.Rails.Budget.RunUSD; budget > 0 && st.CostUSD >= budget {
		return fmt.Sprintf("the run spent $%.2f of its $%.2f budget", st.CostUSD, budget)
	}
	return ""
}

// finished decides the run's outcome. Call it only when nothing is running.
func finished(st *event.State, stop string) (event.RunStatus, string, bool) {
	var stuck []string
	for _, n := range st.Graph.Nodes {
		switch st.Nodes[n.ID].Status {
		case event.Succeeded:
		case event.Waiting:
			if stop == "" {
				return "", "", false
			}
			stuck = append(stuck, n.ID)
		default:
			stuck = append(stuck, n.ID)
		}
	}
	switch {
	case len(stuck) == 0:
		// The last node can take the cost past the budget. Its work is merged, so the run succeeded.
		return event.RunSucceeded, "", true
	case stop != "":
		return event.RunFailed, stop, true
	}
	return event.RunFailed, "nodes can never start: " + strings.Join(stuck, ", "), true
}

// replanPrefix starts the id of each plan node that answers a failed task.
const replanPrefix = "replan-"

// awaitsReplan reports whether a failed node is a task that a replan may
// replace. Only the task's own failures count: a crash or a usage limit
// says nothing about the plan.
func awaitsReplan(st *event.State, n graph.Node) bool {
	ns := st.Nodes[n.ID]
	return n.IsTask() && ns.Status == event.Failed && ns.LastClass == event.Task && st.Replans < st.Rails.Budget.Replans
}

// replan answers a failed task by swapping it, and the tasks that depend on
// it, for a plan node and a slot. It does nothing while an earlier replan is
// still open, because the new planner would plan the same packages, or when
// the run has spent its budget. It reports whether it changed the graph.
func replan(st *event.State, emit func(event.Event) error) (bool, error) {
	if budget := st.Rails.Budget.RunUSD; budget > 0 && st.CostUSD >= budget {
		return false, nil
	}
	if slices.ContainsFunc(st.Graph.Nodes, func(n graph.Node) bool { return n.Kind == graph.Slot && strings.HasPrefix(n.FilledBy, replanPrefix) }) {
		return false, nil
	}
	i := slices.IndexFunc(st.Graph.Nodes, func(n graph.Node) bool { return awaitsReplan(st, n) })
	if i < 0 {
		return false, nil
	}
	failed := st.Graph.Nodes[i]
	id := fmt.Sprintf("%s%d", replanPrefix, st.Replans+1)
	planner := graph.Node{ID: id, Title: "Plan again after " + failed.ID + " failed", Kind: graph.Plan, Role: "planner", Retries: plan.Retries}
	g, removed, err := st.Graph.Replan(failed.ID, planner, id+"-tasks")
	if err != nil {
		return false, fmt.Errorf("replan after %s failed: %w", failed.ID, err)
	}
	var ids []string
	for _, n := range removed {
		ids = append(ids, n.ID)
	}
	d := event.GraphUpdatedData{
		Graph:    g,
		Version:  st.GraphVersion + 1,
		Replaced: ids,
		Reason:   fmt.Sprintf("task %s failed: %s", failed.ID, st.Nodes[failed.ID].LastError),
	}
	return true, emit(event.New(st.Run, event.GraphUpdated, "", 0, d))
}

// replanBrief gathers what the planner needs to plan the replaced tasks again.
func replanBrief(st *event.State) *executor.Replan {
	b := &executor.Replan{Reason: st.ReplanReason, Retired: slices.Sorted(maps.Keys(st.Retired))}
	for _, n := range st.Graph.Nodes {
		if n.IsTask() {
			b.Kept = append(b.Kept, executor.Kept{ID: n.ID, Scope: n.Scope})
		}
	}
	return b
}

// revisable reports whether a gate waits only for agent nodes, which a
// rejection can send back.
func revisable(st *event.State, gate string) bool {
	n, ok := st.Graph.Node(gate)
	return ok && len(n.Deps) > 0 && !slices.ContainsFunc(n.Deps, func(id string) bool {
		d, ok := st.Graph.Node(id)
		return !ok || d.Kind != graph.Agent
	})
}
