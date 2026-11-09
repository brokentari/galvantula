package mcp

import (
	"bytes"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// fakeFactory answers the API calls the tools make, and keeps what it was sent.
type fakeFactory struct {
	posts map[string]string // path -> body
}

func (f *fakeFactory) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		body, _ := io.ReadAll(r.Body)
		f.posts[r.URL.Path] = string(body)
		if r.URL.Path == "/api/runs" {
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"id":"r-new"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/gates/gone") {
			http.Error(w, "node gone is not a waiting gate", http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	switch {
	case r.URL.Path == "/api/runs":
		io.WriteString(w, `[{"id":"r-1","graph":"phase4","status":"running"}]`)
	case r.URL.Path == "/api/runs/r-1/state":
		io.WriteString(w, `{"id":"r-1","graph":"phase4","status":"running","cost_usd":1.5,"graph_version":2,"replans":1,
			"nodes":[{"id":"architect","kind":"agent","status":"succeeded"},{"id":"a","kind":"agent","status":"failed","attempt":3,"last_error":"boom\nmore"}],
			"gates":[{"node":"approve-contract","title":"Approve the contract","context":["# Contract","two packages"]}]}`)
	case r.URL.Path == "/api/runs/r-1/logs":
		io.WriteString(w, `{"lines":["x: one","x: two"]}`)
		f.posts["logs query"] = r.URL.RawQuery
	default:
		http.NotFound(w, r)
	}
}

// talk sends each line to a server and returns its answers, one per request that has an id.
func talk(t *testing.T, s Server, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(t.Context(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var answers []map[string]any
	for line := range strings.Lines(out.String()) {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("answer %q: %v", line, err)
		}
		answers = append(answers, m)
	}
	return answers
}

func call(id int, name, args string) string {
	return `{"jsonrpc":"2.0","id":` + string(rune('0'+id)) + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

// text returns the tool's text and whether it is an error.
func text(t *testing.T, answer map[string]any) (string, bool) {
	t.Helper()
	res, ok := answer["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", answer)
	}
	content := res["content"].([]any)[0].(map[string]any)
	return content["text"].(string), res["isError"] == true
}

func TestProtocol(t *testing.T) {
	s := Server{API: "http://127.0.0.1:1"}
	got := talk(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"nope"}`,
		`not json`,
	)
	if len(got) != 4 { // the notification gets no answer
		t.Fatalf("%d answers, want 4: %v", len(got), got)
	}
	if v := got[0]["result"].(map[string]any)["protocolVersion"]; v != "2025-03-26" {
		t.Errorf("protocol version %v, want the client's", v)
	}
	var names []string
	for _, tl := range got[1]["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	if strings.Join(names, " ") != "factory_start factory_runs factory_status factory_logs factory_approve" {
		t.Errorf("tools: %v", names)
	}
	if code := got[2]["error"].(map[string]any)["code"]; code != -32601.0 {
		t.Errorf("unknown method: %v", got[2])
	}
	if code := got[3]["error"].(map[string]any)["code"]; code != -32700.0 {
		t.Errorf("bad json: %v", got[3])
	}
}

func TestToolsTalkToTheFactory(t *testing.T) {
	f := &fakeFactory{posts: map[string]string{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	s := Server{API: srv.URL, Graphs: fstest.MapFS{"phase4.yaml": {Data: []byte("name: phase4\n")}}}

	got := talk(t, s,
		call(1, "factory_status", `{}`),
		call(2, "factory_logs", `{"node":"x","tail":2}`),
		call(3, "factory_approve", `{"node":"approve-contract","approved":false,"notes":"split it"}`),
		call(4, "factory_start", `{"spec":"build a thing"}`),
		call(5, "factory_runs", `{}`),
	)
	status, isErr := text(t, got[0])
	for _, want := range []string{"Run r-1", "$1.50 spent, 1 replans", "a                  agent  failed (attempt 3): boom", "WAITING FOR A DECISION", "| two packages", `node "approve-contract"`} {
		if !strings.Contains(status, want) {
			t.Errorf("status lacks %q:\n%s", want, status)
		}
	}
	if isErr || strings.Contains(status, "more") {
		t.Errorf("status error %t, or it shows more than the first error line:\n%s", isErr, status)
	}
	if logs, _ := text(t, got[1]); logs != "x: one\nx: two" || f.posts["logs query"] != "node=x&tail=2" {
		t.Errorf("logs %q, query %q", logs, f.posts["logs query"])
	}
	if body := f.posts["/api/runs/r-1/gates/approve-contract"]; !strings.Contains(body, `"approved":false`) || !strings.Contains(body, "split it") {
		t.Errorf("approve sent %q", body)
	}
	if start, _ := text(t, got[3]); !strings.Contains(start, "Started run r-new") || !strings.Contains(f.posts["/api/runs"], `"executor":"claude"`) || !strings.Contains(f.posts["/api/runs"], "name: phase4") {
		t.Errorf("start: %q, sent %q", start, f.posts["/api/runs"])
	}
	if runs, _ := text(t, got[4]); !strings.Contains(runs, "r-1  phase4  running") {
		t.Errorf("runs: %q", runs)
	}
}

func TestToolErrorsReachTheModel(t *testing.T) {
	f := &fakeFactory{posts: map[string]string{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	s := Server{API: srv.URL, Graphs: fstest.MapFS{}}
	for name, c := range map[string]struct{ call, want string }{
		"graph path":     {call(1, "factory_start", `{"spec":"x","graph":"../secret"}`), "must be lower case"},
		"missing graph":  {call(1, "factory_start", `{"spec":"x","graph":"nope"}`), `no graph "nope"`},
		"empty spec":     {call(1, "factory_start", `{"spec":" "}`), "spec is empty"},
		"missing fields": {call(1, "factory_approve", `{"node":"g"}`), "required"},
		"not a gate":     {call(1, "factory_approve", `{"run":"r-1","node":"gone","approved":true}`), "not a waiting gate"},
		"unknown run":    {call(1, "factory_status", `{"run":"zzz"}`), "404"},
		"server down":    {"", ""},
	} {
		if c.call == "" {
			down := Server{API: "http://127.0.0.1:1"}
			if msg, isErr := text(t, talk(t, down, call(1, "factory_status", `{"run":"r-1"}`))[0]); !isErr || !strings.Contains(msg, "factory serve") {
				t.Errorf("%s: %q", name, msg)
			}
			continue
		}
		msg, isErr := text(t, talk(t, s, c.call)[0])
		if !isErr || !strings.Contains(msg, c.want) {
			t.Errorf("%s: error %t, %q; want one with %q", name, isErr, msg, c.want)
		}
	}
	if got := talk(t, s, call(1, "nope", `{}`))[0]; got["error"] == nil {
		t.Errorf("unknown tool: %v", got)
	}
}
