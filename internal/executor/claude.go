package executor

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/brokentari/galvantula/internal/claude"
	"github.com/brokentari/galvantula/internal/gotest"
	"github.com/brokentari/galvantula/internal/graph"
	"github.com/brokentari/galvantula/internal/guardrails"
	"github.com/brokentari/galvantula/internal/sandbox"
	"github.com/brokentari/galvantula/internal/workspace"
	"github.com/brokentari/galvantula/standards"
)

// Claude runs agent nodes with Claude Code inside a sandbox, and tool nodes
// with Go code. Each run gets a folder under Root:
//
//	repo/            the product; main holds merged work
//	nodes/<id>/      a worktree per agent node
//	home/<id>/       the agent's HOME inside the sandbox
//	sessions/<id>    the agent's last session id, for resuming
type Claude struct {
	Root  string
	Rails guardrails.Guardrails
	Spec  string
	// PlantedPlan, if not empty, is the answer the first planner gives. An eval
	// plants a flawed plan this way, to see whether the factory recovers.
	PlantedPlan string
	// SkipOracle turns off the trial build of the architect's tests. Tests that
	// script a fake agent set it, so that they need not script the oracle too.
	SkipOracle bool
	// Auth holds the credential the sandboxed agent uses:
	// CLAUDE_CODE_OAUTH_TOKEN=... or ANTHROPIC_API_KEY=...
	Auth string
	// Proxy and SumDB say where the runtime and the architect fetch a module
	// that deps.txt lists. The defaults are the public Go proxy and checksum
	// database. Tests point them at files.
	Proxy, SumDB string

	timeout time.Duration // replaces the node time limit in tests
}

var tools = []string{"Read", "Write", "Edit", "Glob", "Grep", "Bash"}

var allow = []string{"Bash(go build *)", "Bash(go test *)", "Bash(go vet *)", "Bash(go fix *)",
	"Bash(go run *)", "Bash(go doc *)", "Bash(gofmt *)"}

func (c Claude) Run(ctx context.Context, t Task, log func(string)) (Result, error) {
	repo, err := workspace.Open(filepath.Join(c.Root, t.Run, "repo"))
	if err != nil {
		return Result{}, &InfraError{Err: err}
	}
	switch role := cmp.Or(t.Node.Role, t.Node.ID); {
	case t.Node.Kind == graph.Tool && role == "standards":
		return Result{}, c.writeStandards(repo, log)
	case t.Node.Kind == graph.Tool && role == "verify":
		return Result{}, c.verify(ctx, repo, log)
	case t.Node.Kind == graph.Agent || t.Node.Kind == graph.Plan:
		return c.agent(ctx, repo, t, log)
	}
	return Result{}, fmt.Errorf("the claude executor cannot run %s node %q", t.Node.Kind, t.Node.ID)
}

// writeStandards puts the rules, the spec and go.mod on main. Every agent
// starts from them, and the check keeps them unchanged.
func (c Claude) writeStandards(repo workspace.Repo, log func(string)) error {
	block, err := standards.Render(c.Rails.Go.Version)
	if err != nil {
		return err
	}
	lock, err := standards.Lock()
	if err != nil {
		return err
	}
	review, err := standards.Review()
	if err != nil {
		return err
	}
	files := map[string]string{
		"AGENTS.md":                    block,
		"CLAUDE.md":                    "@AGENTS.md\n",
		"spec.md":                      c.Spec,
		"go.mod":                       fmt.Sprintf("module product\n\ngo %s\n", c.Rails.Go.Version),
		".factory/standards/lock.json": string(lock),
		".factory/standards/review.md": review,
	}
	for path, data := range files {
		full := filepath.Join(repo.Dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			return err
		}
		log("write " + path)
	}
	_, err = repo.CommitAll(repo.Dir, "standards")
	return err
}

// roleName returns a node's role. An agent with none builds the whole spec,
// and a plan node plans.
func roleName(n graph.Node) string {
	if n.Kind == graph.Plan {
		return cmp.Or(n.Role, "planner")
	}
	return cmp.Or(n.Role, "builder")
}

