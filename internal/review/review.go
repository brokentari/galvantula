// Package review reads the reports that ponytail-review writes.
package review

import "strings"

// Verdict returns the text after the report's last "Verdict:" line. A
// report with no findings ends with "Looks good. Ship." instead, as
// ponytail-review asks.
func Verdict(report string) string {
	v := "no verdict line"
	for line := range strings.Lines(report) {
		line = strings.Trim(strings.TrimSpace(line), "*")
		if rest, ok := strings.CutPrefix(line, "Verdict:"); ok {
			v = strings.Trim(strings.TrimSpace(rest), "*")
		} else if line == "Looks good. Ship." {
			v = "Ship."
		}
	}
	return strings.TrimSpace(v)
}

// Clean reports whether the report's verdict is Ship.
func Clean(report string) bool { return Verdict(report) == "Ship." }
