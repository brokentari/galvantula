package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The hidden tests score a product in the sandbox. They must still build and
// run there with only the parts of the repo they need mounted.
func TestHiddenTestsScoreAProduct(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap is not installed")
	}
	t.Chdir("../..")
	product := t.TempDir()
	if err := os.WriteFile(filepath.Join(product, "go.mod"), []byte("module product\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(product, os.DirFS("evals/wordfreq/reference")); err != nil {
		t.Fatal(err)
	}
	passed, total, err := hiddenTests(t.Context(), t.TempDir(), "evals/wordfreq", product)
	if err != nil || total == 0 || passed != total {
		t.Fatalf("got %d/%d, %v; want every hidden test to pass", passed, total, err)
	}

	// A product without the program fails, and must not look like a pass.
	if err := os.RemoveAll(filepath.Join(product, "cmd")); err != nil {
		t.Fatal(err)
	}
	if passed, _, err := hiddenTests(t.Context(), t.TempDir(), "evals/wordfreq", product); err == nil && passed > 0 {
		t.Errorf("a product without its program passed %d hidden tests", passed)
	}
}