func (c Claude) agent(ctx context.Context, repo workspace.Repo, t Task, log func(string)) (Result, error) {
	if t.Node.Kind == graph.Plan && t.Replan == nil && c.PlantedPlan != "" {
		log("using the planted plan, not the planner's")
		nodes, err := planReport(c, repo, t, c.PlantedPlan, log)
		return Result{Nodes: nodes}, err
	}
	p, ok := plans[roleName(t.Node)]
	if !ok {
		return Result{}, fmt.Errorf("node %q has no role the executor knows", t.Node.ID)
	}
	if p.skip != nil {
		if why := p.skip(repo); why != "" {
			log("skipped: " + why)
			return Result{}, nil
		}
	}
	run := filepath.Join(c.Root, t.Run)
	var rejected string // the result a person sent back, shown to the agent because main no longer has it
	if t.Revision {
		data, _ := os.ReadFile(filepath.Join(repo.Dir, "CONTRACT.md"))
		rejected = string(data)
		if err := repo.Rewind("standards"); err != nil {
			return Result{}, &InfraError{Err: err}
		}
		log("a person sent the result back: main is at the standards commit again")
	}
	wt, err := repo.Worktree(t.Node.ID)
	if err != nil {
		return Result{}, &InfraError{Err: err}
	}
	home := filepath.Join(run, "home", t.Node.ID)
	sessions := filepath.Join(run, "sessions")
	for _, dir := range []string{home, sessions} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Result{}, &InfraError{Err: err}
		}
	}
	bin, err := claudeBin()
	if err != nil {
		return Result{}, &InfraError{Err: err}
	}

	role := p.model(c.Rails.Models)
	bare := strings.HasPrefix(c.Auth, "ANTHROPIC_API_KEY=")
	cache, err := c.newCache()
	if err != nil {
		return Result{}, &InfraError{Err: err}
	}
	defer os.RemoveAll(cache)
	box := c.box(cache, wt, home, []string{c.Auth, "DISABLE_AUTOUPDATER=1"})
	box.RO = append(box.RO, bin)
	if p.red {
		// Only the architect may fetch a module. It needs one to compile its tests.
		// The module cache is inside its own build cache, which is deleted after.
		box.Env = append(box.Env, "GOMODCACHE="+filepath.Join(cache, "mod"), "GOFLAGS=-mod=mod -buildvcs=false",
			"GOPROXY="+cmp.Or(c.Proxy, "https://proxy.golang.org"), "GOSUMDB="+cmp.Or(c.SumDB, "sum.golang.org"))
	}
	system := filepath.Join(wt, "AGENTS.md")
	if p.readOnly {
		if system, err = c.readOnlyAgent(&box, wt, home, p.system); err != nil {
			return Result{}, err
		}
	}
	var style string
	if c.Rails.ConciseOutput {
		style = ConciseStyle
	}
	opts := claude.Options{
		Style:        style,
		Dir:          wt,
		Model:        role.Model,
		Effort:       role.Effort,
		SystemFile:   system,
		Tools:        p.tools,
		Allow:        p.allow,
		Mode:         p.mode,
		MaxBudgetUSD: c.Rails.Budget.NodeUSD,
		Bin:          bin,
		Bare:         &bare,
		Exec: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return sandbox.Command(ctx, box, name, args...)
		},
		Log: log,
	}

	// The first retry continues the session, which already knows the code.
	// Later attempts start over, to get away from an approach that failed.
	session := filepath.Join(sessions, t.Node.ID)
	if id, err := os.ReadFile(session); t.Resume && err == nil {
		opts.Resume = string(id)
		opts.Prompt = "The factory found a problem with your last result:\n\n" + t.Feedback + "\n\nFix it."
		log("resume the last session")
	} else {
		if err := repo.Reset(wt); err != nil {
			return Result{}, &InfraError{Err: err}
		}
		opts.SessionID = uuid.New().String()
		if err := os.WriteFile(session, []byte(opts.SessionID), 0o644); err != nil {
			return Result{}, &InfraError{Err: err}
		}
		if opts.Prompt, err = p.brief(repo, t); err != nil {
			return Result{}, &InfraError{Err: err}
		}
		switch {
		case t.Revision:
			opts.Prompt += "\n\n" + t.Feedback + "\n\nThe CONTRACT.md they rejected was:\n\n" + rejected +
				"\n\nThe rejected files are not on disk. Write a new contract that answers their notes."
		case t.Feedback != "":
			opts.Prompt += "\n\nAn earlier attempt failed. The factory reported:\n\n" + t.Feedback
		}
	}

	limit := cmp.Or(c.timeout, time.Duration(c.Rails.Budget.NodeMinutes)*time.Minute)
	attemptCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	res, err := claude.Run(attemptCtx, opts)
	spent := Result{CostUSD: res.CostUSD}
	switch {
	case ctx.Err() != nil:
		return spent, err // the runner is stopping
	case errors.Is(err, context.DeadlineExceeded):
		// shortcut: a killed agent leaves no result line with its cost, so charge
		// the most one attempt may spend. A crash (ErrNoResult) charges nothing,
		// because claude may not have started. Add up the stream's token usage if
		// either guess proves wrong.
		spent.CostUSD = c.Rails.Budget.NodeUSD
		return spent, fmt.Errorf("the agent ran past %s", limit)
	case errors.Is(err, claude.ErrUsageLimit):
		return spent, &LimitError{Err: err}
	case errors.Is(err, claude.ErrNoResult):
		return spent, &InfraError{Err: err}
	case err != nil:
		return spent, err
	}

	if p.readOnly {
		nodes, err := p.report(c, repo, t, res.Text, log)
		spent.Nodes = nodes
		return spent, err
	}

	// Remove build output first, so the check sees what main will get.
	if err := removeBinaries(repo, wt, log); err != nil {
		return spent, &InfraError{Err: err}
	}
	if p.red {
		if err := c.settleDeps(ctx, wt, log); err != nil {
			return spent, fmt.Errorf("check failed:\n%w", err)
		}
	}
	log("check")
	if err := c.check(ctx, repo, wt, t.Node, p, log); err != nil {
		return spent, fmt.Errorf("check failed:\n%w", err)
	}
	if p.red && !c.SkipOracle {
		cost, err := c.oracle(ctx, wt, log)
		spent.CostUSD += cost
		if err != nil {
			return spent, fmt.Errorf("check failed:\n%w", err)
		}
	}
	if _, err := repo.CommitAll(wt, fmt.Sprintf("%s: attempt %d", t.Node.ID, t.Attempt)); err != nil {
		return spent, &InfraError{Err: err}
	}
	if err := repo.Merge(t.Node.ID); err != nil {
		return spent, &InfraError{Err: err}
	}
	log("merged into main")
	if p.after != nil {
		p.after(repo, log)
	}
	return spent, nil
}

