// Package gotest reads the output of go test -json.
package gotest

import (
	"encoding/json/v2"
	"strings"
)

// Count counts the top-level tests that passed and failed, and returns the
// tests' text output. Build errors are not JSON, so it keeps them as text.
func Count(out string) (passed, failed int, text string) {
	var b strings.Builder
	for line := range strings.Lines(out) {
		var e struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
			Output string `json:"Output"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			b.WriteString(line)
			continue
		}
		b.WriteString(e.Output)
		if e.Test == "" || strings.Contains(e.Test, "/") {
			continue
		}
		switch e.Action {
		case "pass":
			passed++
		case "fail":
			failed++
		}
	}
	return passed, failed, b.String()
}
