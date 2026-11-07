package hidden

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/brokentari/galvantula/evals/evalkit"
)

var bin string

func TestMain(m *testing.M) { os.Exit(evalkit.Main(m, "logq", "../reference", &bin)) }

// sample is a small log: every level, four components, quoted values, an
// escaped quote, and a line with no fields.
var sample = []string{
	`2024-03-05T10:00:00Z DEBUG auth user=ana action=check`,
	`2024-03-05T10:05:00Z INFO auth user=ana action=login ms=12`,
	`2024-03-05T10:10:00Z WARN billing user=bo action=charge ms=1200 msg="card is slow"`,
	`2024-03-05T10:15:30Z ERROR auth user=ana action=login ms=120 msg="bad password"`,
	`2024-03-05T10:20:00Z ERROR authz user=bo action=read`,
	`2024-03-05T10:25:00Z INFO billing`,
	`2024-03-05T10:30:00Z WARN web user=cy msg="she said \"hi\" twice"`,
}

// all is the whole sample as log text.
var all = lines(sample...)

// lines joins log lines into text, each line ending in a newline.
func lines(ls ...string) string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l + "\n")
	}
	return b.String()
}

// pick returns the sample lines at the indexes as log text.
func pick(idx ...int) string {
	var ls []string
	for _, i := range idx {
		ls = append(ls, sample[i])
	}
	return lines(ls...)
}

// run runs logq in a fresh directory with stdin and args.
func run(t *testing.T, stdin string, args ...string) evalkit.Result {
	t.Helper()
	return evalkit.Run(t, bin, "", stdin, args...)
}

// writeFile writes a file into dir.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// wantOK checks a run with no bad lines: exit 0, stdout exactly want, and
// no note about bad lines on stderr.
func wantOK(t *testing.T, r evalkit.Result, want string) {
	t.Helper()
	r.Want(t, 0, want)
	if strings.Contains(r.Stderr, "skipped") {
		t.Errorf("stderr %q has a note about bad lines, but no line was bad", r.Stderr)
	}
}

// wantNote checks that stderr has the note about n bad lines.
func wantNote(t *testing.T, r evalkit.Result, n int) {
	t.Helper()
	if want := fmt.Sprintf("skipped %d bad lines", n); !strings.Contains(r.Stderr, want) {
		t.Errorf("stderr %q does not contain %q", r.Stderr, want)
	}
}

func TestTextIsDefault(t *testing.T) {
	wantOK(t, run(t, all), all)
}

func TestTextFormat(t *testing.T) {
	wantOK(t, run(t, all, "--format", "text"), all)
}

// Files are read in argument order, and stdin is ignored when there are files.
func TestFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.log", pick(0, 1, 2))
	writeFile(t, dir, "b.log", pick(3, 4))
	t.Run("in order", func(t *testing.T) {
		wantOK(t, evalkit.Run(t, bin, dir, pick(6), "a.log", "b.log"), pick(0, 1, 2, 3, 4))
	})
	t.Run("reverse order", func(t *testing.T) {
		wantOK(t, evalkit.Run(t, bin, dir, pick(6), "b.log", "a.log"), pick(3, 4, 0, 1, 2))
	})
	t.Run("with filter", func(t *testing.T) {
		wantOK(t, evalkit.Run(t, bin, dir, pick(6), "--level", "WARN", "a.log", "b.log"), pick(2, 3, 4))
	})
}

func TestLevel(t *testing.T) {
	for _, tc := range []struct {
		level string
		want  []int
	}{
		{"DEBUG", []int{0, 1, 2, 3, 4, 5, 6}},
		{"INFO", []int{1, 2, 3, 4, 5, 6}},
		{"WARN", []int{2, 3, 4, 6}},
		{"ERROR", []int{3, 4}},
	} {
		t.Run(tc.level, func(t *testing.T) {
			wantOK(t, run(t, all, "--level", tc.level), pick(tc.want...))
		})
	}
}

func TestComponent(t *testing.T) {
	for _, tc := range []struct {
		component string
		want      []int
	}{
		{"auth", []int{0, 1, 3}},
		{"billing", []int{2, 5}},
		{"web", []int{6}},
	} {
		t.Run(tc.component, func(t *testing.T) {
			wantOK(t, run(t, all, "--component", tc.component), pick(tc.want...))
		})
	}
}

