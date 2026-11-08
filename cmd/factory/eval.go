package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brokentari/galvantula/internal/claude"
	"github.com/brokentari/galvantula/internal/event"
	"github.com/brokentari/galvantula/internal/gotest"
	"github.com/brokentari/galvantula/internal/graph"
	"github.com/brokentari/galvantula/internal/sandbox"
	"github.com/brokentari/galvantula/internal/server"
	"github.com/brokentari/galvantula/standards"
)

// evalResult is one line in evals/results/<date>.jsonl.
type evalResult struct {
	At            time.Time `json:"at"`
	Eval          string    `json:"eval"`
	Run           string    `json:"run"`
	Commit        string    `json:"commit"` // the commit the server started on, with -dirty for uncommitted changes
	Graph         string    `json:"graph"`
	Implementer   string    `json:"implementer"` // model at effort
	ClaudeVersion string    `json:"claude_version"`
	Standards     string    `json:"standards"` // sha256 of standards/vendor/lock.json
	Status        string    `json:"status"`
	Reason        string    `json:"reason,omitempty"`
	HiddenPassed  int       `json:"hidden_passed"`
	HiddenTotal   int       `json:"hidden_total"`
	Pass          bool      `json:"pass"` // the run succeeded and every hidden test passed
	CostUSD       float64   `json:"cost_usd"`
	Minutes       float64   `json:"minutes"`
	Attempts      int       `json:"attempts"`
	Limited       bool      `json:"limited,omitzero"` // stopped by the account's usage limit
	// MaxParallel is the most agent nodes that ran at the same time. Tasks
	// from a plan should make it 2 or more.
	MaxParallel    int `json:"max_parallel,omitzero"`
	MergeConflicts int `json:"merge_conflicts,omitzero"`
	// Planted names the flawed first plan the run started with. Replans is how
	// many times the run replaced a failed task.
	Planted string `json:"planted,omitempty"`
	Replans int    `json:"replans,omitzero"`
}

// parallelism returns the most agent nodes that ran at once, how many
// attempts failed on a merge conflict, and how many agent attempts started,
// from a run's events. It counts attempts of nodes that a replan removed.
func parallelism(evs []event.Event, g graph.Graph) (maxRunning, conflicts, attempts int) {
	kind := map[string]graph.Kind{}
	for _, n := range g.Nodes {
		kind[n.ID] = n.Kind
	}
	running := map[string]bool{}
	for _, e := range evs {
		switch e.Type {
		case event.GraphUpdated:
			if d, err := e.Decode[event.GraphUpdatedData](); err == nil {
				for _, n := range d.Graph.Nodes {
					kind[n.ID] = n.Kind
				}
			}
		case event.NodeStarted:
			if kind[e.Node] == graph.Agent {
				attempts++
				running[e.Node] = true
				maxRunning = max(maxRunning, len(running))
			}
		case event.NodeSucceeded, event.NodeFailed:
			delete(running, e.Node)
			if d, err := e.Decode[event.NodeFailedData](); err == nil && e.Type == event.NodeFailed && strings.Contains(d.Error, "merge conflict") {
				conflicts++
			}
		}
	}
	return maxRunning, conflicts, attempts
}

