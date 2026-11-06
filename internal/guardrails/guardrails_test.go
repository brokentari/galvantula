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

func TestRunBudgetCoversOneNode(t *testing.T) {
	g, err := Load("../../guardrails.yaml")
	if err != nil {
		t.Fatal(err)
	}
	g.Budget.RunUSD = g.Budget.NodeUSD / 2
	if err := g.Validate(); err == nil || !strings.Contains(err.Error(), "run_usd") {
		t.Fatalf("got %v, want an error about run_usd", err)
	}
	g.Budget.NodeUSD, g.Budget.RunUSD = 0.004, 1 // claude would see 0.00
	if err := g.Validate(); err == nil || !strings.Contains(err.Error(), "node_usd") {
		t.Fatalf("got %v, want an error about node_usd", err)
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
