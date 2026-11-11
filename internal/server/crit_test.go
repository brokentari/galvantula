package server

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brokentari/galvantula/internal/crit"
	"github.com/brokentari/galvantula/internal/event"
	"github.com/brokentari/galvantula/internal/guardrails"
	"github.com/brokentari/galvantula/internal/runner"
)

const testContract = "# C\n\n## Unclear in the spec\n\n- one\n- two\n"

type fixture struct {
	srv    *httptest.Server
	store  *event.Store
	root   string
	run    string
	stops  string // the stand-in appends a line here for each "crit stop"
	round1 string
}

// standIn puts a fake crit first on PATH and returns the sessions that find it.
func standIn(t *testing.T, addrLine string) (*crit.Sessions, string) {
	dir := t.TempDir()
	stops := filepath.Join(dir, "stops")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = stop ]; then echo "$*" >> %q; exit 0; fi
echo "$*" > args; pwd > pwd; env > env
echo %q >&2
exec sleep 60
`, stops, addrLine)
	if err := os.WriteFile(filepath.Join(dir, "crit"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	s := crit.Find()
	t.Cleanup(s.StopAll)
	return s, stops
}

// newFixture starts a run that waits at a gate after a node with a contract.
func newFixture(t *testing.T, s *crit.Sessions) *fixture {
	t.Helper()
	store, err := event.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	r := runner.New(t.Context(), store, 3)
	r.Root = t.TempDir()
	f := &fixture{store: store, root: r.Root}
	f.srv = httptest.NewServer(New(store, r, guardrails.Guardrails{}, "", s))
	t.Cleanup(f.srv.Close)
	body, _ := json.Marshal(StartRequest{
		Graph:      "name: g\nnodes:\n  - {id: a, kind: agent}\n  - {id: ok, kind: gate, deps: [a]}\n  - {id: z, kind: gate, deps: [ok]}",
		Executor:   "fake",
		FakeScript: "default: {duration: 1ms}\nnodes:\n  a:\n    tests: 2\n    red: 2\n    contract: " + fmt.Sprintf("%q", testContract) + "\n",
	})
	resp, err := http.Post(f.srv.URL+"/api/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if resp.StatusCode != http.StatusCreated {
		var b bytes.Buffer
		b.ReadFrom(resp.Body)
		t.Fatalf("start: %s %s", resp.Status, b.String())
	}
	json.UnmarshalRead(resp.Body, &created)
	resp.Body.Close()
	f.run = created.ID
	var snap Snapshot
	for range 500 {
		getJSON(t, f.url("/state"), &snap)
		if len(snap.Gates) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(snap.Gates) != 1 || snap.Gates[0].Brief == nil || snap.Gates[0].Brief.Contract == nil {
		t.Fatalf("the gate does not wait with a brief: %+v", snap.Gates)
	}
	f.round1 = filepath.Join(f.root, f.run, "crit", "ok", "round-1")
	return f
}

func (f *fixture) url(path string) string { return f.srv.URL + "/api/runs/" + f.run + path }

func (f *fixture) post(t *testing.T, path, contentType, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(f.url(path), contentType, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String()
}

func TestNoCritNoRoutes(t *testing.T) {
	f := newFixture(t, nil)
	if code := getJSON(t, f.url("/gates/ok/crit"), &CritStatus{}); code != http.StatusNotFound {
		t.Errorf("GET: %d, want 404", code)
	}
	if code, _ := f.post(t, "/gates/ok/crit", "application/json", "{}"); code != http.StatusNotFound {
		t.Errorf("POST: %d, want 404", code)
	}
	var snap Snapshot
	getJSON(t, f.url("/state"), &snap)
	if b := snap.Gates[0].Brief; b.Round != 1 || len(b.Contract.Lines()) != 6 {
		t.Errorf("brief in the state: %+v", b)
	}
}

func TestCritOpenAndStop(t *testing.T) {
	s, stops := standIn(t, "Started crit daemon at http://localhost:1 (session abc, PID 1)")
	t.Setenv("SECRET_TEST_VAR", "leak")
	f := newFixture(t, s)

	var st CritStatus
	if code := getJSON(t, f.url("/gates/ok/crit"), &st); code != http.StatusOK || st.URL != "" {
		t.Errorf("GET before open: %d %+v", code, st)
	}
	if code := getJSON(t, f.url("/gates/z/crit"), &st); code != http.StatusConflict {
		t.Errorf("a gate that does not wait: %d, want 409", code)
	}
	if code := getJSON(t, f.url("/gates/a/crit"), &st); code != http.StatusConflict {
		t.Errorf("a node that is not a gate: %d, want 409", code)
	}
	if code := getJSON(t, f.srv.URL+"/api/runs/nope/gates/ok/crit", &st); code != http.StatusNotFound {
		t.Errorf("no run: %d, want 404", code)
	}

	// Each of these must be refused before anything starts.
	for _, tt := range []struct {
		name, path, ct, body string
		host                 string
		want                 int
	}{
		{"query", "/gates/ok/crit?file=x", "application/json", "{}", "", 400},
		{"member", "/gates/ok/crit", "application/json", `{"file":"/etc/passwd"}`, "", 400},
		{"text/plain", "/gates/ok/crit", "text/plain", "{}", "", 415},
		{"host", "/gates/ok/crit", "application/json", "{}", "evil.example", 403},
	} {
		req, _ := http.NewRequest("POST", f.url(tt.path), strings.NewReader(tt.body))
		req.Header.Set("Content-Type", tt.ct)
		if tt.host != "" {
			req.Host = tt.host
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tt.want {
			t.Errorf("%s: %d, want %d", tt.name, resp.StatusCode, tt.want)
		}
	}
	if _, err := os.Stat(f.round1); err == nil {
		t.Fatal("a refused request made the round folder")
	}

	for range 2 { // the second call returns the same address
		code, body := f.post(t, "/gates/ok/crit", "application/json", "{}")
		st = CritStatus{}
		json.Unmarshal([]byte(body), &st)
		if code != http.StatusOK || st.URL != "http://localhost:1" {
			t.Fatalf("POST: %d %s", code, body)
		}
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(f.round1, name))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	if got := read("args"); got != "--no-open CONTRACT.md" {
		t.Errorf("args %q", got)
	}
	if got := read("pwd"); got != f.round1 {
		t.Errorf("folder %q, want %q", got, f.round1)
	}
	if got := read("CONTRACT.md"); got != strings.TrimSpace(testContract) {
		t.Errorf("CONTRACT.md %q", got)
	}
	env := read("env")
	for _, want := range []string{"HOME=" + filepath.Join(f.round1, "home"), "CRIT_SHARE_URL=", "CRIT_NO_UPDATE_CHECK=1", "CRIT_NO_INTEGRATION_CHECK=1"} {
		if !strings.Contains("\n"+env+"\n", "\n"+want+"\n") {
			t.Errorf("the environment lacks %q", want)
		}
	}
	if strings.Contains(env, "SECRET_TEST_VAR") {
		t.Error("a variable of the server leaked into Crit")
	}
	if getJSON(t, f.url("/gates/ok/crit"), &st); st.URL != "http://localhost:1" {
		t.Errorf("GET after open: %+v", st)
	}

	// Crit's review file shows in the answer.
	review := filepath.Join(f.round1, "home", ".crit", "reviews", "abc")
	os.MkdirAll(review, 0o755)
	data, _ := os.ReadFile("../crit/testdata/review.json")
	os.WriteFile(filepath.Join(review, "review.json"), data, 0o644)
	getJSON(t, f.url("/gates/ok/crit"), &st)
	if len(st.Comments) == 0 || st.Comments[0].Line != 5 {
		t.Errorf("comments %+v", st.Comments)
	}
	if st.Notes == "" {
		t.Error("no notes from the review file")
	}

	// A decision stops Crit.
	if code, body := f.post(t, "/gates/ok", "application/json", `{"approved":true}`); code != http.StatusNoContent {
		t.Fatalf("decide: %d %s", code, body)
	}
	if b, _ := os.ReadFile(stops); !strings.Contains(string(b), "stop --all") {
		t.Errorf("crit stop was not run: %q", b)
	}
	if code := getJSON(t, f.url("/gates/ok/crit"), &st); code != http.StatusConflict {
		t.Errorf("GET after the decision: %d, want 409", code)
	}
	if got := s.Running(filepath.Join(f.root, f.run, "crit", "ok")); got != "" {
		t.Errorf("Crit still runs at %s", got)
	}
}

func TestCritRefusesAddressOffMachine(t *testing.T) {
	s, stops := standIn(t, "Started crit daemon at http://example.com:80 (session abc, PID 1)")
	f := newFixture(t, s)
	code, body := f.post(t, "/gates/ok/crit", "application/json", "{}")
	if code != http.StatusInternalServerError || !strings.Contains(body, "not on this machine") {
		t.Errorf("POST: %d %s", code, body)
	}
	if b, _ := os.ReadFile(stops); !strings.Contains(string(b), "stop --all") {
		t.Error("the refused Crit was not stopped")
	}
	var st CritStatus
	if getJSON(t, f.url("/gates/ok/crit"), &st); st.URL != "" {
		t.Errorf("a refused address was kept: %+v", st)
	}
}

func TestDecideWithComments(t *testing.T) {
	f := newFixture(t, nil)
	if code, body := f.post(t, "/gates/ok", "application/json", `{"approved":false,"notes":"n","comments":[{"line":5,"body":"b"}, {"line":99,"body":"c"}]}`); code != http.StatusUnprocessableEntity || !strings.Contains(body, "past the end") {
		t.Errorf("bad comment: %d %s", code, body)
	}
	if code, _ := f.post(t, "/gates/z", "application/json", `{"approved":true}`); code != http.StatusConflict {
		t.Errorf("gate that does not wait: %d, want 409", code)
	}
	if code, _ := f.post(t, "/gates/ok", "application/json", `{"approved":false,"notes":"`+strings.Repeat("x", maxBody)+`"}`); code != http.StatusBadRequest {
		t.Errorf("body over 1 MB: %d, want 400", code)
	}
	if code, body := f.post(t, "/gates/ok", "application/json", `{"approved":true,"comments":[{"line":5,"body":"b","quote":"forged"}]}`); code != http.StatusNoContent {
		t.Fatalf("decide: %d %s", code, body)
	}
	evs, _ := f.store.Events(f.run, 0)
	for _, e := range evs {
		if e.Type != event.GateDecided {
			continue
		}
		d, err := e.Decode[event.GateDecidedData]()
		if err != nil || len(d.Comments) != 1 || d.Comments[0].Quote != "- one" {
			t.Errorf("decided event: %+v %v", d, err)
		}
		return
	}
	t.Error("no gate_decided event")
}
