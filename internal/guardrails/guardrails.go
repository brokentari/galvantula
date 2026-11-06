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
	Model  string `yaml:"model"`
	Effort string `yaml:"effort"`
}

type Guardrails struct {
	Go struct {
		Version string `yaml:"version"`
	} `yaml:"go"`
	Models struct {
		Architect   Role `yaml:"architect"`
		Planner     Role `yaml:"planner"`
		Implementer Role `yaml:"implementer"`
		Reviewer    Role `yaml:"reviewer"`
		EvalAuthor  Role `yaml:"eval_author"`
	} `yaml:"models"`
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
