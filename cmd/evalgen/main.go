// Command evalgen writes each eval's hidden tests and reference answer from
// its spec.md, using the eval_author model in guardrails.yaml. That model
// never builds products, so the tests do not repeat the builders' mistakes.
// evalgen then checks the work itself: the tests must pass on the reference,
// fail on a program that does nothing, and pass vet, gofmt and go fix.
//
//	go run ./cmd/evalgen [-guardrails guardrails.yaml] [-retries 2] evals/wordfreq ...
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/brokentari/galvantula/internal/claude"
	"github.com/brokentari/galvantula/internal/guardrails"
	"github.com/brokentari/galvantula/standards"
)

const (
	workModule = "evalwork"
	repoModule = "github.com/brokentari/galvantula/evals"
	// minStubFailures is the share of tests that must fail on a program that
	// does nothing. Tests of "prints nothing" cases may pass on it.
	minStubFailures = 0.8
	minTests        = 5
)

const role = `You write hidden acceptance tests for a software factory's eval set. Another system will build a Go program from spec.md alone. Your tests decide whether it built the right thing. They are the ground truth, so they must follow from the spec and nothing else.`

const task = `Write the hidden acceptance tests and a reference answer for the spec in spec.md.

Write these files:
- %[1]s/hidden/%[1]s_test.go, in package hidden. Read evalkit/evalkit.go and use it. Start the file with:
    var bin string
    func TestMain(m *testing.M) { os.Exit(evalkit.Main(m, "%[1]s", "../reference", &bin)) }
  Import evalkit as "evalwork/evalkit". Test only through the built command: arguments, stdin, files, stdout, stderr and exit codes.
- %[1]s/reference/cmd/%[1]s/main.go, and more files under %[1]s/reference/ if you need them: a small, correct answer. It exists only to prove the tests right.
- %[1]s/assumptions.md: each place where the spec is unclear, what a reasonable builder might do, and how your tests treat it.

Rules:
- Test every behavior the spec states. Do not test behavior the spec leaves open; list it in assumptions.md instead.
- Where the spec fixes the output format, check stdout exactly. For errors, check the exit code and that stderr names what the spec says it names.
- Each test must fail on a program that does nothing, unless the spec says that case prints nothing.
- Use only the standard library.
- Run go test ./%[1]s/hidden/ until it passes on the reference. Then run go vet ./..., gofmt -l . and go fix -diff ./... and fix what they report.
- Change nothing outside %[1]s/.`

type provenance struct {
	Spec          string    `json:"spec_sha256"`
	Model         string    `json:"model"`
	Effort        string    `json:"effort"`
	ClaudeVersion string    `json:"claude_version"`
	Generated     time.Time `json:"generated_at"`
	Attempts      int       `json:"attempts"`
	CostUSD       float64   `json:"cost_usd"`
	Tests         int       `json:"tests"`
	StubFailures  int       `json:"stub_failures"`
}

