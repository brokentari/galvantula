package main

import (
	"os"
	"testing"
)

// The UI's types must match the Go types. If this fails, run: go generate ./...
func TestGeneratedTypesAreCurrent(t *testing.T) {
	got, err := os.ReadFile("../../ui/src/gen/events.ts")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != generate() {
		t.Fatal("ui/src/gen/events.ts is out of date; run: go generate ./...")
	}
}
