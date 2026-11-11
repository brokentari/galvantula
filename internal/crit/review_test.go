package crit

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/brokentari/galvantula/internal/event"
)

func TestRead(t *testing.T) {
	got, notes, err := Read("testdata/review.json", 12)
	if err != nil {
		t.Fatal(err)
	}
	want := []event.GateComment{
		{Line: 5, Body: "Return an empty list instead."},
		{Line: 10, EndLine: 12, Body: "Add a test for a missing key."},
		{Line: 7, Body: `On "line 7 text": Say what this returns.`},
	}
	if !slices.Equal(got, want) {
		t.Errorf("comments %+v, want %+v", got, want)
	}
	if want := "The file is short. Good.\n\nLine 400: This line is gone.\n\nOverall: close."; notes != want {
		t.Errorf("notes %q, want %q", notes, want)
	}
}

func TestReadEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "review.json")
	os.WriteFile(p, []byte(`{"branch":"x","files":null}`), 0o644)
	if c, n, err := Read(p, 12); err != nil || c != nil || n != "" {
		t.Errorf("got %v %q %v", c, n, err)
	}
	if c, n, err := Read("", 12); err != nil || c != nil || n != "" {
		t.Errorf("no file: got %v %q %v", c, n, err)
	}
	os.WriteFile(p, []byte(`{`), 0o644)
	if _, _, err := Read(p, 12); err == nil {
		t.Error("a broken file gave no error")
	}
}
