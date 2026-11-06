// Package graph defines the work graph: its nodes, their kinds, and the
// rules a graph must follow before the runtime will run it.
package graph

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Kind says which executor runs a node.
type Kind string

const (
	Agent Kind = "agent" // Claude Code in a worktree
	Tool  Kind = "tool"  // real tooling, no LLM
	Gate  Kind = "gate"  // waits for a human decision
	Merge Kind = "merge" // runtime git
	Plan  Kind = "plan"  // agent that emits nodes into a slot
	Slot  Kind = "slot"  // placeholder that a plan node fills
)

// Kinds lists every valid Kind.
var Kinds = []Kind{Agent, Tool, Gate, Merge, Plan, Slot}

// Graph is a named set of nodes. Deps make it a DAG.
type Graph struct {
	Name  string `yaml:"name" json:"name"`
	Nodes []Node `yaml:"nodes" json:"nodes"`
}

// Node is one unit of work.
type Node struct {
	ID       string   `yaml:"id" json:"id"`
	Title    string   `yaml:"title" json:"title,omitempty"`
	Kind     Kind     `yaml:"kind" json:"kind"`
	Deps     []string `yaml:"deps" json:"deps,omitempty"`
	Retries  int      `yaml:"retries" json:"retries,omitempty"`
	FilledBy string   `yaml:"filled_by" json:"filled_by,omitempty"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Parse reads a graph from YAML and validates it. Unknown fields are errors.
func Parse(data []byte) (Graph, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var g Graph
	if err := dec.Decode(&g); err != nil {
		return Graph{}, fmt.Errorf("parse graph: %w", err)
	}
	return g, g.Validate()
}

// Node returns the node with the given id.
func (g Graph) Node(id string) (Node, bool) {
	i := slices.IndexFunc(g.Nodes, func(n Node) bool { return n.ID == id })
	if i < 0 {
		return Node{}, false
	}
	return g.Nodes[i], true
}

// Validate reports every rule the graph breaks.
func (g Graph) Validate() error {
	var errs []error
	if g.Name == "" {
		errs = append(errs, errors.New("graph has no name"))
	}
	if len(g.Nodes) == 0 {
		errs = append(errs, errors.New("graph has no nodes"))
	}

	byID := make(map[string]Node, len(g.Nodes))
	for _, n := range g.Nodes {
		if !idPattern.MatchString(n.ID) {
			errs = append(errs, fmt.Errorf("node %q: id must match %s", n.ID, idPattern))
		}
		if _, dup := byID[n.ID]; dup {
			errs = append(errs, fmt.Errorf("node %q: duplicate id", n.ID))
		}
		byID[n.ID] = n
		if !slices.Contains(Kinds, n.Kind) {
			errs = append(errs, fmt.Errorf("node %q: unknown kind %q", n.ID, n.Kind))
		}
		if n.Retries < 0 {
			errs = append(errs, fmt.Errorf("node %q: retries must not be negative", n.ID))
		}
		if (n.Kind == Slot) != (n.FilledBy != "") {
			errs = append(errs, fmt.Errorf("node %q: filled_by is required on slot nodes and allowed only there", n.ID))
		}
	}
	for _, n := range g.Nodes {
		seen := make(map[string]bool, len(n.Deps))
		for _, d := range n.Deps {
			switch _, ok := byID[d]; {
			case d == n.ID:
				errs = append(errs, fmt.Errorf("node %q: depends on itself", n.ID))
			case !ok:
				errs = append(errs, fmt.Errorf("node %q: unknown dependency %q", n.ID, d))
			case seen[d]:
				errs = append(errs, fmt.Errorf("node %q: dependency %q listed twice", n.ID, d))
			}
			seen[d] = true
		}
	}
	if len(errs) > 0 {
		// The checks below assume every dependency exists.
		return errors.Join(errs...)
	}

	if err := g.checkAcyclic(); err != nil {
		return err
	}

	filled := make(map[string]string) // plan id -> slot id
	for _, n := range g.Nodes {
		if n.Kind != Slot {
			continue
		}
		p, ok := byID[n.FilledBy]
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("slot %q: filled_by names unknown node %q", n.ID, n.FilledBy))
			continue
		case p.Kind != Plan:
			errs = append(errs, fmt.Errorf("slot %q: filled_by must name a plan node, %q is %s", n.ID, p.ID, p.Kind))
		case !g.dependsOn(n.ID, p.ID, byID):
			errs = append(errs, fmt.Errorf("slot %q: must come after its plan node %q", n.ID, p.ID))
		}
		if other, dup := filled[p.ID]; dup {
			errs = append(errs, fmt.Errorf("plan %q fills two slots: %q and %q", p.ID, other, n.ID))
		}
		filled[p.ID] = n.ID
	}
	return errors.Join(errs...)
}

// checkAcyclic removes nodes with no remaining deps until none are left.
// Whatever cannot be removed is on a cycle.
func (g Graph) checkAcyclic() error {
	waiting := make(map[string]int, len(g.Nodes))
	users := make(map[string][]string)
	var free []string
	for _, n := range g.Nodes {
		waiting[n.ID] = len(n.Deps)
		for _, d := range n.Deps {
			users[d] = append(users[d], n.ID)
		}
		if len(n.Deps) == 0 {
			free = append(free, n.ID)
		}
	}
	for len(free) > 0 {
		id := free[len(free)-1]
		free = free[:len(free)-1]
		delete(waiting, id)
		for _, u := range users[id] {
			waiting[u]--
			if waiting[u] == 0 {
				free = append(free, u)
			}
		}
	}
	if len(waiting) == 0 {
		return nil
	}
	stuck := slices.Sorted(maps.Keys(waiting))
	return fmt.Errorf("graph has a cycle through: %s", strings.Join(stuck, ", "))
}

// dependsOn reports whether node id reaches ancestor by following deps.
func (g Graph) dependsOn(id, ancestor string, byID map[string]Node) bool {
	seen := map[string]bool{}
	stack := slices.Clone(byID[id].Deps)
	for len(stack) > 0 {
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if d == ancestor {
			return true
		}
		if !seen[d] {
			seen[d] = true
			stack = append(stack, byID[d].Deps...)
		}
	}
	return false
}

// Fill replaces a slot with the nodes a plan emitted. Emitted nodes with no
// deps take the slot's deps. Nodes that depended on the slot now depend on
// the emitted nodes that nothing else in the emission depends on.
func (g Graph) Fill(slotID string, emitted []Node) (Graph, error) {
	slot, ok := g.Node(slotID)
	if !ok || slot.Kind != Slot {
		return Graph{}, fmt.Errorf("no slot %q", slotID)
	}
	if len(emitted) == 0 {
		return Graph{}, fmt.Errorf("slot %q: the plan emitted no nodes", slotID)
	}

	ids := make(map[string]bool, len(emitted))
	for _, e := range emitted {
		ids[e.ID] = true
	}
	used := map[string]bool{}
	for _, e := range emitted {
		if e.Kind == Plan || e.Kind == Slot {
			return Graph{}, fmt.Errorf("node %q: a plan may not emit %s nodes", e.ID, e.Kind)
		}
		for _, d := range e.Deps {
			if !ids[d] {
				return Graph{}, fmt.Errorf("node %q: emitted nodes may depend only on each other, not on %q", e.ID, d)
			}
			used[d] = true
		}
	}
	var leaves []string
	for _, e := range emitted {
		if !used[e.ID] {
			leaves = append(leaves, e.ID)
		}
	}

	out := Graph{Name: g.Name}
	for _, n := range g.Nodes {
		if n.ID == slotID {
			continue
		}
		if i := slices.Index(n.Deps, slotID); i >= 0 {
			n.Deps = slices.Concat(n.Deps[:i], leaves, n.Deps[i+1:])
		}
		out.Nodes = append(out.Nodes, n)
	}
	for _, e := range emitted {
		if len(e.Deps) == 0 {
			e.Deps = slices.Clone(slot.Deps)
		}
		out.Nodes = append(out.Nodes, e)
	}
	return out, out.Validate()
}
