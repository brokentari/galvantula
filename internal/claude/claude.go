// Package claude runs Claude Code headless (claude -p) and reads its result.
package claude

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
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
	SessionID    string   // id for a new session
	Resume       string   // session id to continue
	MaxBudgetUSD float64

	// Bin is the claude executable; empty means "claude" on PATH.
	Bin string
	// Bare chooses --bare (auth only by ANTHROPIC_API_KEY) over --safe-mode
	// (the login, without the user's CLAUDE.md, skills, plugins, hooks or MCP
	// servers). Nil decides by whether this process has ANTHROPIC_API_KEY.
	Bare *bool
	// Exec builds the command, for example inside a sandbox. Nil runs it directly.
	Exec func(ctx context.Context, name string, args ...string) *exec.Cmd
	// Log receives one short line for each step the session takes.
	Log func(line string)
}

// ErrUsageLimit means the account reached its plan's usage limit. Trying
// again soon only fails again, and must never buy more usage.
var ErrUsageLimit = errors.New("usage limit reached")

// limitPhrases appear in claude's message when the account hits a limit.
var limitPhrases = []string{"usage limit", "spend limit", "usage credits", "out of credits", "hit your"}

func isLimit(msg string) bool {
	msg = strings.ToLower(msg)
	return slices.ContainsFunc(limitPhrases, func(p string) bool { return strings.Contains(msg, p) })
}

// ErrNoResult means claude stopped without a result: it crashed, could not
// start, or could not reach the API. The task itself did not fail.
var ErrNoResult = errors.New("claude -p gave no result")

// Result is the final "result" object of the run.
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
		"--output-format", "stream-json", "--verbose",
		"--model", o.Model,
		"--effort", o.Effort,
		"--permission-mode", o.Mode,
		"--permission-prompts", "none",
		"--strict-mcp-config",
	}
	bare := os.Getenv("ANTHROPIC_API_KEY") != ""
	if o.Bare != nil {
		bare = *o.Bare
	}
	if bare {
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
	if o.SessionID != "" {
		args = append(args, "--session-id", o.SessionID)
	}
	if o.Resume != "" {
		args = append(args, "--resume", o.Resume)
	}
	if o.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(o.MaxBudgetUSD, 'f', 2, 64))
	}

	run := o.Exec
	if run == nil {
		run = exec.CommandContext
	}
	cmd := run(ctx, cmp.Or(o.Bin, "claude"), args...)
	cmd.Dir = o.Dir
	cmd.WaitDelay = 10 * time.Second // tools it started must not keep Run waiting after a cancel
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// The goroutine closes pw after Wait returns. WaitDelay bounds Wait, so a
	// tool that outlives claude and holds stdout cannot keep read waiting.
	stdout, pw := io.Pipe()
	cmd.Stdout = pw
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("claude -p: %w", err)
	}
	waited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		pw.Close()
		waited <- err
	}()
	r, found := read(stdout, o.Log)
	io.Copy(io.Discard, stdout) // read stops early if one line passes 64 MB; drain the pipe so Wait can finish
	runErr := <-waited
	if ctx.Err() != nil {
		// A cancel kills the process, and Wait reports "signal: killed".
		// Report the cancel instead, so callers do not treat it as a crash.
		return r, fmt.Errorf("claude -p: %w", context.Cause(ctx))
	}
	if (r.IsError && isLimit(r.Text)) || (!found && isLimit(stderr.String())) {
		return r, fmt.Errorf("%w: %s", ErrUsageLimit, strings.TrimSpace(r.Text+" "+stderr.String()))
	}
	if !found {
		return r, fmt.Errorf("%w: %w; stderr: %s", ErrNoResult, cmp.Or(runErr, errors.New("no result line")), strings.TrimSpace(stderr.String()))
	}
	if r.IsError {
		return r, fmt.Errorf("claude -p ended with %s: %s", r.Subtype, r.Text)
	}
	return r, nil
}

// line is the part of a stream-json line that Run reads.
type line struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

// read follows the stream, sends a summary of each step to log, and
// returns the final result.
func read(out io.Reader, log func(string)) (Result, bool) {
	var r Result
	found := false
	sc := bufio.NewScanner(out)
	sc.Buffer(nil, 64<<20) // one line holds a whole message
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if l.Type == "result" {
			found = json.Unmarshal(sc.Bytes(), &r) == nil
			continue
		}
		if log == nil || l.Type != "assistant" {
			continue
		}
		for _, c := range l.Message.Content {
			switch c.Type {
			case "text":
				log(short(c.Text))
			case "tool_use":
				log(c.Name + ": " + short(toolArg(c.Input)))
			}
		}
	}
	return r, found
}

// toolArg picks the input that says most about a tool call.
func toolArg(in map[string]any) string {
	for _, k := range []string{"command", "file_path", "pattern", "path"} {
		if v, ok := in[k].(string); ok {
			return v
		}
	}
	return ""
}

func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		return string(r[:200]) + "…" // cut by rune, so a character is never split
	}
	return s
}

// Version returns the installed Claude Code version.
func Version() string {
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
