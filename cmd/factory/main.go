// Command factory runs the software factory.
//
//	factory serve    [-addr localhost:7070] [-db factory.db] [-runs runs] [-guardrails guardrails.yaml] [-ui ui/dist] [-workers 3]
//	factory start    [-server http://localhost:7070] (-fake script.yaml | -spec spec.md) graph.yaml
//	factory eval     [-server http://localhost:7070] [-db factory.db] [-runs runs] [-n 10] [-parallel 1] [-graph graphs/phase1.yaml] evals/<name>...
//	factory mcp      [-server http://localhost:7070] [-graphs folder]
//	factory validate graph.yaml...
//	factory standards [-guardrails guardrails.yaml] [-o AGENTS.md]
package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/brokentari/galvantula/graphs"
	"github.com/brokentari/galvantula/internal/crit"
	"github.com/brokentari/galvantula/internal/event"
	"github.com/brokentari/galvantula/internal/executor"
	"github.com/brokentari/galvantula/internal/graph"
	"github.com/brokentari/galvantula/internal/guardrails"
	"github.com/brokentari/galvantula/internal/mcp"
	"github.com/brokentari/galvantula/internal/runner"
	"github.com/brokentari/galvantula/internal/server"
	"github.com/brokentari/galvantula/standards"
)

const usage = `usage:
  factory serve    [-addr localhost:7070] [-db factory.db] [-runs runs] [-guardrails guardrails.yaml] [-ui ui/dist] [-workers 3]
  factory start    [-server http://localhost:7070] (-fake script.yaml | -spec spec.md) graph.yaml
  factory eval     [-server http://localhost:7070] [-db factory.db] [-runs runs] [-n 10] [-parallel 1] [-graph graphs/phase1.yaml] evals/<name>...
  factory mcp      [-server http://localhost:7070] [-graphs folder]
  factory validate graph.yaml...
  factory standards [-guardrails guardrails.yaml] [-o AGENTS.md]`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "serve":
		err = serve(args)
	case "start":
		err = start(args)
	case "mcp":
		err = serveMCP(args)
	case "validate":
		err = validate(args)
	case "standards":
		err = writeStandards(args)
	case "eval":
		err = runEvals(args)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "factory:", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "localhost:7070", "listen address; the API has no login, so keep it local")
	dbPath := fs.String("db", "factory.db", "event log file")
	runsDir := fs.String("runs", "runs", "folder for each run's product repo and worktrees")
	railsPath := fs.String("guardrails", "guardrails.yaml", "settings for new runs")
	uiDir := fs.String("ui", "ui/dist", "built UI to serve; empty to serve only the API")
	workers := fs.Int("workers", 3, "nodes of one run that may execute at the same time")
	fs.Parse(args)

	if _, err := os.Stat(*uiDir); *uiDir != "" && err != nil {
		slog.Warn("no built UI; serving only the API", "dir", *uiDir)
		*uiDir = ""
	}

	rails, err := guardrails.Load(*railsPath)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(*runsDir) // the sandbox needs absolute paths
	if err != nil {
		return err
	}
	store, err := event.Open(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	// Bind the port before resuming runs. If another server already holds
	// it, this one stops here, before it can drive that server's runs too.
	// shortcut: two servers on different ports can still share one database; add a lock if that becomes a real setup.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// This server holds the port, so it is the only server and no attempt is using a cache.
	if err := os.RemoveAll(executor.BuildCaches(root)); err != nil {
		ln.Close()
		return err
	}
	run := runner.New(ctx, store, *workers)
	run.Root, run.Auth, run.Commit = root, credential(), factoryCommit()
	if run.Auth == "" {
		slog.Warn("no CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY; only fake runs can start")
	} else if err := sandboxReady(); err != nil {
		ln.Close()
		return fmt.Errorf("%w; unset the credential to serve fake runs only", err)
	}
	if err := run.ResumeAll(); err != nil {
		ln.Close()
		return err
	}

	sessions := crit.Find()
	if sessions == nil {
		slog.Info("crit is not on PATH; gates have no Crit button")
	} else {
		slog.Info("crit found", "path", sessions.Path())
		defer sessions.StopAll()
	}
	srv := &http.Server{Handler: server.New(store, run, rails, *uiDir, sessions)}
	defer context.AfterFunc(ctx, func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	})()
	slog.Info("factory listening", "addr", *addr, "db", *dbPath)
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	run.Wait()
	return nil
}

