package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Review reports must not block a rerun; any other change must.
func TestUncommitted(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "start"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(path string) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("docs/reviews/phase-0.md")
	if dirty, err := uncommitted(); err != nil || dirty != "" {
		t.Fatalf("with only a review report: %q, %v; want a clean tree", dirty, err)
	}
	write("a.go")
	if dirty, err := uncommitted(); err != nil || dirty == "" {
		t.Fatalf("with a.go changed: %q, %v; want it listed", dirty, err)
	}
}
