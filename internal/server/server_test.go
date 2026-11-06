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
