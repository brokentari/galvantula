// Command factory runs the software factory.
//
//	factory serve    [-addr localhost:7070] [-db factory.db] [-ui ui/dist] [-workers 3]
//	factory start    [-server http://localhost:7070] -fake script.yaml graph.yaml
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
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/brokentari/galvantula/internal/event"
	"github.com/brokentari/galvantula/internal/graph"
	"github.com/brokentari/galvantula/internal/guardrails"
	"github.com/brokentari/galvantula/internal/runner"
	"github.com/brokentari/galvantula/internal/server"
	"github.com/brokentari/galvantula/standards"
)

const usage = `usage:
  factory serve    [-addr localhost:7070] [-db factory.db] [-ui ui/dist] [-workers 3]
  factory start    [-server http://localhost:7070] -fake script.yaml graph.yaml
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
	case "validate":
		err = validate(args)
	case "standards":
		err = writeStandards(args)
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
	uiDir := fs.String("ui", "ui/dist", "built UI to serve; empty to serve only the API")
	workers := fs.Int("workers", 3, "nodes of one run that may execute at the same time")
	fs.Parse(args)

	if _, err := os.Stat(*uiDir); *uiDir != "" && err != nil {
		slog.Warn("no built UI; serving only the API", "dir", *uiDir)
		*uiDir = ""
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
	run := runner.New(ctx, store, *workers)
	if err := run.ResumeAll(); err != nil {
		ln.Close()
		return err
	}

	srv := &http.Server{Handler: server.New(store, run, *uiDir)}
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

func start(args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	addr := fs.String("server", "http://localhost:7070", "factory server")
	fake := fs.String("fake", "", "fake executor script (phase 0 has no other executor)")
	fs.Parse(args)
	if fs.NArg() != 1 || *fake == "" {
		return errors.New("start needs -fake script.yaml and one graph file")
	}
	g, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	script, err := os.ReadFile(*fake)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(server.StartRequest{Graph: string(g), FakeScript: string(script)})
	resp, err := http.Post(*addr+"/api/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("server refused the run (%s): %s", resp.Status, bytes.TrimSpace(out))
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		return err
	}
	fmt.Println(created.ID)
	return nil
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
