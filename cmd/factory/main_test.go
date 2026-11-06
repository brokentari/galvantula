package main

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/brokentari/galvantula/internal/event"
	"github.com/brokentari/galvantula/internal/graph"
)

// A second server on a busy port must stop before it resumes runs that the
// first server drives; two drivers on one run corrupt its log.
func TestServeStopsBeforeResumeWhenPortIsBusy(t *testing.T) {
	db := filepath.Join(t.TempDir(), "events.db")
	store, err := event.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	g := graph.Graph{Name: "g", Nodes: []graph.Node{{ID: "a", Kind: graph.Tool}}}
	for _, e := range []event.Event{
		event.New("r", event.RunCreated, "", 0, event.RunCreatedData{Graph: g, Executor: "fake"}),
		event.New("r", event.NodeStarted, "a", 1, nil),
	} {
		if _, err := store.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if err := serve([]string{"-addr", busy.Addr().String(), "-db", db, "-ui", ""}); err == nil {
		t.Fatal("serve started on a busy port")
	}

	store, err = event.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if evs, _ := store.Events("r", 0); len(evs) != 2 {
		t.Errorf("the log has %d events, want 2: serve resumed the run before it found the port busy", len(evs))
	}
}
