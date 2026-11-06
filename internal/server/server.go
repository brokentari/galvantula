// Package server is the factory's HTTP API. The UI reads runs as server-sent
// events; a reconnect with Last-Event-ID and a full replay use the same path.
package server

import (
	"cmp"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/brokentari/galvantula/internal/event"
	"github.com/brokentari/galvantula/internal/graph"
	"github.com/brokentari/galvantula/internal/runner"
)

const (
	// heartbeat keeps idle SSE connections open through proxies.
	heartbeat = 15 * time.Second
	// maxBody caps request bodies. A graph and a fake script are a few KB.
	maxBody = 1 << 20
)

type server struct {
	store  *event.Store
	runner *runner.Runner
}

// StartRequest is the body of POST /api/runs.
type StartRequest struct {
	Graph      string `json:"graph"`       // graph YAML
	FakeScript string `json:"fake_script"` // fake executor script YAML
}

// DecideRequest is the body of POST /api/runs/{run}/gates/{node}.
type DecideRequest struct {
	Approved bool   `json:"approved"`
	Notes    string `json:"notes"`
}

// New returns the API handler. If uiDir is not empty, it also serves the UI from it.
func New(store *event.Store, r *runner.Runner, uiDir string) http.Handler {
	s := &server{store: store, runner: r}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/runs", s.listRuns)
	mux.HandleFunc("POST /api/runs", s.startRun)
	mux.HandleFunc("GET /api/runs/{run}/events", s.streamEvents)
	mux.HandleFunc("POST /api/runs/{run}/gates/{node}", s.decide)
	if uiDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(uiDir)))
	}
	return mux
}

func (s *server) listRuns(w http.ResponseWriter, _ *http.Request) {
	runs, err := s.store.Runs()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *server) startRun(w http.ResponseWriter, r *http.Request) {
	var req StartRequest
	if !readJSON(w, r, &req) {
		return
	}
	g, err := graph.Parse([]byte(req.Graph))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	id, err := s.runner.Start(g, req.FakeScript)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *server) decide(w http.ResponseWriter, r *http.Request) {
	var req DecideRequest
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.runner.Decide(r.PathValue("run"), r.PathValue("node"), req.Approved, req.Notes); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// streamEvents sends the run's events after Last-Event-ID (or ?after=),
// then new events as they happen.
func (s *server) streamEvents(w http.ResponseWriter, r *http.Request) {
	run := r.PathValue("run")
	after, err := strconv.ParseInt(cmp.Or(r.Header.Get("Last-Event-ID"), r.URL.Query().Get("after"), "0"), 10, 64)
	if err != nil {
		http.Error(w, "bad Last-Event-ID or after: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Subscribe before reading the backlog, so no event falls between the two.
	live, cancel := s.store.Subscribe(run)
	defer cancel()
	backlog, err := s.store.Events(run, after)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if after == 0 && len(backlog) == 0 {
		http.Error(w, fmt.Sprintf("no run %q", run), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	rc := http.NewResponseController(w)
	send := func(e event.Event) error {
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Seq, data); err != nil {
			return err
		}
		after = e.Seq
		return rc.Flush()
	}
	for _, e := range backlog {
		if err := send(e); err != nil {
			return
		}
	}
	if err := rc.Flush(); err != nil {
		return
	}

	tick := time.Tick(heartbeat)
	for {
		select {
		case e, ok := <-live:
			if !ok {
				return // dropped for falling behind; the client reconnects
			}
			if e.Seq <= after {
				continue
			}
			if err := send(e); err != nil {
				return
			}
		case <-tick:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// readJSON decodes a JSON body of at most maxBody bytes. It refuses any
// other content type: a web page can send text/plain to localhost without
// asking the browser first, but application/json needs a preflight that
// this server never answers.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}
	if err := json.UnmarshalRead(http.MaxBytesReader(w, r.Body, maxBody), v); err != nil {
		http.Error(w, "bad request body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.MarshalWrite(w, v); err != nil {
		slog.Warn("write response", "err", err)
	}
}
