// Package render writes kept log entries in an output format.
package render

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"time"

	"github.com/brokentari/galvantula/evals/logq/reference/internal/parse"
)

// Formats lists the output formats.
var Formats = []string{"text", "json", "count"}

type jsonEntry struct {
	Time      string            `json:"time"`
	Level     string            `json:"level"`
	Component string            `json:"component"`
	Fields    map[string]string `json:"fields"`
}

// Entry writes one kept entry in the given format. The count format writes
// nothing here; call Count after the last entry.
func Entry(w io.Writer, format string, e parse.Entry) error {
	switch format {
	case "text":
		_, err := fmt.Fprintln(w, e.Raw)
		return err
	case "json":
		b, err := json.Marshal(jsonEntry{e.Time.Format(time.RFC3339Nano), e.Level.String(), e.Component, e.Fields}, json.Deterministic(true))
		if err != nil {
			return err
		}
		_, err = w.Write(append(b, '\n'))
		return err
	}
	return nil
}

// Count writes the number of kept lines, such as "3 lines".
func Count(w io.Writer, n int) error {
	_, err := fmt.Fprintf(w, "%d lines\n", n)
	return err
}
