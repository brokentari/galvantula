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
	// Auth holds the credential the sandboxed agent uses:
	// CLAUDE_CODE_OAUTH_TOKEN=... or ANTHROPIC_API_KEY=...
	Auth string

	timeout time.Duration // replaces the node time limit in tests
}

const brief = `Build the program that spec.md describes, in this directory. AGENTS.md, which is part of your instructions, holds the rules for the code and its prose.

- Write the code and its tests. Use only the standard library.
- Do not change or delete a file that was here when you started: AGENTS.md, CLAUDE.md, spec.md, go.mod and .factory/. The check fails if you do.
- Before you finish, make all of these pass: gofmt -l . (prints nothing), go vet ./..., go fix -diff ./... (prints nothing), go build ./... and go test ./...
- Write NOTES.md: in a few lines, what you built and each assumption you made where the spec is unclear.
- Do not leave build output, such as compiled programs, in the directory.`

var tools = []string{"Read", "Write", "Edit", "Glob", "Grep", "Bash"}

var allow = []string{"Bash(go build *)", "Bash(go test *)", "Bash(go vet *)", "Bash(go fix *)",
	"Bash(go run *)", "Bash(go doc *)", "Bash(gofmt *)"}

func (c Claude) Run(ctx context.Context, t Task, log func(string)) (Result, error) {
	repo, err := workspace.Open(filepath.Join(c.Root, t.Run, "repo"))
	if err != nil {
		return Result{}, &InfraError{Err: err}
	}
	switch {
	case t.Node.Kind == graph.Tool && t.Node.ID == "standards":
		return Result{}, c.writeStandards(repo, log)
	case t.Node.Kind == graph.Agent:
		return c.agent(ctx, repo, t, log)
	}
	return Result{}, fmt.Errorf("the claude executor cannot run %s node %q yet", t.Node.Kind, t.Node.ID)
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

func (c Claude) agent(ctx context.Context, repo workspace.Repo, t Task, log func(string)) (Result, error) {
	run := filepath.Join(c.Root, t.Run)
	wt, err := repo.Worktree(t.Node.ID)
	if err != nil {
		return Result{}, &InfraError{Err: err}
	}
	home := filepath.Join(run, "home", t.Node.ID)
	sessions := filepath.Join(run, "sessions")
	for _, dir := range []string{home, sessions, c.goCache(t.Run), c.goModCache()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Result{}, &InfraError{Err: err}
		}
	}
	bin, err := claudeBin()
	if err != nil {
		return Result{}, &InfraError{Err: err}
	}

	role := c.Rails.Models.Implementer
	bare := strings.HasPrefix(c.Auth, "ANTHROPIC_API_KEY=")
	box := c.box(t.Run, wt, home, []string{c.Auth, "DISABLE_AUTOUPDATER=1"})
	box.RO = append(box.RO, bin)
	opts := claude.Options{
		Dir:          wt,
		Model:        role.Model,
		Effort:       role.Effort,
		SystemFile:   filepath.Join(wt, "AGENTS.md"),
		Tools:        tools,
		Allow:        allow,
		Mode:         "acceptEdits",
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
		opts.Prompt = "The factory's check failed:\n\n" + t.Feedback + "\n\nFix the code so the check passes."
		log("resume the last session")
	} else {
		if err := repo.Reset(wt); err != nil {
			return Result{}, &InfraError{Err: err}
		}
		opts.SessionID = uuid.New().String()
		if err := os.WriteFile(session, []byte(opts.SessionID), 0o644); err != nil {
			return Result{}, &InfraError{Err: err}
		}
		opts.Prompt = brief
		if t.Feedback != "" {
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

	// Remove build output first, so the check sees what main will get.
	if err := removeBinaries(repo, wt, log); err != nil {
		return spent, &InfraError{Err: err}
	}
	log("check")
	if err := c.check(ctx, repo, t.Run, wt); err != nil {
		return spent, fmt.Errorf("check failed:\n%w", err)
	}
	if _, err := repo.CommitAll(wt, fmt.Sprintf("%s: attempt %d", t.Node.ID, t.Attempt)); err != nil {
		return spent, &InfraError{Err: err}
	}
	if err := repo.Merge(t.Node.ID); err != nil {
		return spent, &InfraError{Err: err}
	}
	log("merged into main")
	return spent, nil
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
func (c Claude) check(ctx context.Context, repo workspace.Repo, run, wt string) error {
	var errs []error
	changed, err := repo.Changed(wt)
	if err != nil {
		return &InfraError{Err: err}
	}
	if len(changed) > 0 {
		errs = append(errs, fmt.Errorf("these files must not change: %s", strings.Join(changed, ", ")))
	}
	home, err := os.MkdirTemp("", "check-home")
	if err != nil {
		return &InfraError{Err: err}
	}
	defer os.RemoveAll(home)
	box := c.box(run, wt, home, nil)
	// The checks run the agent's code. It must not change what the runtime commits.
	box.RW = slices.DeleteFunc(box.RW, func(p string) bool { return p == wt })
	box.RO = append(box.RO, wt)
	for _, step := range [][]string{
		{"gofmt", "-l", "."},
		{"go", "vet", "./..."},
		{"go", "fix", "-diff", "./..."},                    // exits non-zero when it has a change to suggest
		{"go", "build", "-o", os.DevNull, "./..."},         // a single main package would otherwise leave its binary here
		{"go", "test", "-count=1", "-timeout=2m", "./..."}, // a hung test fails the check, not the worker
	} {
		out, err := sandbox.Command(ctx, box, step[0], step[1:]...).CombinedOutput()
		quiet := step[0] == "gofmt" // gofmt lists files to fix and still exits 0
		if err != nil || (quiet && len(out) > 0) {
			errs = append(errs, fmt.Errorf("%s:\n%s", strings.Join(step, " "), gotest.Tail(string(out), 40)))
		}
	}
	return errors.Join(errs...)
}

// box is the sandbox for one worktree, with the Go settings the factory needs.
func (c Claude) box(run, wt, home string, env []string) sandbox.Spec {
	return sandbox.Spec{
		Dir:  wt,
		RW:   []string{wt, home, c.goCache(run), c.goModCache()},
		Home: home,
		Env: slices.Concat(env, []string{
			"GOCACHE=" + c.goCache(run),
			"GOMODCACHE=" + c.goModCache(),
			"GOFLAGS=-mod=readonly -buildvcs=false",
			"GOPROXY=off",
			"GOTOOLCHAIN=local",
			"GOTELEMETRY=off",
		}),
	}
}

// The factory keeps its own Go caches, so agent code never writes to the
// user's. Each run gets its own build cache: agent code runs during go test
// and could write build results that another run's check would then reuse.
// Nothing writes to the module cache, since GOPROXY is off, so runs share it.
func (c Claude) goCache(run string) string { return filepath.Join(c.Root, run, ".cache", "go-build") }
func (c Claude) goModCache() string        { return filepath.Join(c.Root, ".cache", "go-mod") }

// claudeBin finds the real claude executable. Updates change its path, so
// look it up on each attempt.
func claudeBin() (string, error) {
	p, err := exec.LookPath("claude")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}
