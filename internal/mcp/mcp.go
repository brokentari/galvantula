// Package mcp is a Model Context Protocol server on standard input and
// output. It lets Claude Code run the factory: start a run, read its status
// and logs, and decide at the gates. It holds no state. Every tool call
// goes to the factory server's HTTP API, so the runs and their gates are the
// ones the UI shows.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Server answers MCP requests by calling the factory server.
type Server struct {
	// API is the factory server's address, such as http://localhost:7070.
	API string
	// Graphs holds the graph files that factory_start may name, as name.yaml.
	Graphs fs.FS
	// HTTP is the client for the API. Nil means a client with a 30 second timeout.
	HTTP *http.Client
}

// versions are the protocol versions this server speaks, newest first.
var versions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type request struct {
	ID     jsontext.Value `json:"id,omitzero"`
	Method string         `json:"method"`
	Params jsontext.Value `json:"params,omitzero"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      jsontext.Value `json:"id"`
	Result  any            `json:"result,omitempty"`
	Error   *rpcError      `json:"error,omitempty"`
}

// Serve reads one JSON message per line from in and writes the answers to
// out, until in ends.
func (s Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(nil, 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			if err := reply(out, response{ID: jsontext.Value("null"), Error: &rpcError{-32700, "parse error: " + err.Error()}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue // a notification, such as notifications/initialized, has no answer
		}
		res := response{ID: req.ID}
		result, err := s.handle(ctx, req)
		if rpc, ok := errors.AsType[*rpcError](err); ok {
			res.Error = rpc
		} else if err != nil {
			res.Error = &rpcError{-32603, err.Error()}
		} else {
			res.Result = result
		}
		if err := reply(out, res); err != nil {
			return err
		}
	}
	return sc.Err()
}

// unmarshal reads JSON into v. A tool's arguments and the server's answers use
// lower case names, and the structs here use Go names, so names match in any case.
func unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v, json.MatchCaseInsensitiveNames(true))
}

func (e *rpcError) Error() string { return e.Message }

func reply(out io.Writer, r response) error {
	r.JSONRPC = "2.0"
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = out.Write(append(data, '\n'))
	return err
}

func (s Server) handle(ctx context.Context, req request) (any, error) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		unmarshal(req.Params, &p)
		version := versions[0]
		for _, v := range versions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "factory", "version": "1"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments jsontext.Value `json:"arguments"`
		}
		if err := unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "bad params: " + err.Error()}
		}
		call, ok := s.calls()[p.Name]
		if !ok {
			return nil, &rpcError{-32602, "unknown tool " + p.Name}
		}
		// A failed tool is a result with isError, so the model reads the reason.
		text, err := call(ctx, p.Arguments)
		if err != nil {
			text = "error: " + err.Error()
		}
		return map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": err != nil}, nil
	}
	return nil, &rpcError{-32601, "method not found: " + req.Method}
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func schema(required []string, props map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

var (
	str  = func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	flag = func(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
	runP = str("The run id, such as r-abc123. Leave it out for the newest run.")
)

var tools = []tool{
	{"factory_start", "Start a factory run that builds a Go program from a spec. The run stops at two gates for a person to approve: the contract, then the task graph. Returns the run id.",
		schema([]string{"spec"}, map[string]any{
			"spec":         str("The spec, in markdown: what the program must do."),
			"graph":        str("The graph to run, such as phase4 (the default). It is a file in the graphs folder."),
			"auto_approve": flag("Approve both gates without a person. Use it only when the user says to."),
		})},
	{"factory_runs", "List the factory runs, newest first, with each one's status.", schema(nil, map[string]any{})},
	{"factory_status", "Show a run: its status, its cost, each node's state, and every gate that waits for a decision, with what the person must read to decide.",
		schema(nil, map[string]any{"run": runP})},
	{"factory_logs", "Show the last log lines of one node, or of every node.",
		schema(nil, map[string]any{"run": runP, "node": str("The node id. Leave it out for every node."), "tail": map[string]any{"type": "integer", "description": "How many lines, from 1 to 1000. The default is 50."}})},
	{"factory_approve", "Decide a gate that waits. Reject with notes to send the contract back to the architect, as far as the run's revision limit allows.",
		schema([]string{"node", "approved"}, map[string]any{"run": runP, "node": str("The gate's node id, such as approve-contract."), "approved": flag("true to approve, false to reject."), "notes": str("Why. Notes are required to send work back.")})},
}

func (s Server) calls() map[string]func(context.Context, jsontext.Value) (string, error) {
	return map[string]func(context.Context, jsontext.Value) (string, error){
		"factory_start":   s.start,
		"factory_runs":    s.runs,
		"factory_status":  s.status,
		"factory_logs":    s.logs,
		"factory_approve": s.approve,
	}
}

var graphName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func (s Server) start(ctx context.Context, raw jsontext.Value) (string, error) {
	var a struct {
		Spec        string `json:"spec"`
		Graph       string `json:"graph"`
		AutoApprove bool   `json:"auto_approve"`
	}
	if err := unmarshal(raw, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Spec) == "" {
		return "", errors.New("spec is empty")
	}
	if a.Graph == "" {
		a.Graph = "phase4"
	}
	if !graphName.MatchString(a.Graph) { // the name becomes a file name
		return "", fmt.Errorf("graph %q must be lower case letters, digits and hyphens", a.Graph)
	}
	g, err := fs.ReadFile(s.Graphs, a.Graph+".yaml")
	if err != nil {
		return "", fmt.Errorf("no graph %q", a.Graph)
	}
	var out struct {
		ID string `json:"id"`
	}
	body := map[string]any{"graph": string(g), "executor": "claude", "spec": a.Spec, "auto_approve": a.AutoApprove}
	if err := s.call(ctx, http.MethodPost, "/api/runs", body, &out); err != nil {
		return "", err
	}
	return fmt.Sprintf("Started run %s on graph %s. Call factory_status to follow it. It stops at the gate approve-contract.", out.ID, a.Graph), nil
}

func (s Server) runs(ctx context.Context, _ jsontext.Value) (string, error) {
	var runs []struct{ ID, Graph, Status string }
	if err := s.call(ctx, http.MethodGet, "/api/runs", nil, &runs); err != nil {
		return "", err
	}
	if len(runs) == 0 {
		return "There are no runs.", nil
	}
	var b strings.Builder
	for _, r := range runs {
		fmt.Fprintf(&b, "%s  %s  %s\n", r.ID, r.Graph, r.Status)
	}
	return b.String(), nil
}

// runID returns id, or the newest run's id if id is empty.
func (s Server) runID(ctx context.Context, id string) (string, error) {
	if id != "" {
		return url.PathEscape(id), nil
	}
	var runs []struct{ ID string }
	if err := s.call(ctx, http.MethodGet, "/api/runs", nil, &runs); err != nil {
		return "", err
	}
	if len(runs) == 0 {
		return "", errors.New("there are no runs")
	}
	return url.PathEscape(runs[0].ID), nil
}

type snapshot struct {
	ID           string  `json:"id"`
	Graph        string  `json:"graph"`
	Status       string  `json:"status"`
	Reason       string  `json:"reason"`
	CostUSD      float64 `json:"cost_usd"`
	GraphVersion int     `json:"graph_version"`
	Replans      int     `json:"replans"`
	Revisions    int     `json:"revisions"`
	Nodes        []struct {
		ID, Title, Kind, Status string
		Attempt                 int
		LastError               string `json:"last_error"`
	} `json:"nodes"`
	Gates []struct {
		Node, Title string
		Context     []string
	} `json:"gates"`
}

func (s Server) status(ctx context.Context, raw jsontext.Value) (string, error) {
	var a struct{ Run string }
	if len(raw) > 0 {
		if err := unmarshal(raw, &a); err != nil {
			return "", err
		}
	}
	id, err := s.runID(ctx, a.Run)
	if err != nil {
		return "", err
	}
	var snap snapshot
	if err := s.call(ctx, http.MethodGet, "/api/runs/"+id+"/state", nil, &snap); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Run %s, graph %s (version %d): %s. $%.2f spent, %d replans, %d revisions.\n", snap.ID, snap.Graph, snap.GraphVersion, snap.Status, snap.CostUSD, snap.Replans, snap.Revisions)
	if snap.Reason != "" {
		fmt.Fprintf(&b, "Reason: %s\n", snap.Reason)
	}
	for _, n := range snap.Nodes {
		fmt.Fprintf(&b, "  %-18s %-6s %s", n.ID, n.Kind, n.Status)
		if n.Attempt > 1 {
			fmt.Fprintf(&b, " (attempt %d)", n.Attempt)
		}
		if n.LastError != "" && n.Status == "failed" {
			fmt.Fprintf(&b, ": %s", firstLine(n.LastError))
		}
		b.WriteByte('\n')
	}
	for _, g := range snap.Gates {
		fmt.Fprintf(&b, "\nWAITING FOR A DECISION: gate %s (%s). Show the user this, ask them, then call factory_approve with node %q.\n", g.Node, g.Title, g.Node)
		for _, l := range g.Context {
			fmt.Fprintf(&b, "  | %s\n", l)
		}
	}
	return b.String(), nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func (s Server) logs(ctx context.Context, raw jsontext.Value) (string, error) {
	var a struct {
		Run  string `json:"run"`
		Node string `json:"node"`
		Tail int    `json:"tail"`
	}
	if len(raw) > 0 {
		if err := unmarshal(raw, &a); err != nil {
			return "", err
		}
	}
	id, err := s.runID(ctx, a.Run)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	if a.Node != "" {
		q.Set("node", a.Node)
	}
	if a.Tail != 0 {
		q.Set("tail", fmt.Sprint(a.Tail))
	}
	var out struct{ Lines []string }
	if err := s.call(ctx, http.MethodGet, "/api/runs/"+id+"/logs?"+q.Encode(), nil, &out); err != nil {
		return "", err
	}
	if len(out.Lines) == 0 {
		return "No log lines yet.", nil
	}
	return strings.Join(out.Lines, "\n"), nil
}

func (s Server) approve(ctx context.Context, raw jsontext.Value) (string, error) {
	var a struct {
		Run      string `json:"run"`
		Node     string `json:"node"`
		Approved *bool  `json:"approved"`
		Notes    string `json:"notes"`
	}
	if err := unmarshal(raw, &a); err != nil {
		return "", err
	}
	if a.Node == "" || a.Approved == nil {
		return "", errors.New("node and approved are required")
	}
	id, err := s.runID(ctx, a.Run)
	if err != nil {
		return "", err
	}
	body := map[string]any{"approved": *a.Approved, "notes": a.Notes}
	if err := s.call(ctx, http.MethodPost, "/api/runs/"+id+"/gates/"+url.PathEscape(a.Node), body, nil); err != nil {
		return "", err
	}
	if *a.Approved {
		return fmt.Sprintf("Approved %s. Call factory_status to follow the run.", a.Node), nil
	}
	return fmt.Sprintf("Rejected %s. If the run has revisions left and you gave notes, the architect starts again; call factory_status to see.", a.Node), nil
}

// call sends one request to the factory server and decodes its JSON answer into out, if out is not nil.
func (s Server) call(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.API, "/")+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("the factory server is not reachable at %s; start it with: factory serve (%w)", s.API, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("the factory server answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.UnmarshalRead(resp.Body, out, json.MatchCaseInsensitiveNames(true))
}
