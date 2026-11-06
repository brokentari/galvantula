package guardrails

import (
	"strings"
	"testing"
)

func TestRepoGuardrailsAreValid(t *testing.T) {
	if _, err := Load("../../guardrails.yaml"); err != nil {
		t.Fatal(err)
	}
}

func TestEvalAuthorMustNotBuild(t *testing.T) {
	g, err := Load("../../guardrails.yaml")
	if err != nil {
		t.Fatal(err)
	}
	g.Models.EvalAuthor.Model = g.Models.Implementer.Model
	if err := g.Validate(); err == nil || !strings.Contains(err.Error(), "eval_author") {
		t.Fatalf("got %v, want an error about eval_author", err)
	}
}