func main() {
	rails := flag.String("guardrails", "guardrails.yaml", "settings file with the eval_author model")
	retries := flag.Int("retries", 2, "times to send failed checks back to the model")
	budget := flag.Float64("budget", 20, "most dollars to spend on one eval")
	flag.Parse()
	if flag.NArg() == 0 {
		log.Fatal("usage: evalgen [-guardrails file] [-retries n] [-budget usd] evals/<name>...")
	}
	g, err := guardrails.Load(*rails)
	if err != nil {
		log.Fatal(err)
	}
	failed := false
	for _, dir := range flag.Args() {
		if err := generate(context.Background(), g, dir, *retries, *budget); err != nil {
			log.Printf("%s: %v", dir, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func generate(ctx context.Context, g guardrails.Guardrails, dir string, retries int, budget float64) error {
	dir = filepath.Clean(dir)
	name := filepath.Base(dir)
	spec, err := os.ReadFile(filepath.Join(dir, "spec.md"))
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "evalgen-"+name)
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	kit, err := os.ReadFile(filepath.Join(filepath.Dir(dir), "evalkit", "evalkit.go"))
	if err != nil {
		return err
	}
	if err := setUp(work, kit, spec, g.Go.Version); err != nil {
		return err
	}

	block, err := standards.Render(g.Go.Version)
	if err != nil {
		return err
	}
	// Outside the workspace, so the model cannot edit its own instructions.
	sys, err := os.CreateTemp("", "evalgen-system-*.md")
	if err != nil {
		return err
	}
	defer os.Remove(sys.Name())
	_, err = sys.WriteString(role + "\n\n" + block)
	if err := errors.Join(err, sys.Close()); err != nil {
		return err
	}
	system := sys.Name()

	author := g.Models.EvalAuthor
	opts := claude.Options{
		Dir:        work,
		Prompt:     fmt.Sprintf(task, name),
		Model:      author.Model,
		Effort:     author.Effort,
		SystemFile: system,
		Tools:      []string{"Read", "Write", "Edit", "Glob", "Grep", "Bash"},
		Allow: []string{"Bash(go test *)", "Bash(go build *)", "Bash(go vet *)", "Bash(go fix *)",
			"Bash(gofmt *)", "Bash(go run *)"},
		Mode:         "acceptEdits",
		MaxBudgetUSD: budget,
	}
	p := provenance{Model: author.Model, Effort: author.Effort, ClaudeVersion: claude.Version()}
	sum := sha256.Sum256(spec)
	p.Spec = hex.EncodeToString(sum[:])

	for attempt := 1; ; attempt++ {
		log.Printf("%s: attempt %d with %s at %s effort", name, attempt, author.Model, author.Effort)
		res, err := claude.Run(ctx, opts)
		p.CostUSD += res.CostUSD
		p.Attempts = attempt
		if err != nil {
			return err
		}
		checkErr := check(work, name, kit, &p)
		if checkErr == nil {
			break
		}
		if attempt > retries {
			return fmt.Errorf("checks still fail after %d attempts ($%.2f spent): %w", attempt, p.CostUSD, checkErr)
		}
		log.Printf("%s: checks failed, sending them back: %v", name, firstLine(checkErr))
		opts.Resume = res.SessionID
		opts.Prompt = "The checks failed:\n\n" + checkErr.Error() + "\n\nFix the files, then run the checks again."
		opts.MaxBudgetUSD = max(budget-p.CostUSD, 1)
	}

	runTests := func(dir string) error {
		if out, err := goCmd(".", []string{"EVAL_PRODUCT="}, "test", "-count=1", "./"+filepath.ToSlash(dir)+"/hidden/"); err != nil {
			return fmt.Errorf("%w\n%s", err, out)
		}
		return nil
	}
	if err := install(work, dir, name, runTests); err != nil {
		return err
	}
	p.Generated = time.Now().UTC()
	data, _ := json.Marshal(p, jsontext.WithIndent("  "))
	if err := os.WriteFile(filepath.Join(dir, "provenance.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	log.Printf("%s: done: %d tests, %d fail on a stub, $%.2f", name, p.Tests, p.StubFailures, p.CostUSD)
	return nil
}

// setUp makes a small module that mirrors evals/: evalkit, plus the spec.
func setUp(work string, kit, spec []byte, goVersion string) error {
	return writeFiles(work, map[string][]byte{
		"go.mod":             fmt.Appendf(nil, "module %s\n\ngo %s\n", workModule, goVersion),
		"spec.md":            spec,
		"evalkit/evalkit.go": kit,
	})
}

func writeFiles(root string, files map[string][]byte) error {
	for path, data := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// check runs the same checks the model was told to run, plus the stub test.
func check(work, name string, kit []byte, p *provenance) error {
	var errs []error
	if now, _ := os.ReadFile(filepath.Join(work, "evalkit", "evalkit.go")); !bytes.Equal(now, kit) {
		errs = append(errs, errors.New("evalkit/evalkit.go was changed; change nothing outside "+name+"/"))
	}
	if out, err := run(work, nil, "gofmt", "-l", "."); err != nil || out != "" {
		errs = append(errs, fmt.Errorf("gofmt -l lists files that need formatting:\n%s", out))
	}
	// go fix -diff exits non-zero when it has a change to suggest (go help fix).
	for _, args := range [][]string{{"vet", "./..."}, {"fix", "-diff", "./..."}} {
		if out, err := goCmd(work, nil, args...); err != nil {
			errs = append(errs, fmt.Errorf("go %s:\n%s", strings.Join(args, " "), out))
		}
	}
	for _, f := range []string{"hidden/" + name + "_test.go", "reference/cmd/" + name + "/main.go", "assumptions.md"} {
		if _, err := os.Stat(filepath.Join(work, name, f)); err != nil {
			errs = append(errs, fmt.Errorf("missing %s/%s", name, f))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	pkg := "./" + name + "/hidden/"
	// An empty EVAL_PRODUCT means the reference, whatever the user's shell sets.
	passed, failed, out := testCounts(work, []string{"EVAL_PRODUCT="}, pkg)
	if failed > 0 || passed < minTests {
		return fmt.Errorf("on the reference, %d tests pass and %d fail; all must pass and there must be at least %d:\n%s", passed, failed, minTests, out)
	}

	stub, err := os.MkdirTemp("", "evalgen-stub")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stub)
	if err := writeFiles(stub, map[string][]byte{
		"go.mod":                   []byte("module stub\n"),
		"cmd/" + name + "/main.go": []byte("package main\n\nfunc main() {}\n"),
	}); err != nil {
		return err
	}
	stubPassed, stubFailed, stubOut := testCounts(work, []string{"EVAL_PRODUCT=" + stub}, pkg)
	if stubPassed+stubFailed == 0 {
		return fmt.Errorf("no test ran against the do-nothing program:\n%s", stubOut)
	}
	if float64(stubFailed) < minStubFailures*float64(stubPassed+stubFailed) {
		return fmt.Errorf("only %d of %d tests fail on a program that does nothing; at least %.0f%% must fail, so most tests check real behavior",
			stubFailed, stubPassed+stubFailed, minStubFailures*100)
	}
	p.Tests, p.StubFailures = passed, stubFailed
	return nil
}

// testCounts runs go test -json and counts top-level tests that pass and fail.
func testCounts(dir string, env []string, pkg string) (passed, failed int, output string) {
	out, _ := goCmd(dir, env, "test", "-count=1", "-json", pkg)
	return countTests(out)
}

// countTests reads go test -json output. Build errors are not JSON, so it
// keeps them as text.
func countTests(out string) (passed, failed int, output string) {
	var text strings.Builder
	for line := range strings.Lines(out) {
		var e struct{ Action, Test, Output string }
		if json.Unmarshal([]byte(line), &e) != nil {
			text.WriteString(line) // build errors are not JSON
			continue
		}
		text.WriteString(e.Output)
		if e.Test == "" || strings.Contains(e.Test, "/") {
			continue
		}
		switch e.Action {
		case "pass":
			passed++
		case "fail":
			failed++
		}
	}
	return passed, failed, tail(text.String(), 60)
}

// install moves the generated files into the repo, fixing import paths on
// the way. It keeps the old files aside until runTests passes in place, and
// puts them back if it does not.
func install(work, dir, name string, runTests func(dir string) error) error {
	stage, err := os.MkdirTemp(dir, ".evalgen-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	fresh, old := filepath.Join(stage, "new"), filepath.Join(stage, "old")
	if err := copyTree(filepath.Join(work, name), fresh); err != nil {
		return err
	}
	if err := os.Mkdir(old, 0o755); err != nil {
		return err
	}
	if err := swap(dir, old); err != nil {
		return errors.Join(err, swap(old, dir))
	}
	if err := swap(fresh, dir); err != nil {
		return errors.Join(err, swap(old, dir))
	}
	if err := runTests(dir); err != nil {
		return errors.Join(fmt.Errorf("tests fail in place, so the old tests are back: %w", err), swap(old, dir))
	}
	return nil
}

// swap moves each generated part from one folder to another, replacing what is there.
func swap(from, to string) error {
	for _, part := range []string{"hidden", "reference", "assumptions.md"} {
		if err := os.RemoveAll(filepath.Join(to, part)); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(from, part), filepath.Join(to, part)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".go") {
			data = bytes.ReplaceAll(data, []byte(`"`+workModule+`/`), []byte(`"`+repoModule+`/`))
		}
		return writeFiles(dst, map[string][]byte{rel: data})
	})
}

func goCmd(dir string, env []string, args ...string) (string, error) {
	return run(dir, append(env, "GOFLAGS=-buildvcs=false"), "go", args...)
}

func run(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func tail(s string, lines int) string {
	parts := strings.Split(strings.TrimSpace(s), "\n")
	return strings.Join(parts[max(len(parts)-lines, 0):], "\n")
}

func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}
