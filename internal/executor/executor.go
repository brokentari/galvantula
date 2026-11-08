// Package executor runs one node attempt. The runtime picks an executor per run.
package executor

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/brokentari/galvantula/internal/graph"
)

// Task is one attempt at one node.
type Task struct {
	Run     string
	Node    graph.Node
	Attempt int // starts at 1
	// Resume asks the executor to continue the last attempt's session
	// instead of starting again from a clean worktree.
	Resume bool
	// Feedback is why the last attempt failed, if there was one.
	Feedback string
	// Replan is set on a plan node that answers a failed task.
	Replan *Replan
	// Revision is set when a person rejected the node's last result at a
	// gate. Feedback holds their notes. The node starts again from the
	// commit before its work.
	Revision bool
}

// Kept is a task that a replan leaves in place.
type Kept struct {
	ID    string
	Scope []string
}

// Replan tells the planner what went wrong and what it may not change.
type Replan struct {
	// Reason is why the task failed.
	Reason string
	// Kept lists the tasks that stay. The planner plans the other packages
	// again, and a new task may depend on a kept one.
	Kept []Kept
	// Retired lists the ids of replaced tasks. A new task may not use one.
	Retired []string
}

// Result is what an attempt produced. CostUSD is set even when it failed.
type Result struct {
	// Nodes is the subgraph a plan node emitted. Other kinds leave it empty.
	Nodes   []graph.Node
	CostUSD float64
}

// Executor runs tasks. log sends one line to the run's event log.
type Executor interface {
	Run(ctx context.Context, t Task, log func(line string)) (Result, error)
}

// InfraError marks a failure that is not the node's fault, such as a crash
// or a rate limit. It does not count toward the node's retries.
type InfraError struct{ Err error }

func (e *InfraError) Error() string { return "infra: " + e.Err.Error() }
func (e *InfraError) Unwrap() error { return e.Err }

// LimitError marks a failure because the account reached its usage limit.
// The node does not retry, and the run stops.
type LimitError struct{ Err error }

func (e *LimitError) Error() string { return e.Err.Error() }
func (e *LimitError) Unwrap() error { return e.Err }

// IsInfra reports whether err is an infrastructure failure.
func IsInfra(err error) bool {
	_, ok := errors.AsType[*InfraError](err)
	return ok
}

// Script tells the Fake executor how each node behaves.
type Script struct {
	Default Step            `yaml:"default"`
	Nodes   map[string]Step `yaml:"nodes"`
}

// Step is the fake behavior of one node.
type Step struct {
	Duration     time.Duration `yaml:"duration"`
	Logs         []string      `yaml:"logs"`
	FailAttempts int           `yaml:"fail_attempts"` // the first N attempts fail
	Error        string        `yaml:"error"`
	Emit         []graph.Node  `yaml:"emit"`     // for plan nodes
	CostUSD      float64       `yaml:"cost_usd"` // what each attempt reports it spent
}

// ParseScript reads a fake script from YAML. Unknown fields are errors.
func ParseScript(data []byte) (Script, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var s Script
	if err := dec.Decode(&s); err != nil && !errors.Is(err, io.EOF) { // empty means all defaults
		return Script{}, fmt.Errorf("parse fake script: %w", err)
	}
	return s, nil
}

// Fake plays a script instead of doing work. Phase 0 uses it to drive the
// runtime and the UI without an LLM.
type Fake struct{ Script Script }

func (f Fake) Run(ctx context.Context, t Task, log func(string)) (Result, error) {
	step, ok := f.Script.Nodes[t.Node.ID]
	if !ok {
		step = f.Script.Default
	}
	lines := step.Logs
	if len(lines) == 0 {
		lines = []string{fmt.Sprintf("working on %s", t.Node.ID)}
	}
	pause := cmp.Or(step.Duration, f.Script.Default.Duration) / time.Duration(len(lines)+1)
	for _, line := range lines {
		if err := Sleep(ctx, pause); err != nil {
			return Result{}, err
		}
		log(line)
	}
	if err := Sleep(ctx, pause); err != nil {
		return Result{}, err
	}
	if t.Attempt <= step.FailAttempts {
		return Result{CostUSD: step.CostUSD}, errors.New(cmp.Or(step.Error, "fake failure"))
	}
	return Result{Nodes: step.Emit, CostUSD: step.CostUSD}, nil
}

// Sleep waits for d, or until ctx ends.
func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
