package event

import (
	"path/filepath"
	"testing"

	"github.com/brokentari/galvantula/internal/graph"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func appendAll(t *testing.T, s *Store, events ...Event) {
	t.Helper()
	for _, e := range events {
		if _, err := s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	s := openStore(t)
	ch, cancel := s.Subscribe("r")
	defer cancel()
	other, cancelOther := s.Subscribe("other-run")
	defer cancelOther()

	for range subBuffer + 1 { // one more than the reader can hold
		appendAll(t, s, New("r", NodeLog, "a", 1, NodeLogData{Line: "x"}))
	}
	got := 0
	for range ch {
		got++
	}
	if got != subBuffer {
		t.Errorf("read %d events before the channel closed, want %d", got, subBuffer)
	}
	select {
	case e := <-other:
		t.Errorf("a subscriber of another run got event %d", e.Seq)
	default:
	}
}

func TestRuns(t *testing.T) {
	s := openStore(t)
	g := graph.Graph{Name: "demo", Nodes: []graph.Node{{ID: "a", Kind: graph.Tool}}}
	appendAll(t, s,
		New("old", RunCreated, "", 0, RunCreatedData{Graph: g, Executor: "fake"}),
		New("old", RunFinished, "", 0, RunFinishedData{Status: RunSucceeded}),
		New("new", RunCreated, "", 0, RunCreatedData{Graph: g, Executor: "fake"}),
	)
	runs, err := s.Runs()
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != "new" || runs[1].ID != "old" {
		t.Fatalf("got %+v, want runs new then old", runs)
	}
	if runs[0].Status != RunRunning || runs[1].Status != RunSucceeded || runs[0].Graph != "demo" {
		t.Errorf("got %+v", runs)
	}
}