// runEvals runs each eval n times through the server, scores each product
// with the eval's hidden tests, records the results, and prints a summary.
func runEvals(args []string) error {
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	addr := fs.String("server", "http://localhost:7070", "factory server")
	dbPath := fs.String("db", "factory.db", "the server's event log, read to follow each run")
	runsDir := fs.String("runs", "runs", "the server's runs folder, where the products are")
	n := fs.Int("n", 10, "runs per eval")
	parallel := fs.Int("parallel", 1, "runs at the same time")
	graphPath := fs.String("graph", "graphs/phase1.yaml", "graph to run")
	planted := fs.String("planted", "", "plant the flawed first plan in <eval>/planted/<name>.json, to test replanning")
	fs.Parse(args)
	if fs.NArg() == 0 {
		return errors.New("eval needs at least one eval folder, such as evals/wordfreq")
	}
	// The hidden tests need these from the working directory. Without them, every run is paid for and then cannot be scored.
	for _, p := range []string{"go.mod", filepath.Join("evals", "evalkit")} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("run factory eval from the repo root: %w", err)
		}
	}
	g, err := os.ReadFile(*graphPath)
	if err != nil {
		return err
	}
	parsed, err := graph.Parse(g)
	if err != nil {
		return err
	}
	if *planted != "" && !slices.ContainsFunc(parsed.Nodes, func(n graph.Node) bool { return n.Kind == graph.Plan }) {
		// Only a plan node reads the planted plan. Without one the run would record it and never use it.
		return fmt.Errorf("-planted needs a graph with a plan node, and %s has none; use graphs/phase4.yaml", *graphPath)
	}
	root, err := filepath.Abs(*runsDir)
	if err != nil {
		return err
	}
	store, err := event.Open(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	lock, err := standards.Lock()
	if err != nil {
		return err
	}
	sum := sha256.Sum256(lock)
	out := filepath.Join("evals", "results", time.Now().Format(time.DateOnly)+".jsonl")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}

	type job struct{ dir, name, planted string }
	var jobs []job
	for _, dir := range fs.Args() {
		dir = filepath.Clean(dir)
		var plan []byte
		if *planted != "" {
			// Read it before any run starts, so a missing file costs nothing.
			if plan, err = os.ReadFile(filepath.Join(dir, "planted", *planted+".json")); err != nil {
				return err
			}
		}
		for range *n {
			jobs = append(jobs, job{dir, filepath.Base(dir), string(plan)})
		}
	}
	var (
		mu      sync.Mutex
		results []evalResult
		wg      sync.WaitGroup
		slots   = make(chan struct{}, max(*parallel, 1))
		limited atomic.Bool // the account reached its usage limit: start no more runs
	)
	for _, j := range jobs {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			if limited.Load() {
				return
			}
			r, err := evalOnce(store, *addr, root, j.dir, string(g), j.planted)
			if r.Limited {
				limited.Store(true)
			}
			if err != nil {
				// A run the eval lost still counts, as a failure.
				fmt.Fprintf(os.Stderr, "%s: %v\n", j.name, err)
				r.At, r.Status, r.Reason, r.Pass = cmp.Or(r.At, time.Now()), "error", err.Error(), false
			}
			r.Eval, r.Graph, r.Standards, r.Planted = j.name, parsed.Name, hex.EncodeToString(sum[:8]), *planted
			line, _ := json.Marshal(r)
			mu.Lock()
			defer mu.Unlock()
			results = append(results, r)
			f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err == nil {
				_, err = f.Write(append(line, '\n'))
				err = errors.Join(err, f.Close())
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "record %s: %v\n", r.Run, err)
			}
			fmt.Printf("%-10s %s  pass=%-5v hidden %d/%d  $%.2f  %.1f min  %d attempts\n",
				r.Eval, r.Run, r.Pass, r.HiddenPassed, r.HiddenTotal, r.CostUSD, r.Minutes, r.Attempts)
		})
	}
	wg.Wait()
	if limited.Load() {
		fmt.Println("stopped: the account reached its usage limit; the runs left were not started")
	}
	printSummary(results)
	fmt.Println("results:", out)
	return nil
}

var (
	// pollEvery is how often evalOnce reads the run's events. Tests shorten it.
	pollEvery = 5 * time.Second
	// checkGrace is how long a run may log nothing beyond its node time
	// limit before the eval calls it lost. The check logs nothing while it
	// runs. Tests shorten it.
	checkGrace = 10 * time.Minute
)

