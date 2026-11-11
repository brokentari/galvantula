package crit

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"strings"

	"github.com/brokentari/galvantula/internal/event"
)

type review struct {
	ReviewComments []comment `json:"review_comments"`
	Files          map[string]struct {
		Comments []comment `json:"comments"`
	} `json:"files"`
}

// comment holds the fields of a Crit comment that the adapter reads. json/v2 ignores the rest.
type comment struct {
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Body      string `json:"body"`
	Quote     string `json:"quote"`
	Scope     string `json:"scope"`
	Resolved  bool   `json:"resolved"`
	Drifted   bool   `json:"drifted"`
}

// Read maps a Crit review file to comments on the lines of a contract of
// lines lines, and notes for every comment that names no line of it. A path
// of "" gives nothing.
func Read(path string, lines int) (comments []event.GateComment, notes string, err error) {
	if path == "" {
		return nil, "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var rv review
	if err := json.Unmarshal(data, &rv); err != nil {
		return nil, "", fmt.Errorf("read %s: %w", path, err)
	}
	var parts []string
	for _, c := range rv.Files["CONTRACT.md"].Comments {
		switch {
		case c.Resolved:
		case c.Scope == "file":
			parts = append(parts, c.Body)
		case c.StartLine < 1 || c.StartLine > lines || c.Drifted:
			parts = append(parts, fmt.Sprintf("Line %d: %s", c.StartLine, c.Body))
		default:
			g := event.GateComment{Line: c.StartLine, Body: c.Body}
			if c.EndLine > c.StartLine {
				g.EndLine = min(c.EndLine, lines)
			}
			if c.Quote != "" {
				g.Body = `On "` + c.Quote + `": ` + c.Body
			}
			comments = append(comments, g)
		}
	}
	for _, c := range rv.ReviewComments {
		if !c.Resolved {
			parts = append(parts, c.Body)
		}
	}
	return comments, strings.Join(parts, "\n\n"), nil
}