func TestWhere(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []int
	}{
		{"one field", []string{"--where", "user=bo"}, []int{2, 4}},
		{"exact value", []string{"--where", "ms=120"}, []int{3}},
		{"every condition", []string{"--where", "user=ana", "--where", "action=login"}, []int{1, 3}},
		{"quoted value", []string{"--where", "msg=bad password"}, []int{3}},
		{"escaped quote", []string{"--where", `msg=she said "hi" twice`}, []int{6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantOK(t, run(t, all, tc.args...), pick(tc.want...))
		})
	}
}

func TestSinceUntil(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []int
	}{
		{"since keeps its own time", []string{"--since", "2024-03-05T10:15:30Z"}, []int{3, 4, 5, 6}},
		{"since between lines", []string{"--since", "2024-03-05T10:12:00Z"}, []int{3, 4, 5, 6}},
		{"until drops its own time", []string{"--until", "2024-03-05T10:15:30Z"}, []int{0, 1, 2}},
		{"until between lines", []string{"--until", "2024-03-05T10:12:00Z"}, []int{0, 1, 2}},
		{"since and until", []string{"--since", "2024-03-05T10:05:00Z", "--until", "2024-03-05T10:20:00Z"}, []int{1, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantOK(t, run(t, all, tc.args...), pick(tc.want...))
		})
	}
}

func TestFiltersCombine(t *testing.T) {
	r := run(t, all, "--level", "INFO", "--component", "auth", "--where", "action=login",
		"--since", "2024-03-05T10:00:00Z", "--until", "2024-03-05T10:15:30Z")
	wantOK(t, r, pick(1))
}

// entry is one object of --format json output.
type entry struct {
	Time      string            `json:"time"`
	Level     string            `json:"level"`
	Component string            `json:"component"`
	Fields    map[string]string `json:"fields"`
}

// decodeJSON reads the JSON objects in out. Each object must have exactly
// the keys time, level, component and fields; the first three hold strings
// and fields holds an object of strings.
func decodeJSON(t *testing.T, out string) []entry {
	t.Helper()
	var got []entry
	dec := jsontext.NewDecoder(strings.NewReader(out))
	for {
		v, err := dec.ReadValue()
		if errors.Is(err, io.EOF) {
			return got
		}
		if err != nil {
			t.Fatalf("stdout is not a series of JSON values: %v\nstdout:\n%s", err, out)
		}
		var keys map[string]jsontext.Value
		if err := json.Unmarshal(v, &keys); err != nil || keys == nil {
			t.Fatalf("%s is not a JSON object (%v)", v, err)
		}
		if k := slices.Sorted(maps.Keys(keys)); !slices.Equal(k, []string{"component", "fields", "level", "time"}) {
			t.Errorf("%s has keys %q, want component, fields, level and time", v, k)
		}
		var e entry
		if err := json.Unmarshal(v, &e); err != nil {
			t.Errorf("%s: %v (time, level and component must be strings, fields an object of strings)", v, err)
		}
		got = append(got, e)
	}
}

// wantEntries checks a JSON run with no bad lines: exit 0, no note about bad
// lines, and stdout holding exactly the want objects in order.
func wantEntries(t *testing.T, r evalkit.Result, want []entry) {
	t.Helper()
	if r.Code != 0 {
		t.Errorf("exit %d, want 0 (stderr: %s)", r.Code, r.Stderr)
	}
	if strings.Contains(r.Stderr, "skipped") {
		t.Errorf("stderr %q has a note about bad lines, but no line was bad", r.Stderr)
	}
	got := decodeJSON(t, r.Stdout)
	if !slices.EqualFunc(got, want, func(a, b entry) bool {
		return a.Time == b.Time && a.Level == b.Level && a.Component == b.Component && maps.Equal(a.Fields, b.Fields)
	}) {
		t.Errorf("got objects\n%+v\nwant\n%+v\nstdout:\n%s", got, want, r.Stdout)
	}
}

