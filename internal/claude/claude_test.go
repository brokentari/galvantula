package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeClaude puts a shell script named claude first on PATH.
func fakeClaude(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestRunResults(t *testing.T) {
	tests := []struct {
		name, script string
		wantErr      string // empty: no error
	}{
		{"success", `echo '{"type":"system","subtype":"init"}'; echo '{"type":"result","subtype":"success","is_error":false,"result":"done","total_cost_usd":0.5,"session_id":"s1"}'`, ""},
		{"model error", `echo '{"type":"result","subtype":"error_max_turns","is_error":true,"result":"boom"}'`, "error_max_turns: boom"},
		{"not JSON", `echo 'claude: bad flag' >&2; exit 2`, "claude: bad flag"},
		{"usage limit", `echo '{"type":"result","is_error":true,"result":"You have hit your usage limit · resets 5pm"}'`, "usage limit reached"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClaude(t, tt.script)
			res, err := Run(t.Context(), Options{Dir: t.TempDir(), Prompt: "x", Model: "m", Effort: "low", Mode: "dontAsk"})
			switch {
			case tt.wantErr == "" && (err != nil || res.Text != "done" || res.CostUSD != 0.5 || res.SessionID != "s1"):
				t.Errorf("got %+v, %v; want the parsed result", res, err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("got error %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// A tool that claude started can outlive a cancel and keep stdout open.
// Run must still return.
func TestRunReturnsAfterCancelWithChildHoldingStdout(t *testing.T) {
	fakeClaude(t, "sleep 60 &\nsleep 60\n")
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Run(ctx, Options{Dir: t.TempDir(), Prompt: "x", Model: "m", Effort: "low", Mode: "dontAsk"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %v, want the deadline", err)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("Run took %s after the cancel", d)
	}
}

// Without an API key, runs keep the user's login but turn off their
// customizations; with one, they use --bare.
func TestRunIsolation(t *testing.T) {
	for key, want := range map[string]string{"": "--safe-mode", "sk-test": "--bare"} {
		t.Setenv("ANTHROPIC_API_KEY", key)
		args := filepath.Join(t.TempDir(), "args")
		fakeClaude(t, `echo "$@" > `+args+`; echo '{"type":"result","is_error":false,"result":"ok"}'`)
		if _, err := Run(t.Context(), Options{Dir: t.TempDir(), Prompt: "x", Model: "m", Effort: "low", Mode: "dontAsk"}); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(args)
		if !strings.Contains(string(got), want) {
			t.Errorf("with key %q, args %q do not contain %s", key, got, want)
		}
	}
}

func TestRunLogsSteps(t *testing.T) {
	fakeClaude(t, `cat <<'EOS'
{"type":"assistant","message":{"content":[{"type":"text","text":"I will   write\nthe file."},{"type":"tool_use","name":"Write","input":{"file_path":"main.go","content":"package main"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"result","is_error":false,"result":"done"}
EOS
`)
	var lines []string
	_, err := Run(t.Context(), Options{Dir: t.TempDir(), Prompt: "x", Model: "m", Effort: "low", Mode: "dontAsk",
		Log: func(l string) { lines = append(lines, l) }})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"I will write the file.", "Write: main.go", "Bash: go test ./..."}
	if !slices.Equal(lines, want) {
		t.Errorf("log = %q, want %q", lines, want)
	}
}

// A cancel must read as a cancel, not as a crashed process.
func TestRunReportsCancel(t *testing.T) {
	fakeClaude(t, "exec sleep 30\n") // stands in for a long claude -p run

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