// evalOnce starts one run, waits for it, and scores its product.
func evalOnce(store *event.Store, addr, root, dir, g, planted string) (evalResult, error) {
	spec, err := os.ReadFile(filepath.Join(dir, "spec.md"))
	if err != nil {
		return evalResult{}, err
	}
	id, err := startRun(addr, server.StartRequest{Graph: g, Executor: "claude", Spec: string(spec), AutoApprove: true, PlantedPlan: planted})
	if err != nil {
		return evalResult{}, err
	}
	var st *event.State
	var evs []event.Event
	seen, last := 0, time.Now()
	for {
		time.Sleep(pollEvery)
		if evs, err = store.Events(id, 0); err != nil {
			return evalResult{Run: id}, err
		}
		if st, err = event.Fold(evs); err != nil {
			return evalResult{Run: id}, err
		}
		if st.Status != event.RunRunning {
			break
		}
		if len(evs) > seen {
			seen, last = len(evs), time.Now()
		}
		if quiet := time.Since(last); quiet > time.Duration(st.Rails.Budget.NodeMinutes)*time.Minute+checkGrace {
			return evalResult{Run: id, Commit: st.Commit}, fmt.Errorf("run %s logged nothing for %s; it may still be running and spending, so check the server", id, quiet.Round(time.Minute))
		}
	}
	r := evalResult{
		At: evs[0].At, Run: id, Commit: st.Commit, Status: string(st.Status), Reason: st.Reason, CostUSD: st.CostUSD,
		Minutes:       evs[len(evs)-1].At.Sub(evs[0].At).Minutes(),
		Implementer:   st.Rails.Models.Implementer.Model + " at " + st.Rails.Models.Implementer.Effort,
		ClaudeVersion: claude.Version(),
	}
	r.MaxParallel, r.MergeConflicts, r.Attempts = parallelism(evs, graphOf(evs, st))
	r.Replans = st.Replans
	for _, n := range st.Graph.Nodes {
		ns := st.Nodes[n.ID]
		r.Limited = r.Limited || ns.LastClass == event.Limit
	}
	if st.Status != event.RunSucceeded {
		return r, nil
	}
	r.HiddenPassed, r.HiddenTotal, err = hiddenTests(context.Background(), dir, filepath.Join(root, id, "repo"))
	if err != nil {
		r.Reason = err.Error() // a product that does not build fails, and still counts
	}
	r.Pass = err == nil && r.HiddenTotal > 0 && r.HiddenPassed == r.HiddenTotal
	return r, nil
}

// hiddenTests runs an eval's hidden tests against a product, in the sandbox,
// because they build and run the agent's code. The sandbox sees only the parts
// of the repo the tests need: not the reference answers, tokens or other runs.
func hiddenTests(ctx context.Context, dir, product string) (passed, total int, err error) {
	repo, err := os.Getwd()
	if err != nil {
		return 0, 0, err
	}
	modCache, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		return 0, 0, err
	}
	home, err := os.MkdirTemp("", "hidden-home")
	if err != nil {
		return 0, 0, err
	}
	defer os.RemoveAll(home)
	// A new cache for each score: the product runs during scoring and could
	// otherwise plant build results that later scores use.
	cache, err := os.MkdirTemp("", "hidden-build")
	if err != nil {
		return 0, 0, err
	}
	defer os.RemoveAll(cache)
	hidden, err := filepath.Abs(filepath.Join(dir, "hidden"))
	if err != nil {
		return 0, 0, err
	}
	box := sandbox.Spec{
		Dir: hidden,
		RO: []string{product, strings.TrimSpace(string(modCache)), hidden,
			filepath.Join(repo, "go.mod"), filepath.Join(repo, "go.sum"), filepath.Join(repo, "evals", "evalkit")},
		RW:   []string{cache, home},
		Home: home,
		Env: []string{"GOCACHE=" + cache, "GOMODCACHE=" + strings.TrimSpace(string(modCache)), "EVAL_PRODUCT=" + product,
			"GOFLAGS=-buildvcs=false", "GOPROXY=off", "GOTOOLCHAIN=local", "GOTELEMETRY=off"},
	}
	out, _ := sandbox.Command(ctx, box, "go", "test", "-count=1", "-timeout=2m", "-json", ".").Output()
	p, f, text := gotest.Count(string(out))
	if p+f == 0 {
		return 0, 0, fmt.Errorf("no hidden test ran:\n%s", text)
	}
	return p, p + f, nil
}

func printSummary(results []evalResult) {
	by := map[string][]evalResult{}
	for _, r := range results {
		by[r.Eval] = append(by[r.Eval], r)
	}
	fmt.Printf("\n%-10s %8s %12s %12s\n", "eval", "passed", "median $", "median min")
	for _, name := range slices.Sorted(maps.Keys(by)) {
		rs := by[name]
		passed := 0
		var costs, mins []float64
		for _, r := range rs {
			if r.Pass {
				passed++
			}
			costs = append(costs, r.CostUSD)
			mins = append(mins, r.Minutes)
		}
		fmt.Printf("%-10s %5d/%-2d %12.2f %12.1f\n", name, passed, len(rs), median(costs), median(mins))
	}
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	slices.Sort(xs)
	m := len(xs) / 2
	if len(xs)%2 == 0 {
		return (xs[m-1] + xs[m]) / 2
	}
	return xs[m]
}

// graphOf returns the graph as the run started it, before a plan changed it.
func graphOf(evs []event.Event, st *event.State) graph.Graph {
	if d, err := evs[0].Decode[event.RunCreatedData](); err == nil {
		return d.Graph
	}
	return st.Graph
}
