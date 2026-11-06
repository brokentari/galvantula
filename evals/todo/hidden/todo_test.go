package hidden

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/brokentari/galvantula/evals/evalkit"
)

var bin string

func TestMain(m *testing.M) { os.Exit(evalkit.Main(m, "todo", "../reference", &bin)) }

// todo runs the command with dir as its working directory, so the default
// todo.json in dir carries over from one run to the next.
func todo(t *testing.T, dir string, args ...string) evalkit.Result {
	t.Helper()
	return evalkit.Run(t, bin, dir, "", args...)
}

// seed adds texts to the empty list in dir and checks that they get the ids
// 1, 2, 3 and so on. It stops the test if any add fails.
func seed(t *testing.T, dir string, texts ...string) {
	t.Helper()
	for i, text := range texts {
		todo(t, dir, "add", text).Want(t, 0, fmt.Sprintf("added %d\n", i+1))
	}
	if t.Failed() {
		t.FailNow()
	}
}

// checkJSON fails the test unless the file at path holds valid JSON.
func checkJSON(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read list file: %v", err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Errorf("%s is not valid JSON: %v\n%s", path, err, data)
	}
}

// wantNotFound checks a run that named an id which does not exist.
func wantNotFound(t *testing.T, r evalkit.Result) {
	t.Helper()
	r.WantFail(t, 1)
	if strings.TrimSpace(r.Stderr) == "" {
		t.Error("stderr is empty; want an error message")
	}
}

func stat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestAddPrintsSequentialIDs(t *testing.T) {
	dir := t.TempDir()
	todo(t, dir, "add", "first").Want(t, 0, "added 1\n")
	todo(t, dir, "add", "second").Want(t, 0, "added 2\n")
	todo(t, dir, "add", "third").Want(t, 0, "added 3\n")
	todo(t, dir, "list").Want(t, 0, "1 first\n2 second\n3 third\n")
}

func TestAddJoinsArgumentsWithSingleSpaces(t *testing.T) {
	dir := t.TempDir()
	todo(t, dir, "add", "buy", "milk", "today").Want(t, 0, "added 1\n")
	// The join puts one space between arguments and keeps the spaces inside one.
	todo(t, dir, "add", "two  spaces", "kept").Want(t, 0, "added 2\n")
	todo(t, dir, "list").Want(t, 0, "1 buy milk today\n2 two  spaces kept\n")
}

func TestTextKeepsSpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	text := "say \"hi\" \\ 100% %d\ttab café ☕ <a&b>"
	seed(t, dir, text)
	todo(t, dir, "list").Want(t, 0, "1 "+text+"\n")
	todo(t, dir, "list", "--all").Want(t, 0, "1 [ ] "+text+"\n")
}

func TestListShowsOpenItemsInIDOrder(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "a", "b", "c", "d", "e")
	todo(t, dir, "done", "2").Want(t, 0, "done 2\n")
	todo(t, dir, "done", "4").Want(t, 0, "done 4\n")
	todo(t, dir, "list").Want(t, 0, "1 a\n3 c\n5 e\n")
}

func TestListAllShowsEveryItemWithItsState(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "a", "b", "c")
	todo(t, dir, "done", "2").Want(t, 0, "done 2\n")
	todo(t, dir, "list", "--all").Want(t, 0, "1 [ ] a\n2 [x] b\n3 [ ] c\n")
}

func TestDoneMarksItemDone(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "a", "b")
	todo(t, dir, "done", "1").Want(t, 0, "done 1\n")
	todo(t, dir, "list").Want(t, 0, "2 b\n")
	todo(t, dir, "list", "--all").Want(t, 0, "1 [x] a\n2 [ ] b\n")
	todo(t, dir, "done", "2").Want(t, 0, "done 2\n")
	todo(t, dir, "list").Want(t, 0, "")
	todo(t, dir, "list", "--all").Want(t, 0, "1 [x] a\n2 [x] b\n")
}

func TestDoneAgainIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "a")
	todo(t, dir, "done", "1").Want(t, 0, "done 1\n")
	todo(t, dir, "done", "1").Want(t, 0, "done 1\n")
	todo(t, dir, "list", "--all").Want(t, 0, "1 [x] a\n")
}

func TestRmRemovesItem(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "a", "b", "c")
	todo(t, dir, "rm", "2").Want(t, 0, "removed 2\n")
	todo(t, dir, "list").Want(t, 0, "1 a\n3 c\n")
	todo(t, dir, "list", "--all").Want(t, 0, "1 [ ] a\n3 [ ] c\n")
	// rm also removes a done item.
	todo(t, dir, "done", "3").Want(t, 0, "done 3\n")
	todo(t, dir, "rm", "3").Want(t, 0, "removed 3\n")
	todo(t, dir, "list", "--all").Want(t, 0, "1 [ ] a\n")
}

func TestIDsAreNeverReused(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "a", "b")
	todo(t, dir, "rm", "2").Want(t, 0, "removed 2\n")
	todo(t, dir, "add", "c").Want(t, 0, "added 3\n")
	todo(t, dir, "rm", "1").Want(t, 0, "removed 1\n")
	todo(t, dir, "rm", "3").Want(t, 0, "removed 3\n")
	todo(t, dir, "list", "--all").Want(t, 0, "")
	todo(t, dir, "add", "d").Want(t, 0, "added 4\n")
	todo(t, dir, "list", "--all").Want(t, 0, "4 [ ] d\n")
}

