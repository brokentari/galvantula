package executor

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	"github.com/brokentari/galvantula/internal/claude"
	"github.com/brokentari/galvantula/internal/sandbox"
)

const oracleBrief = `Implement the program that spec.md describes, in this directory. The architect wrote stubs, acceptance tests and CONTRACT.md. This is a trial build that checks the tests are right. Nobody keeps your code. AGENTS.md, which is part of your instructions, holds the rules for the code.

- Fill in the stubs so that go test ./... passes.
- Do not change or delete a test (_test.go file), testdata, CONTRACT.md, spec.md, AGENTS.md, CLAUDE.md, go.mod or .factory/.
- If a test contradicts spec.md, or another test, do not change it and do not bend your code to it. Leave it failing. Write DISPUTES.md: for each such test, its name, what it expects, and what spec.md says. Dispute a test only when spec.md is clear. A test that is hard to pass is not wrong.
` + rules

// oracle checks the architect's tests against spec.md. The stubs check only
// proves that the tests fail on nothing. Here a second agent, who has not seen
// the architect's reasoning, implements the stubs in a throwaway copy of wt.
// If a test still fails, either the test or that agent is wrong, and the
// architect must decide which. It returns the cost of the agent.
func (c Claude) oracle(ctx context.Context, wt string, log func(string)) (cost float64, err error) {
	dir, err := os.MkdirTemp("", "oracle-")
	if err != nil {
		return 0, &InfraError{Err: err}
	}
	defer os.RemoveAll(dir)
	work, home := filepath.Join(dir, "oracle"), filepath.Join(dir, "home")
	if err := os.CopyFS(work, os.DirFS(wt)); err != nil {
		return 0, &InfraError{Err: err}
	}
	if err := errors.Join(os.RemoveAll(filepath.Join(work, ".git")), os.Mkdir(home, 0o755)); err != nil {
		return 0, &InfraError{Err: err}
	}
	bin, err := c.claudeBin()
	if err != nil {
		return 0, &InfraError{Err: err}
	}
	cache, err := c.newCache()
	if err != nil {
		return 0, &InfraError{Err: err}
	}
	defer os.RemoveAll(cache)
	box := c.box(cache, work, home, []string{c.Auth, "DISABLE_AUTOUPDATER=1"})
	box.RO = append(box.RO, bin)
	role := c.Rails.Models.Implementer
	bare := strings.HasPrefix(c.Auth, "ANTHROPIC_API_KEY=")
	var style string
	if c.Rails.ConciseOutput {
		style = ConciseStyle
	}
	limit := cmp.Or(c.timeout, time.Duration(c.Rails.Budget.NodeMinutes)*time.Minute)
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	log("oracle: a second agent implements the stubs, to check the tests")
	res, err := claude.Run(ctx, claude.Options{
		Style: style, Dir: work, Prompt: oracleBrief, Model: role.Model, Effort: role.Effort,
		SystemFile: filepath.Join(work, "AGENTS.md"), Tools: tools, Allow: allow, Mode: "acceptEdits",
		MaxBudgetUSD: c.Rails.Budget.NodeUSD, SessionID: uuid.New().String(), Bin: bin, Bare: &bare,
		Exec: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return sandbox.Command(ctx, box, name, args...)
		},
		Log: log,
	})
	cost = res.CostUSD
	switch {
	case errors.Is(err, claude.ErrUsageLimit):
		return cost, &LimitError{Err: err}
	case err != nil: // the oracle failed, not the architect: do not blame the tests
		return cost, &InfraError{Err: fmt.Errorf("the oracle agent: %w", err)}
	}

	check, cleanup, err := c.readOnlyBox(work)
	if err != nil {
		return cost, &InfraError{Err: err}
	}
	defer cleanup()
	if err := testsPass(ctx, check, minTests, []string{"./..."}, "-timeout=2m"); err != nil {
		disputes, _ := os.ReadFile(filepath.Join(work, "DISPUTES.md"))
		return cost, fmt.Errorf("a second agent implemented your stubs from spec.md and could not make your tests pass. "+
			"Either a test is wrong or that agent was. Compare each failing test with spec.md. "+
			"Fix every test that disagrees with spec.md. Keep a test that agrees with it, and say in CONTRACT.md why it is right.\n\n%w\n\nThe agent's disputes:\n%s",
			err, cmp.Or(strings.TrimSpace(string(disputes)), "(none)"))
	}
	log("oracle: all tests pass on an independent implementation")
	return cost, nil
}
