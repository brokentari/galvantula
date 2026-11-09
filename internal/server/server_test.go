package server

import (
	"bufio"
	"bytes"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brokentari/galvantula/internal/event"
	"github.com/brokentari/galvantula/internal/guardrails"
	"github.com/brokentari/galvantula/internal/runner"
)

// readEvents reads SSE events from the stream until stop returns true.
func readEvents(t *testing.T, resp *http.Response, stop func(event.Event) bool) []event.Event {
	t.Helper()
	var out []event.Event
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var e event.Event
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
		if stop(e) {
			return out
		}
	}
	t.Fatalf("stream ended early: %v", sc.Err())
	return nil
}

func TestStreamAndDecide(t *testing.T) {
	store, err := event.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r := runner.New(t.Context(), store, 3)
	srv := httptest.NewServer(New(store, r, guardrails.Guardrails{}, ""))
	defer srv.Close()

	body, _ := json.Marshal(StartRequest{
		Graph:      "name: g\nnodes:\n  - {id: a, kind: tool}\n  - {id: ok, kind: gate, deps: [a]}\n  - {id: b, kind: tool, deps: [ok]}",
		Executor:   "fake",
		FakeScript: "default: {duration: 1ms}",
	})
	resp, err := http.Post(srv.URL+"/api/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID string `json:"id"`
	}
	json.UnmarshalRead(resp.Body, &created)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("start: %s", resp.Status)
	}

	stream, err := http.Get(srv.URL + "/api/runs/" + created.ID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	evs := readEvents(t, stream, func(e event.Event) bool { return e.Type == event.GateWaiting })
	stream.Body.Close()
	if evs[0].Type != event.RunCreated {
		t.Errorf("first event is %s, want run_created", evs[0].Type)
	}
	lastSeen := evs[len(evs)-1].Seq

	resp, err = http.Post(srv.URL+"/api/runs/"+created.ID+"/gates/ok", "application/json", strings.NewReader(`{"approved": true}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("decide: %s", resp.Status)
	}

	// Reconnect as a browser would after a dropped connection.
	req, _ := http.NewRequest("GET", srv.URL+"/api/runs/"+created.ID+"/events", nil)
	req.Header.Set("Last-Event-ID", strconv.FormatInt(lastSeen, 10))
	stream, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	evs = readEvents(t, stream, func(e event.Event) bool { return e.Type == event.RunFinished })
	if evs[0].Seq != lastSeen+1 || evs[0].Type != event.GateDecided {
		t.Errorf("after reconnect the first event is %d (%s), want %d (gate_decided)", evs[0].Seq, evs[0].Type, lastSeen+1)
	}

	resp, err = http.Get(srv.URL + "/api/runs/nope/events")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown run: %s, want 404", resp.Status)
	}
}

func TestRejectsUnsafeRequests(t *testing.T) {
	store, err := event.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := httptest.NewServer(New(store, runner.New(t.Context(), store, 1), guardrails.Guardrails{}, ""))
	defer srv.Close()

	tests := []struct {
		name, contentType, body string
		want                    int
	}{
		// A web page can send text/plain to localhost with no preflight.
		{"text/plain from a web page", "text/plain", `{"graph": "name: g"}`, http.StatusUnsupportedMediaType},
		{"body over the limit", "application/json", `{"graph": "` + strings.Repeat("x", maxBody) + `"}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Post(srv.URL+"/api/runs", tt.contentType, strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tt.want {
				t.Errorf("got %s, want %d", resp.Status, tt.want)
			}
		})
	}

	// DNS rebinding: a page on another domain that resolves to 127.0.0.1.
	req, _ := http.NewRequest("GET", srv.URL+"/api/runs", nil)
	req.Host = "evil.example:7070"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("request with Host evil.example: got %s, want 403", resp.Status)
	}

	if runs, _ := store.Runs(); len(runs) != 0 {
		t.Errorf("a refused request started %d runs", len(runs))
	}
}

func getJSON(t *testing.T, url string, v any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		if err := json.UnmarshalRead(resp.Body, v); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

// The state and logs endpoints give a client the run, with what the waiting gate needs to show.
func TestStateAndLogs(t *testing.T) {
	store, err := event.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r := runner.New(t.Context(), store, 3)
	srv := httptest.NewServer(New(store, r, guardrails.Guardrails{}, ""))
	defer srv.Close()
	body, _ := json.Marshal(StartRequest{
		Graph:      "name: g\nnodes:\n  - {id: a, title: Write it, kind: tool}\n  - {id: ok, kind: gate, deps: [a]}\n  - {id: b, kind: tool, deps: [ok]}",
		Executor:   "fake",
		FakeScript: "default: {duration: 1ms}\nnodes:\n  a: {logs: [first line, second line]}",
	})
	resp, err := http.Post(srv.URL+"/api/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID string `json:"id"`
	}
	json.UnmarshalRead(resp.Body, &created)
	resp.Body.Close()

	var snap Snapshot
	for range 500 {
		getJSON(t, srv.URL+"/api/runs/"+created.ID+"/state", &snap)
		if len(snap.Gates) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(snap.Gates) != 1 || snap.Gates[0].Node != "ok" || strings.Join(snap.Gates[0].Context, "|") != "first line|second line" {
		t.Fatalf("snapshot gates: %+v", snap.Gates)
	}
	if snap.Graph != "g" || snap.Status != event.RunRunning || len(snap.Nodes) != 3 || snap.Nodes[0].Title != "Write it" {
		t.Errorf("snapshot: %+v", snap)
	}

	var logs map[string][]string
	getJSON(t, srv.URL+"/api/runs/"+created.ID+"/logs?node=a&tail=1", &logs)
	if len(logs["lines"]) != 1 || logs["lines"][0] != "second line" {
		t.Errorf("logs of a, tail 1: %v", logs)
	}
	getJSON(t, srv.URL+"/api/runs/"+created.ID+"/logs", &logs)
	if len(logs["lines"]) != 2 || !strings.HasPrefix(logs["lines"][0], "a: ") {
		t.Errorf("logs of every node: %v", logs)
	}
	if code := getJSON(t, srv.URL+"/api/runs/"+created.ID+"/logs?tail=0", &logs); code != http.StatusBadRequest {
		t.Errorf("tail=0: %d, want 400", code)
	}
	if code := getJSON(t, srv.URL+"/api/runs/nope/state", &snap); code != http.StatusNotFound {
		t.Errorf("unknown run: %d, want 404", code)
	}
}

// After a gate sends work back, its context shows the new attempt only.
func TestGateContextIsTheLatestAttempt(t *testing.T) {
	line := func(node, text string) event.Event {
		return event.New("r", event.NodeLog, node, 1, event.NodeLogData{Line: text})
	}
	evs := []event.Event{
		event.New("r", event.NodeStarted, "a", 1, nil), line("a", "old contract"),
		event.New("r", event.NodeStarted, "a", 2, nil), line("a", "new contract"), line("other", "not a dependency"),
	}
	if got := latestLines(evs, []string{"a"}, 10); strings.Join(got, "|") != "new contract" {
		t.Errorf("context %v, want only the second attempt", got)
	}
	if got := logLines(evs, []string{"a"}, 10); len(got) != 2 {
		t.Errorf("the logs endpoint must keep every attempt: %v", got)
	}
}
