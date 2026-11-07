package review

import "testing"

func TestVerdict(t *testing.T) {
	tests := map[string]string{
		"Findings.\n\nVerdict: Ship.\n":                                              "Ship.",
		"Verdict: fix 1 and 3 first.\nLean: -4\n":                                    "fix 1 and 3 first.",
		"**Verdict: Ship.**\n":                                                       "Ship.",
		"What this change does: a thing.\nNo issue.":                                 "no verdict line",
		"What this change does: a thing.\n\nLooks good. Ship.\n\nI checked the API.": "Ship.",
		"Looks good. Ship. But fix 2.\n":                                             "no verdict line",
	}
	for report, want := range tests {
		if got := Verdict(report); got != want {
			t.Errorf("Verdict(%q) = %q, want %q", report, got, want)
		}
	}
}
