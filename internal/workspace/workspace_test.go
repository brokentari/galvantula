package workspace

import (
	"os"
	"path/filepath"
	"slices"
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