func TestJSON(t *testing.T) {
	wantEntries(t, run(t, all, "--format", "json", "--level", "WARN"), []entry{
		{"2024-03-05T10:10:00Z", "WARN", "billing", map[string]string{"user": "bo", "action": "charge", "ms": "1200", "msg": "card is slow"}},
		{"2024-03-05T10:15:30Z", "ERROR", "auth", map[string]string{"user": "ana", "action": "login", "ms": "120", "msg": "bad password"}},
		{"2024-03-05T10:20:00Z", "ERROR", "authz", map[string]string{"user": "bo", "action": "read"}},
		{"2024-03-05T10:30:00Z", "WARN", "web", map[string]string{"user": "cy", "msg": `she said "hi" twice`}},
	})
}

// A line with no fields gives an empty fields object. The spec leaves open
// whether that is {} or null, and the test accepts both.
func TestJSONNoFields(t *testing.T) {
	wantEntries(t, run(t, pick(5), "--format", "json"), []entry{
		{"2024-03-05T10:25:00Z", "INFO", "billing", nil},
	})
}

func TestCount(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"all", []string{"--format", "count"}, "7 lines\n"},
		{"filtered", []string{"--format", "count", "--level", "ERROR"}, "2 lines\n"},
		{"one", []string{"--format", "count", "--component", "web"}, "1 lines\n"},
		{"none", []string{"--format", "count", "--component", "nobody"}, "0 lines\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantOK(t, run(t, all, tc.args...), tc.want)
		})
	}
}

// With no kept lines, text and json print nothing and exit 0. The note
// about bad lines still goes to stderr.
func TestNoKeptLines(t *testing.T) {
	in := lines(sample[0], "garbage", sample[1], "garbage")
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			r := run(t, in, "--format", format, "--level", "ERROR")
			r.Want(t, 0, "")
			wantNote(t, r, 2)
		})
	}
}

// Each bad line is skipped and counted. Each case has the bad line twice.
func TestBadLines(t *testing.T) {
	for _, tc := range []struct{ name, line string }{
		{"not a log line", "hello world"},
		{"bad timestamp", "2024-03-05 10:00:00 INFO auth user=ana"},
		{"unknown level", "2024-03-05T10:00:00Z TRACE auth user=ana"},
		{"no component", "2024-03-05T10:00:00Z INFO"},
		{"field without equals sign", "2024-03-05T10:00:00Z INFO auth user=ana action"},
		{"unclosed quote", `2024-03-05T10:00:00Z INFO auth msg="bad password`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, lines(sample[0], tc.line, sample[1], tc.line))
			r.Want(t, 0, pick(0, 1))
			wantNote(t, r, 2)
		})
	}
}

func TestBadLinesWithCount(t *testing.T) {
	in := lines(sample[0], "garbage", sample[1], "2024-03-05T10:00:00Z TRACE auth", sample[2], "2024-03-05T10:00:00Z INFO")
	r := run(t, in, "--format", "count")
	r.Want(t, 0, "3 lines\n")
	wantNote(t, r, 3)
}

// One note gives the total over all files.
func TestBadLinesAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.log", lines(sample[0], "garbage"))
	writeFile(t, dir, "b.log", lines("garbage", sample[1], "garbage"))
	r := evalkit.Run(t, bin, dir, "", "a.log", "b.log")
	r.Want(t, 0, pick(0, 1))
	wantNote(t, r, 3)
}

func TestMissingFile(t *testing.T) {
	evalkit.Run(t, bin, t.TempDir(), all, "nope.log").WantFail(t, 1, "nope.log")
}

// The spec leaves open whether lines from a.log print before the error, so
// stdout is not checked.
func TestMissingSecondFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.log", all)
	r := evalkit.Run(t, bin, dir, "", "a.log", "nope.log")
	if r.Code != 1 || !strings.Contains(r.Stderr, "nope.log") {
		t.Errorf("exit %d, stderr %q; want exit 1 and stderr naming nope.log", r.Code, r.Stderr)
	}
}

func TestDirectoryIsUnreadable(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "old.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	evalkit.Run(t, bin, dir, all, "old.log").WantFail(t, 1, "old.log")
}

func TestBadFlagValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown level", []string{"--level", "TRACE"}},
		{"unknown format", []string{"--format", "xml"}},
		{"since does not parse", []string{"--since", "yesterday"}},
		{"until does not parse", []string{"--until", "10am"}},
		{"where without equals sign", []string{"--where", "user"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run(t, all, tc.args...).WantFail(t, 2)
		})
	}
}
