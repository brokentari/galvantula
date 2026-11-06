// Package guardrails loads guardrails.yaml: the target Go version and the
// model and effort for each role.
package guardrails

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"

	"go.yaml.in/yaml/v3"
)

// Role is the model one kind of node runs on.
type Role struct {
	Model  string `yaml:"model" json:"model"`
	Effort string `yaml:"effort" json:"effort"`
}

type Guardrails struct {
	Go     GoTarget `yaml:"go" json:"go"`
	Budget Budget   `yaml:"budget" json:"budget"`
	Models Models   `yaml:"models" json:"models"`
}

// GoTarget is the Go version every product's go.mod names.
type GoTarget struct {
	Version string `yaml:"version" json:"version"`
}

// Budget limits spending and time.
type Budget struct {
	NodeUSD     float64 `yaml:"node_usd" json:"node_usd"`
	RunUSD      float64 `yaml:"run_usd" json:"run_usd"`
	NodeMinutes int     `yaml:"node_minutes" json:"node_minutes"`
}

// Models names the model for each role.
type Models struct {
	Architect   Role `yaml:"architect" json:"architect"`
	Planner     Role `yaml:"planner" json:"planner"`
	Implementer Role `yaml:"implementer" json:"implementer"`
	Reviewer    Role `yaml:"reviewer" json:"reviewer"`
	EvalAuthor  Role `yaml:"eval_author" json:"eval_author"`
}

var (
	efforts   = []string{"low", "medium", "high", "xhigh", "max"}
	goVersion = regexp.MustCompile(`^1\.[0-9]+$`)
)

// Load reads and validates a guardrails file. Unknown fields are errors.
func Load(path string) (Guardrails, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Guardrails{}, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var g Guardrails
	if err := dec.Decode(&g); err != nil {
		return Guardrails{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := g.Validate(); err != nil {
		return Guardrails{}, fmt.Errorf("%s: %w", path, err)
	}
	return g, nil
}

// Validate reports every rule the settings break.
func (g Guardrails) Validate() error {
	var errs []error
	if !goVersion.MatchString(g.Go.Version) {
		errs = append(errs, fmt.Errorf("go.version %q must look like 1.27", g.Go.Version))
	}
	if b := g.Budget; b.NodeUSD <= 0 || b.RunUSD < b.NodeUSD || b.NodeMinutes <= 0 {
		errs = append(errs, fmt.Errorf("budget: node_usd and node_minutes must be positive, and run_usd at least node_usd"))
	}
	m := g.Models
	roles := []struct {
		name string
		Role
	}{{"architect", m.Architect}, {"planner", m.Planner}, {"implementer", m.Implementer}, {"reviewer", m.Reviewer}, {"eval_author", m.EvalAuthor}}
	for _, r := range roles {
		if r.Model == "" {
			errs = append(errs, fmt.Errorf("models.%s.model is missing", r.name))
		}
		if !slices.Contains(efforts, r.Effort) {
			errs = append(errs, fmt.Errorf("models.%s.effort %q must be one of %v", r.name, r.Effort, efforts))
		}
		if r.name != "eval_author" && r.Model == m.EvalAuthor.Model {
			errs = append(errs, fmt.Errorf("models.eval_author uses %s, which the %s also uses; hidden tests must come from a model the factory does not build with", r.Model, r.name))
		}
	}
	return errors.Join(errs...)
}
