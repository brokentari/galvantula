// Package plan reads the planner's task graph and checks it against the
// contract. The planner is an agent, so its output is untrusted: nothing
// runs until the graph passes these rules.
package plan

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/brokentari/galvantula/internal/graph"
)

// Limits on a plan. Retries is how many times a task node may retry.
const (
	MaxTasks = 6
	MaxDepth = 4
	Retries  = 2
)

// Task is one node of the planner's output.
type Task struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Scope []string `json:"scope"`
	Deps  []string `json:"deps"`
}

// Output is what the planner's final answer must contain.
type Output struct {
	Tasks []Task `json:"tasks"`
}

// Schema is the shape the planner is asked for. The brief shows it.
const Schema = `{"tasks": [{"id": "store", "title": "what this task builds", "scope": ["internal/store"], "deps": []}]}`

// Parse reads the planner's answer. The answer may wrap the JSON in a code
// fence or in a sentence; Parse takes the first object it finds.
func Parse(answer string) (Output, error) {
	start, end := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if start < 0 || end < start {
		return Output{}, errors.New("the answer has no JSON object")
	}
	var out Output
	if err := json.Unmarshal([]byte(answer[start:end+1]), &out, json.RejectUnknownMembers(true)); err != nil {
		return Output{}, fmt.Errorf("the answer is not valid against the schema %s: %w", Schema, err)
	}
	return out, nil
}

// Validate checks the tasks against the contract's packages: the directories
// that hold Go files on main. It returns every rule that the plan breaks.
func Validate(out Output, packages []string) error {
	var errs []error
	n := len(out.Tasks)
	if n == 0 || n > MaxTasks {
		errs = append(errs, fmt.Errorf("a plan has 1 to %d tasks, this one has %d", MaxTasks, n))
	}
	owner := map[string]string{} // package dir -> task id
	for _, t := range out.Tasks {
		if len(t.Scope) == 0 {
			errs = append(errs, fmt.Errorf("task %q has no scope", t.ID))
		}
		for _, dir := range t.Scope {
			switch {
			case dir != path.Clean(dir) || path.IsAbs(dir) || strings.HasPrefix(dir, "..") || strings.HasPrefix(dir, ".factory"):
				errs = append(errs, fmt.Errorf("task %q: scope %q must be a clean relative path inside the product", t.ID, dir))
			case !slices.Contains(packages, dir):
				errs = append(errs, fmt.Errorf("task %q: scope %q is not a package of the contract (the packages are: %s)", t.ID, dir, strings.Join(packages, ", ")))
			case owner[dir] != "":
				errs = append(errs, fmt.Errorf("package %q is in the scope of both %q and %q; each package needs one owner", dir, owner[dir], t.ID))
			default:
				owner[dir] = t.ID
			}
		}
	}
	for _, p := range packages {
		if owner[p] == "" {
			errs = append(errs, fmt.Errorf("package %q is in no task's scope", p))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if d := depth(out.Tasks); d > MaxDepth {
		return fmt.Errorf("the longest chain of dependencies has %d tasks; at most %d are allowed, so that tasks run in parallel", d, MaxDepth)
	}
	return nil
}

// depth is the number of tasks in the longest chain of dependencies. A
// dependency on an unknown task or a cycle counts as 0 here: graph.Fill
// rejects both with a better message.
func depth(tasks []Task) int {
	byID := map[string]Task{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	memo := map[string]int{}
	var of func(id string, seen map[string]bool) int
	of = func(id string, seen map[string]bool) int {
		if d, ok := memo[id]; ok {
			return d
		}
		if seen[id] {
			return 0
		}
		seen[id] = true
		defer delete(seen, id)
		deepest := 0
		for _, dep := range byID[id].Deps {
			deepest = max(deepest, of(dep, seen))
		}
		memo[id] = deepest + 1
		return memo[id]
	}
	deepest := 0
	for _, t := range tasks {
		deepest = max(deepest, of(t.ID, map[string]bool{}))
	}
	return deepest
}

// Nodes turns the tasks into graph nodes: implementer agents with the
// scope and dependencies the planner chose.
func Nodes(out Output) []graph.Node {
	nodes := make([]graph.Node, len(out.Tasks))
	for i, t := range out.Tasks {
		nodes[i] = graph.Node{ID: t.ID, Title: t.Title, Kind: graph.Agent, Role: "implementer", Retries: Retries, Scope: t.Scope, Deps: t.Deps}
	}
	return nodes
}
