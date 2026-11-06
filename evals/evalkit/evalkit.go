// Package evalkit builds a product's command and runs it, for the hidden
// eval tests. The tests treat the product as a black box: arguments and
// stdin in, stdout, stderr and exit code out.
package evalkit

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Build compiles ./cmd/<name> from the product under test into dir.
// EVAL_PRODUCT names the product's root directory. Without it, Build uses
// reference, so `go test ./evals/...` checks the tests against a known-good
// implementation.
func Build(name, reference, dir string) (string, error) {
	src := cmp.Or(os.Getenv("EVAL_PRODUCT"), reference)
	bin := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/"+name)
	cmd.Dir = src
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build ./cmd/%s in %s: %w\n%s", name, src, err, out)
	}
	return bin, nil
}

// Main builds the command once for a test package, runs the tests, and
// cleans up. Call it from TestMain.
func Main(m *testing.M, name, reference string, bin *string) int {
	dir, err := os.MkdirTemp("", "eval-"+name)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	if *bin, err = Build(name, reference, dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

// Result is what one run of the command produced.
type Result struct {
	Stdout, Stderr string
	Code           int
}

// timeout fails one run of a product that hangs, instead of the whole package.
const timeout = 30 * time.Second

// Run runs bin with args in dir (the test's temp dir if empty), feeding stdin.
func Run(t *testing.T, bin, dir, stdin string, args ...string) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = cmp.Or(dir, t.TempDir())
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if ctx.Err() != nil {
		t.Fatalf("run %s %q: no exit after %s", bin, args, timeout)
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		res.Code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s: %v", bin, err)
	}
	return res
}

// Want fails the test unless the run exited with code and printed stdout.
func (r Result) Want(t *testing.T, code int, stdout string) {
	t.Helper()
	if r.Code != code || r.Stdout != stdout {
		t.Errorf("exit %d, stdout:\n%s\nwant exit %d, stdout:\n%s\nstderr: %s", r.Code, r.Stdout, code, stdout, r.Stderr)
	}
}

// WantFail fails the test unless the run exited with code, printed nothing
// to stdout, and mentioned every word in stderr.
func (r Result) WantFail(t *testing.T, code int, words ...string) {
	t.Helper()
	if r.Code != code || r.Stdout != "" {
		t.Errorf("exit %d, stdout %q; want exit %d and no stdout (stderr: %s)", r.Code, r.Stdout, code, r.Stderr)
	}
	for _, w := range words {
		if !strings.Contains(r.Stderr, w) {
			t.Errorf("stderr %q does not mention %q", r.Stderr, w)
		}
	}
}
