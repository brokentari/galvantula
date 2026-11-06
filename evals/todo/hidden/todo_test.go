// Hidden acceptance tests for evals/todo/spec.md. The factory never sees them.
package hidden

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/brokentari/galvantula/evals/evalkit"
)

var bin string

func TestMain(m *testing.M) { os.Exit(evalkit.Main(m, "todo", "../reference", &bin)) }

// todo runs the command in dir. Each call is a separate process, so state
// must survive in the file.
func todo(t *testing.T, dir string, args ...string) evalkit.Result {
	t.Helper()
	return evalkit.Run(t, bin, dir, "", args...)
}

func TestAddAndList(t *testing.T) {
	dir := t.TempDir()
	todo(t, dir, "add", "buy", "milk").Want(t, 0, "added 1\n")
	todo(t, dir, "add", "call  mum").Want(t, 0, "added 2\n")
	todo(t, dir, "list").Want(t, 0, "1 buy milk\n2 call  mum\n")
}

func TestMissingFileIsEmpty(t *testing.T) {
	todo(t, "", "list").Want(t, 0, "")
	todo(t, "", "list", "--all").Want(t, 0, "")
}

func TestDone(t *testing.T) {
	dir := t.TempDir()
	todo(t, dir, "add", "one")
	todo(t, dir, "add", "two")
	todo(t, dir, "done", "1").Want(t, 0, "done 1\n")
	todo(t, dir, "done", "1").Want(t, 0, "done 1\n")
	todo(t, dir, "list").Want(t, 0, "2 two\n")
	todo(t, dir, "list", "--all").Want(t, 0, "1 [x] one\n2 [ ] two\n")
}

func TestIdsAreNeverReused(t *testing.T) {
	dir := t.TempDir()
	todo(t, dir, "add", "a")
	todo(t, dir, "add", "b")
	todo(t, dir, "rm", "2").Want(t, 0, "removed 2\n")
	todo(t, dir, "add", "c").Want(t, 0, "added 3\n")
	todo(t, dir, "rm", "1")
	todo(t, dir, "rm", "3")
	todo(t, dir, "add", "d").Want(t, 0, "added 4\n")
	todo(t, dir, "list").Want(t, 0, "4 d\n")
}

func TestFileFlag(t *testing.T) {
	dir := t.TempDir()
	todo(t, dir, "--file", "work.json", "add", "ship it").Want(t, 0, "added 1\n")
	todo(t, dir, "list").Want(t, 0, "")
	todo(t, dir, "--file", "work.json", "list").Want(t, 0, "1 ship it\n")
	if _, err := os.Stat(filepath.Join(dir, "work.json")); err != nil {
		t.Errorf("work.json was not written: %v", err)
	}
}

func TestNoTempFilesLeft(t *testing.T) {
	dir := t.TempDir()
	todo(t, dir, "add", "a")
	todo(t, dir, "done", "1")
	todo(t, dir, "rm", "1")
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"todo.json"}) {
		t.Errorf("directory holds %v, want only todo.json", names)
	}
}

func TestUnknownId(t *testing.T) {
	dir := t.TempDir()
	todo(t, dir, "add", "a")
	todo(t, dir, "done", "7").WantFail(t, 1, "7")
	todo(t, dir, "rm", "7").WantFail(t, 1, "7")
	todo(t, dir, "list").Want(t, 0, "1 a\n")
}

func TestUsageErrors(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{},
		{"frobnicate"},
		{"add"},
		{"add", ""},
		{"done"},
		{"done", "x"},
		{"rm"},
	} {
		if res := todo(t, dir, args...); res.Code != 2 {
			t.Errorf("todo %q: exit %d, want 2 (stderr: %s)", args, res.Code, res.Stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "todo.json")); err == nil {
		t.Error("a usage error wrote todo.json")
	}
}
