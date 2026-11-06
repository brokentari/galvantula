package event

import (
	"strings"
	"testing"
)

// A stray byte in tool output must not cost the event its data.
func TestNewKeepsDataWithInvalidUTF8(t *testing.T) {
	e := New("r", NodeLog, "a", 1, NodeLogData{Line: "ok \xff done"})
	d, err := e.Decode[NodeLogData]()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(d.Line, "ok ") || !strings.HasSuffix(d.Line, " done") {
		t.Errorf("line = %q, want the text around the bad byte kept", d.Line)
	}
}