// factoryCommit names the code this server runs, so eval results from two
// versions of the factory never mix. Results are left out: an eval batch
// writes them, and that does not change the code.
func factoryCommit() string {
	head, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		slog.Warn("runs will not record a commit: git cannot name one", "err", err)
		return ""
	}
	c := strings.TrimSpace(string(head))
	if status, err := exec.Command("git", "status", "--porcelain", "--", ".", ":!evals/results").Output(); err != nil || len(status) > 0 {
		c += "-dirty"
	}
	return c
}

// sandboxReady reports what agents need that this host lacks. Without the
// check, each attempt would fail as if the agent were at fault.
func sandboxReady() error {
	if _, err := exec.LookPath("bwrap"); err != nil {
		return errors.New("agents run in bubblewrap, which needs Linux and the bwrap command")
	}
	for _, p := range []string{"/usr/bin/go", "/usr/bin/gofmt"} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("the sandbox finds tools only in /usr/bin, and %s is missing", p)
		}
	}
	return nil
}

// credential returns the credential that sandboxed agents use, from this
// process's environment. A token from claude setup-token comes first.
func credential() string {
	for _, k := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		if v := os.Getenv(k); v != "" {
			return k + "=" + v
		}
	}
	return ""
}

func start(args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	addr := fs.String("server", "http://localhost:7070", "factory server")
	fake := fs.String("fake", "", "play this fake executor script")
	spec := fs.String("spec", "", "build this spec with Claude Code")
	auto := fs.Bool("auto-approve", false, "approve every gate without asking; for evals")
	fs.Parse(args)
	if fs.NArg() != 1 || (*fake == "") == (*spec == "") {
		return errors.New("start needs one graph file and either -fake script.yaml or -spec spec.md")
	}
	g, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	req := server.StartRequest{Graph: string(g), Executor: "fake", AutoApprove: *auto}
	file := *fake
	if *spec != "" {
		req.Executor, file = "claude", *spec
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if *spec != "" {
		req.Spec = string(data)
	} else {
		req.FakeScript = string(data)
	}
	id, err := startRun(*addr, req)
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

// startRun asks the server to start a run and returns its id.
func startRun(addr string, req server.StartRequest) (string, error) {
	body, _ := json.Marshal(req)
	client := &http.Client{Timeout: 30 * time.Second} // a stuck server must not hang the eval
	resp, err := client.Post(addr+"/api/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("server refused the run (%s): %s", resp.Status, bytes.TrimSpace(out))
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

// serveMCP runs the MCP server on standard input and output, for Claude Code.
// Only protocol messages go to standard output; logs go to standard error.
func serveMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	addr := fs.String("server", "http://localhost:7070", "factory server")
	graphsDir := fs.String("graphs", "", "folder of graphs that factory_start may run; empty means the graphs built into this program")
	fs.Parse(args)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var files iofs.FS = graphs.FS
	if *graphsDir != "" {
		files = os.DirFS(*graphsDir)
	}
	return mcp.Server{API: *addr, Graphs: files}.Serve(ctx, os.Stdin, os.Stdout)
}

func validate(paths []string) error {
	if len(paths) == 0 {
		return errors.New("validate needs at least one graph file")
	}
	var errs []error
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil {
			_, err = graph.Parse(data)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s:\n%w", p, err))
			continue
		}
		fmt.Println(p, "ok")
	}
	return errors.Join(errs...)
}

// writeStandards prints the standards block, or writes it into a file: in
// place of the old block if the file has one, or as the whole file if it
// does not exist yet.
func writeStandards(args []string) error {
	fs := flag.NewFlagSet("standards", flag.ExitOnError)
	rails := fs.String("guardrails", "guardrails.yaml", "settings file with the target Go version")
	out := fs.String("o", "", "file to write; stdout if empty")
	fs.Parse(args)
	g, err := guardrails.Load(*rails)
	if err != nil {
		return err
	}
	block, err := standards.Render(g.Go.Version)
	if err != nil {
		return err
	}
	if *out == "" {
		fmt.Print(block)
		return nil
	}
	doc, err := os.ReadFile(*out)
	if errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(*out, []byte(block), 0o644)
	}
	if err != nil {
		return err
	}
	if doc, err = standards.Replace(doc, block); err != nil {
		return fmt.Errorf("%s: %w", *out, err)
	}
	return os.WriteFile(*out, doc, 0o644)
}