// readOnlyAgent makes the worktree read-only in the box, so that a reviewer
// cannot change the code, and writes the reviewer's instructions. It returns
// the path of that file.
func (c Claude) readOnlyAgent(box *sandbox.Spec, wt, home string, instructions func(string) (string, error)) (string, error) {
	box.RW = slices.DeleteFunc(box.RW, func(d string) bool { return d == wt })
	box.RO = append(box.RO, wt)
	text, err := instructions(c.Rails.Go.Version)
	if err != nil {
		return "", err
	}
	system := filepath.Join(home, "system.md")
	if err := os.WriteFile(system, []byte(text), 0o644); err != nil {
		return "", &InfraError{Err: err}
	}
	return system, nil
}

// saveReview puts the reviewer's report on main, where the fix node reads it.
func saveReview(repo workspace.Repo, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("the reviewer ended without a report")
	}
	path := filepath.Join(repo.Dir, reviewFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return &InfraError{Err: err}
	}
	if err := os.WriteFile(path, []byte(text+"\n"), 0o644); err != nil {
		return &InfraError{Err: err}
	}
	if _, err := repo.CommitAll(repo.Dir, "review"); err != nil {
		return &InfraError{Err: err}
	}
	return nil
}

// removeBinaries deletes new executable files that hold a NUL byte, such as a
// program the agent compiled, so they never reach the product's history. It
// keeps data files, such as a fixture in testdata, and skips links and devices.
func removeBinaries(repo workspace.Repo, wt string, log func(string)) error {
	files, err := repo.NewFiles(wt)
	if err != nil {
		return err
	}
	for _, f := range files {
		path := filepath.Join(wt, f)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		bin, err := hasNUL(path)
		if err != nil {
			return err
		}
		if bin {
			if err := os.Remove(path); err != nil {
				return err
			}
			log("removed build output: " + f)
		}
	}
	return nil
}

// hasNUL reports whether the first 8000 bytes of a file hold a NUL byte, as git
// does to tell binary files from text.
func hasNUL(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	head := make([]byte, 8000)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	return bytes.IndexByte(head[:n], 0) >= 0, nil
}

