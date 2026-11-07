// Package filter decides which log entries to keep.
package filter

import (
	"time"

	"github.com/brokentari/galvantula/evals/logq/reference/internal/parse"
)

// Match is the condition that a field has exactly Value.
type Match struct{ Key, Value string }

// Filter holds the conditions that a kept entry must meet. A zero condition
// matches every entry.
type Filter struct {
	Level     parse.Level
	Component string
	Where     []Match
	Since     time.Time
	Until     time.Time
}

// Keep reports whether e meets every condition in f.
func (f Filter) Keep(e parse.Entry) bool {
	for _, m := range f.Where {
		if v, ok := e.Fields[m.Key]; !ok || v != m.Value {
			return false
		}
	}
	return e.Level >= f.Level &&
		(f.Component == "" || e.Component == f.Component) &&
		(f.Since.IsZero() || !e.Time.Before(f.Since)) &&
		(f.Until.IsZero() || e.Time.Before(f.Until))
}
