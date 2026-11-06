package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVerdict(t *testing.T) {
	tests := map[string]string{
		"Findings.\n\nVerdict: Ship.\n":                                              "Ship.",
		"Verdict: fix 1 and 3 first.\nLean: -4\n":                                    "fix 1 and 3 first.",
		"**Verdict: Ship.**\n":                                                       "Ship.",
		"What this change does: a thing.\nNo issue.":                                 "no verdict line",
		"What this change does: a thing.\n\nLooks good. Ship.\n\nI checked the API.": "Ship.",
		"Looks good. Ship. But fix 2.\n":                                             "no verdict line",
	}
	for report, want := range tests {
		if got := verdict(report); got != want {
			t.Errorf("verdict(%q) = %q, want %q", report, got, want)
		}
	}
}

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
