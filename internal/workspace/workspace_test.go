package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestNodeWorkflow(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "repo"))
	must(t, err)
	must(t, os.WriteFile(filepath.Join(r.Dir, "AGENTS.md"), []byte("rules"), 0o644))
	if ok, err := r.CommitAll(r.Dir, "standards"); !ok || err != nil {
		t.Fatalf("commit standards: %v, %v", ok, err)
	}
	if ok, _ := r.CommitAll(r.Dir, "again"); ok {
		t.Error("CommitAll committed with nothing to commit")
	}

	wt, err := r.Worktree("impl")
	must(t, err)
	must(t, os.WriteFile(filepath.Join(wt, "main.go"), []byte("package main"), 0o644))
	must(t, os.WriteFile(filepath.Join(wt, "AGENTS.md"), []byte("changed"), 0o644))
	changed, err := r.Changed(wt)
	must(t, err)
	if !slices.Equal(changed, []string{"AGENTS.md"}) {
		t.Errorf("Changed = %v, want only AGENTS.md (new files do not count)", changed)
	}
	// git quotes a name like this one unless asked not to.
	must(t, os.WriteFile(filepath.Join(wt, "café \"1\".txt"), nil, 0o644))
	must(t, os.WriteFile(filepath.Join(wt, " lead.txt"), nil, 0o644))
	added, err := r.NewFiles(wt)
	must(t, err)
	if want := []string{" lead.txt", "café \"1\".txt", "main.go"}; !slices.Equal(added, want) {
		t.Errorf("NewFiles = %q, want %q", added, want)
	}

	must(t, r.Reset(wt))
	if _, err := os.Stat(filepath.Join(wt, "main.go")); err == nil {
		t.Error("Reset kept a new file")
	}
	must(t, os.WriteFile(filepath.Join(wt, "main.go"), []byte("package main"), 0o644))
	_, err = r.CommitAll(wt, "impl: attempt 1")
	must(t, err)
	must(t, r.Merge("impl"))
	if _, err := os.Stat(filepath.Join(r.Dir, "main.go")); err != nil {
		t.Error("main does not have the merged file")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Git quotes non-ASCII names unless it is told to use NUL separators.
func TestTrackedKeepsNonASCIINames(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "repo"))
	must(t, err)
	must(t, os.WriteFile(filepath.Join(r.Dir, "café.go"), []byte("package x"), 0o644))
	if _, err := r.CommitAll(r.Dir, "x"); err != nil {
		t.Fatal(err)
	}
	got, err := r.Tracked(r.Dir)
	must(t, err)
	if !slices.Equal(got, []string{"café.go"}) {
		t.Errorf("Tracked = %q, want [café.go]", got)
	}
}

// Nodes work in parallel from the same main. The second to finish merges
// into a main that moved, and must still merge, and Changed must compare
// with where the node started, not with the new main.
func TestParallelNodesMerge(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "repo"))
	must(t, err)
	must(t, os.WriteFile(filepath.Join(r.Dir, "base.txt"), []byte("base"), 0o644))
	_, err = r.CommitAll(r.Dir, "base")
	must(t, err)

	a, err := r.Worktree("a")
	must(t, err)
	b, err := r.Worktree("b")
	must(t, err)
	must(t, os.MkdirAll(filepath.Join(a, "pkga"), 0o755))
	must(t, os.MkdirAll(filepath.Join(b, "pkgb"), 0o755))
	must(t, os.WriteFile(filepath.Join(a, "pkga", "a.go"), []byte("package a"), 0o644))
	must(t, os.WriteFile(filepath.Join(b, "pkgb", "b.go"), []byte("package b"), 0o644))
	_, err = r.CommitAll(a, "a")
	must(t, err)
	must(t, r.Merge("a"))
	// b is checked before it commits, as in the executor, while main has moved on.
	if changed, err := r.Changed(b); err != nil || len(changed) != 0 {
		t.Errorf("Changed(b) = %v, %v; want nothing: b did not change a file that main had", changed, err)
	}
	_, err = r.CommitAll(b, "b")
	must(t, err)
	must(t, r.Merge("b"))
	for _, f := range []string{"base.txt", "pkga/a.go", "pkgb/b.go"} {
		if _, err := os.Stat(filepath.Join(r.Dir, f)); err != nil {
			t.Errorf("main lacks %s after both merges", f)
		}
	}
}

// Two nodes that change the same file conflict. The merge must fail, name
// the node, and leave main clean for the next one.
func TestMergeConflictIsReportedAndAborted(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "repo"))
	must(t, err)
	must(t, os.WriteFile(filepath.Join(r.Dir, "f.txt"), []byte("base\n"), 0o644))
	_, err = r.CommitAll(r.Dir, "base")
	must(t, err)
	a, err := r.Worktree("a")
	must(t, err)
	b, err := r.Worktree("b")
	must(t, err)
	must(t, os.WriteFile(filepath.Join(a, "f.txt"), []byte("from a\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(b, "f.txt"), []byte("from b\n"), 0o644))
	for _, n := range []struct{ id, dir string }{{"a", a}, {"b", b}} {
		_, err := r.CommitAll(n.dir, n.id)
		must(t, err)
	}
	must(t, r.Merge("a"))
	err = r.Merge("b")
	if err == nil || !strings.Contains(err.Error(), "merge conflict: node b") {
		t.Fatalf("got %v, want a conflict that names node b", err)
	}
	if status, _ := r.git(r.Dir, "status", "--porcelain"); status != "" {
		t.Errorf("main is not clean after the aborted merge:\n%s", status)
	}
}
