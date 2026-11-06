package main

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A run the eval loses still counts, as a failed run, so the pass rate
// never leaves it out.
func TestLostRunsCountAsFailures(t *testing.T) {
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer refuse.Close()
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo = filepath.Join(repo, "..", "..")
	t.Chdir(t.TempDir()) // runEvals writes evals/results here
	err = runEvals([]string{"-server", refuse.URL, "-db", "f.db", "-runs", "runs", "-n", "2",
		"-graph", filepath.Join(repo, "graphs", "phase1.yaml"), filepath.Join(repo, "evals", "wordfreq")})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("evals", "results", time.Now().Format(time.DateOnly)+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("recorded %d runs, want 2:\n%s", len(lines), data)
	}
	for _, l := range lines {
		var r evalResult
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatal(err)
		}
		if r.Pass || r.Status != "error" || !strings.Contains(r.Reason, "refused") || r.Eval != "wordfreq" {
			t.Errorf("got %+v, want a failed wordfreq run with status error", r)
		}
	}
}

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