func TestMissingFileIsEmptyList(t *testing.T) {
	dir := t.TempDir()
	todo(t, dir, "list").Want(t, 0, "")
	todo(t, dir, "list", "--all").Want(t, 0, "")
	todo(t, dir, "add", "a").Want(t, 0, "added 1\n")
	todo(t, dir, "list").Want(t, 0, "1 a\n")

	path := filepath.Join(t.TempDir(), "new.json")
	todo(t, dir, "--file", path, "list").Want(t, 0, "")
	todo(t, dir, "--file", path, "add", "b").Want(t, 0, "added 1\n")
	todo(t, dir, "--file", path, "list").Want(t, 0, "1 b\n")
}

func TestDefaultFileIsTodoJSONInWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "todo.json")
	todo(t, dir, "add", "a").Want(t, 0, "added 1\n")
	checkJSON(t, path)
	todo(t, t.TempDir(), "--file", path, "list").Want(t, 0, "1 a\n")
}

func TestFileFlagNamesTheFile(t *testing.T) {
	work := t.TempDir()
	path := filepath.Join(t.TempDir(), "list.json")
	todo(t, work, "--file", path, "add", "a").Want(t, 0, "added 1\n")
	todo(t, work, "--file", path, "add", "b").Want(t, 0, "added 2\n")
	checkJSON(t, path)
	if _, err := os.Stat(filepath.Join(work, "todo.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("--file %s also made todo.json in the working directory (stat: %v)", path, err)
	}
	// The default list is a different list.
	todo(t, work, "list").Want(t, 0, "")
	todo(t, work, "add", "c").Want(t, 0, "added 1\n")
	todo(t, work, "--file", path, "list").Want(t, 0, "1 a\n2 b\n")

	// A relative path starts at the working directory.
	todo(t, work, "--file", "rel.json", "add", "r").Want(t, 0, "added 1\n")
	todo(t, t.TempDir(), "--file", filepath.Join(work, "rel.json"), "list").Want(t, 0, "1 r\n")
}

func TestUnknownIDExitsOne(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "a", "b")
	todo(t, dir, "rm", "2").Want(t, 0, "removed 2\n")
	for _, args := range [][]string{
		{"done", "2"}, {"rm", "2"}, // removed
		{"done", "3"}, {"rm", "3"}, // never given out
		{"done", "99"}, {"rm", "99"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			wantNotFound(t, todo(t, dir, args...))
		})
	}
	todo(t, dir, "list", "--all").Want(t, 0, "1 [ ] a\n")

	// A missing file is an empty list, so no id exists.
	wantNotFound(t, todo(t, t.TempDir(), "done", "1"))
	wantNotFound(t, todo(t, t.TempDir(), "rm", "1"))
}

func TestUsageErrorsExitTwo(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown subcommand", []string{"frobnicate"}},
		{"add without text", []string{"add"}},
		{"add empty text", []string{"add", ""}},
		{"done without id", []string{"done"}},
		{"rm without id", []string{"rm"}},
		{"done non-numeric id", []string{"done", "abc"}},
		{"rm non-numeric id", []string{"rm", "abc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			seed(t, dir, "a")
			if r := todo(t, dir, tc.args...); r.Code != 2 {
				t.Errorf("%q: exit %d, want 2\nstdout: %s\nstderr: %s", tc.args, r.Code, r.Stdout, r.Stderr)
			}
			// The rejected command changes nothing and uses up no id.
			todo(t, dir, "list", "--all").Want(t, 0, "1 [ ] a\n")
			todo(t, dir, "add", "b").Want(t, 0, "added 2\n")
		})
	}
}

func TestSaveRenamesNewFileOverList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "todo.json")
	seed(t, dir, "a")
	for _, step := range []struct {
		args []string
		want string
	}{
		{[]string{"add", "b"}, "added 2\n"},
		{[]string{"done", "1"}, "done 1\n"},
		{[]string{"rm", "2"}, "removed 2\n"},
	} {
		before := stat(t, path)
		todo(t, dir, step.args...).Want(t, 0, step.want)
		if os.SameFile(before, stat(t, path)) {
			t.Errorf("%q wrote todo.json in place; want a temporary file renamed over it", step.args)
		}
		// The temporary file became the list, so no other file is left.
		if names := dirNames(t, dir); !slices.Equal(names, []string{"todo.json"}) {
			t.Errorf("after %q the directory holds %q; want only todo.json", step.args, names)
		}
		checkJSON(t, path)
	}
	todo(t, dir, "list", "--all").Want(t, 0, "1 [x] a\n")
}

func TestSaveWritesTemporaryFileInListDirectory(t *testing.T) {
	dir := t.TempDir()
	// A program that puts its temporary file in TMPDIR cannot save now.
	t.Setenv("TMPDIR", filepath.Join(dir, "missing"))
	seed(t, dir, "a", "b")
	todo(t, dir, "done", "1").Want(t, 0, "done 1\n")
	todo(t, dir, "rm", "2").Want(t, 0, "removed 2\n")
	todo(t, dir, "list", "--all").Want(t, 0, "1 [x] a\n")
}
