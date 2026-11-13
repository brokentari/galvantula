//go:build linux || darwin

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestServeStopsWhenAnotherServerUsesTheDatabase(t *testing.T) {
	db := filepath.Join(t.TempDir(), "events.db")
	lock, err := lockDB(db)
	if err != nil {
		t.Fatal(err)
	}
	err = serve([]string{"-addr", "127.0.0.1:0", "-db", db, "-ui", "", "-guardrails", "../../guardrails.yaml"})
	if err == nil || !strings.Contains(err.Error(), "another factory server uses") {
		t.Fatalf("serve = %v, want it to stop on the held lock", err)
	}
	lock.Close()
	lock, err = lockDB(db)
	if err != nil {
		t.Fatalf("the lock was not released: %v", err)
	}
	lock.Close()
}