// check runs the factory's checks on a worktree. Build and test run the
// agent's code, so they run in the sandbox too, without any credential.
func (c Claude) check(ctx context.Context, repo workspace.Repo, wt string, node graph.Node, p rolePlan, log func(string)) error {
	var errs []error
	changed, err := repo.Changed(wt)
	if err != nil {
		return &InfraError{Err: err}
	}
	if len(node.Scope) > 0 {
		// The node may write only in its packages. Other nodes write the rest at
		// the same time, and a file in both would conflict at the merge.
		fresh, err := repo.NewFiles(wt)
		if err != nil {
			return &InfraError{Err: err}
		}
		if out := outsideScope(node, slices.Concat(changed, fresh)); len(out) > 0 {
			errs = append(errs, fmt.Errorf("these files are outside your scope (%s): %s", strings.Join(node.Scope, ", "), strings.Join(out, ", ")))
		}
	}
	if p.frozen != nil {
		changed = slices.DeleteFunc(changed, func(f string) bool { return !p.frozen(f) })
	}
	if len(changed) > 0 {
		errs = append(errs, fmt.Errorf("these files must not change: %s", strings.Join(changed, ", ")))
	}
	for _, f := range p.needs {
		if info, err := os.Stat(filepath.Join(wt, f)); err != nil || info.Size() == 0 {
			errs = append(errs, fmt.Errorf("%s must exist and not be empty", f))
		}
	}
	if err := noLinks(wt); err != nil {
		return err // nothing else reads files that a link could redirect
	}
	if err := noSkips(wt); err != nil {
		errs = append(errs, err)
	}
	// Not the agent's cache or home: Go caches vet and fix results too, and
	// the agent must not be able to plant a clean result for its own code.
	box, cleanup, err := c.readOnlyBox(wt)
	if err != nil {
		return &InfraError{Err: err}
	}
	defer cleanup()
	errs = append(errs, runSteps(ctx, box, baseSteps)...)
	if p.red {
		if err := c.red(ctx, box, log); err != nil {
			errs = append(errs, err)
		}
	} else {
		pkgs, min := testTargets(wt, node, p.minTests)
		if err := testsPass(ctx, box, min, pkgs, "-timeout=2m"); err != nil { // a hung test fails the check, not the worker
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// testsPass runs the tests and requires that at least min of them ran and
// that none failed or skipped. Counting the tests matters: a test binary
// that exits early with status 0, as an init function can make it do, would
// otherwise pass with no test run.
func testsPass(ctx context.Context, box sandbox.Spec, min int, pkgs []string, flags ...string) error {
	args := append([]string{"test", "-count=1", "-json"}, flags...)
	out, err := sandbox.Command(ctx, box, "go", append(args, pkgs...)...).CombinedOutput()
	passed, failed, text := gotest.Count(string(out))
	switch {
	case failed > 0:
		return fmt.Errorf("go test %s: %d of %d tests failed or skipped:\n%s", strings.Join(flags, " "), failed, passed+failed, gotest.Tail(text, 40))
	case err != nil:
		// A package can fail with no test failing: a TestMain that returns 1, or a panic in init.
		return fmt.Errorf("go test %s exited with an error:\n%s", strings.Join(flags, " "), gotest.Tail(text, 40))
	case passed < min:
		return fmt.Errorf("go test %s: %d tests ran; at least %d must. A test binary that exits early runs none:\n%s", strings.Join(flags, " "), passed, min, gotest.Tail(text, 20))
	}
	return nil
}

// baseSteps are the checks that the check and verify share, before the tests.
var baseSteps = [][]string{
	{"gofmt", "-l", "."},
	{"go", "vet", "./..."},
	{"go", "fix", "-diff", "./..."},            // exits non-zero when it has a change to suggest
	{"go", "build", "-o", os.DevNull, "./..."}, // a single main package would otherwise leave its binary here
}

// runSteps runs each command in the sandbox and returns one error for each that fails.
func runSteps(ctx context.Context, box sandbox.Spec, steps [][]string) []error {
	var errs []error
	for _, step := range steps {
		out, err := sandbox.Command(ctx, box, step[0], step[1:]...).CombinedOutput()
		quiet := step[0] == "gofmt" // gofmt lists files to fix and still exits 0
		if err != nil || (quiet && len(out) > 0) {
			errs = append(errs, fmt.Errorf("%s:\n%s", strings.Join(step, " "), gotest.Tail(string(out), 40)))
		}
	}
	return errs
}

// red checks that the architect's tests fail on its stubs. At least minRed
// of them must fail: a test that passes on a stub tests
// nothing, and a panic in a stub would hide the tests after it.
func (c Claude) red(ctx context.Context, box sandbox.Spec, log func(string)) error {
	out, _ := sandbox.Command(ctx, box, "go", "test", "-count=1", "-timeout=2m", "-json", "./...").CombinedOutput()
	passed, failed, text := gotest.Count(string(out))
	total := passed + failed
	log(fmt.Sprintf("%d of %d tests fail on the stubs", failed, total))
	switch {
	case total < minTests:
		return fmt.Errorf("only %d tests ran on your stubs; at least %d must run. They may not compile, or a stub may panic, which stops the run:\n%s", total, minTests, gotest.Tail(text, 30))
	case float64(failed) < minRed*float64(total):
		return fmt.Errorf("only %d of %d tests fail on your stubs; at least %.0f%% must. A test that passes on a stub tests nothing, unless doing nothing is correct", failed, total, minRed*100)
	}
	return nil
}

// verify is the last step: the whole suite with the race detector, on what
// is on main, and a check that go.mod names only the modules in deps.txt.
func (c Claude) verify(ctx context.Context, repo workspace.Repo, log func(string)) error {
	box, cleanup, err := c.readOnlyBox(repo.Dir)
	if err != nil {
		return &InfraError{Err: err}
	}
	defer cleanup()

	var errs []error
	mod, err := os.ReadFile(filepath.Join(repo.Dir, "go.mod"))
	switch {
	case err != nil:
		errs = append(errs, err)
	case !strings.Contains(string(mod), "go "+c.Rails.Go.Version+"\n"):
		errs = append(errs, fmt.Errorf("go.mod must say go %s", c.Rails.Go.Version))
	}
	if err := checkDeps(ctx, repo.Dir); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, runSteps(ctx, box, baseSteps)...)
	if err := testsPass(ctx, box, minTests, []string{"./..."}, "-race", "-timeout=5m"); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("verify failed:\n%w", err)
	}
	log("verify passed: the suite passes with -race")
	return nil
}

// readOnlyBox is the sandbox for the checks, which run the agent's code. The
// directory is read-only, so that code cannot change what the runtime commits.
// Each call gets a new home and a new build cache. cleanup deletes them.
func (c Claude) readOnlyBox(dir string) (box sandbox.Spec, cleanup func(), err error) {
	home, err := os.MkdirTemp("", "check-home")
	if err != nil {
		return box, nil, err
	}
	cache, err := c.newCache()
	if err != nil {
		os.RemoveAll(home)
		return box, nil, err
	}
	box = c.box(cache, dir, home, nil)
	box.RW = slices.DeleteFunc(box.RW, func(p string) bool { return p == dir })
	box.RO = append(box.RO, dir)
	return box, func() { os.RemoveAll(home); os.RemoveAll(cache) }, nil
}

// box is the sandbox for one worktree, with the Go settings the factory needs.
func (c Claude) box(cache, wt, home string, env []string) sandbox.Spec {
	return sandbox.Spec{
		Dir:  wt,
		RW:   []string{wt, home, cache},
		Home: home,
		Env: slices.Concat(env, []string{
			"GOCACHE=" + cache,
			"GOFLAGS=-buildvcs=false",
			"GOPROXY=off",
			"GOTOOLCHAIN=local",
			"GOTELEMETRY=off",
		}),
	}
}

// BuildCaches is where agents and checks get their Go build caches, one new
// cache each, deleted after use. Agent code runs during go test and could
// otherwise write build results that a later check reuses. factory serve
// empties it at start, in case a hard kill left caches behind.
func BuildCaches(root string) string { return filepath.Join(root, ".cache", "build") }

func (c Claude) newCache() (string, error) {
	if err := os.MkdirAll(BuildCaches(c.Root), 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(BuildCaches(c.Root), "go-build-")
}

// claudeBin finds the real claude executable. Updates change its path, so
// look it up on each attempt.
func claudeBin() (string, error) {
	p, err := exec.LookPath("claude")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}
