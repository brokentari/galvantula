// Package claude runs Claude Code headless (claude -p) and reads its result.
package claude

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Options configures one headless run.
type Options struct {
	Dir          string // working directory
	Prompt       string
	Model        string
	Effort       string
	SystemFile   string   // appended to the system prompt
	Tools        []string // built-in tools the session may see
	Allow        []string // permission rules that need no prompt, such as "Bash(go test *)"
	Mode         string   // permission mode; anything that would prompt is denied
	Resume       string   // session id to continue
	MaxBudgetUSD float64
}

// Result is the final JSON object that claude -p prints.
type Result struct {
	SessionID string  `json:"session_id"`
	Subtype   string  `json:"subtype"`
	IsError   bool    `json:"is_error"`
	Text      string  `json:"result"`
	CostUSD   float64 `json:"total_cost_usd"`
	Turns     int     `json:"num_turns"`
}

// Run starts claude -p and waits for it to finish.
func Run(ctx context.Context, o Options) (Result, error) {
	args := []string{"-p", o.Prompt,
		"--output-format", "json",
		"--model", o.Model,
		"--effort", o.Effort,
		"--permission-mode", o.Mode,
		"--permission-prompts", "none",
		"--strict-mcp-config",
	}
	// --bare needs an API key. Without one, --safe-mode keeps the user's
	// login but turns off their CLAUDE.md, skills, plugins, hooks and MCP
	// servers, so the run sees only what we pass in.
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		args = append(args, "--bare")
	} else {
		args = append(args, "--safe-mode")
	}
	if len(o.Tools) > 0 {
		args = append(args, "--tools", strings.Join(o.Tools, ","))
	}
	if len(o.Allow) > 0 {
		args = append(args, "--allowedTools", strings.Join(o.Allow, ","))
	}
	if o.SystemFile != "" {
		args = append(args, "--append-system-prompt-file", o.SystemFile)
	}
	if o.Resume != "" {
		args = append(args, "--resume", o.Resume)
	}
	if o.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(o.MaxBudgetUSD, 'f', 2, 64))
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = o.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 10 * time.Second // tools it started must not keep Run waiting after a cancel
	runErr := cmd.Run()
	if ctx.Err() != nil {
		// A cancel kills the process, and Wait reports "signal: killed".
		// Report the cancel instead, so callers do not treat it as a crash.
		return Result{}, fmt.Errorf("claude -p: %w", context.Cause(ctx))
	}

	var r Result
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		return r, fmt.Errorf("claude -p: %w; stderr: %s", errors.Join(runErr, err), strings.TrimSpace(stderr.String()))
	}
	if r.IsError {
		return r, fmt.Errorf("claude -p ended with %s: %s", r.Subtype, r.Text)
	}
	return r, nil
}

// Version returns the installed Claude Code version.
func Version() string {
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
