package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A cancel must read as a cancel, not as a crashed process.
func TestRunReportsCancel(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nexec sleep 30\n" // stands in for a long claude -p run
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Run(ctx, Options{Dir: t.TempDir(), Prompt: "x", Model: "m", Effort: "low", Mode: "dontAsk"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %v, want an error that wraps the context's error", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("Run kept waiting after the cancel")
	}
}
