package hidden

import (
	"encoding/csv"
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/brokentari/galvantula/evals/evalkit"
)

var bin string

func TestMain(m *testing.M) { os.Exit(evalkit.Main(m, "csvq", "../reference", &bin)) }

// people is the main input. Every age is a number, and text order of the
// ages differs from numeric order. The dept and age columns have ties.
const people = `name,dept,age
alice,eng,30
bob,ops,100
carol,eng,9
dave,sales,25
erin,ops,25
frank,eng,-4.5
`

// mixed has a column with numbers and one value that is not a number.
const mixed = `id,code
a,10
b,9
c,x1
d,100
`

// quoted has fields that need CSV quoting.
const quoted = `name,note
"Smith, Jo","say ""hi"""
lee,plain
`

func run(t *testing.T, stdin string, args ...string) evalkit.Result {
	t.Helper()
	return evalkit.Run(t, bin, "", stdin, args...)
}

func readCSV(s string) ([][]string, error) {
	r := csv.NewReader(strings.NewReader(s))
	r.FieldsPerRecord = -1
	return r.ReadAll()
}

// wantCSV checks for exit 0 and CSV output with the same records as want.
// The spec fixes the records but not the quoting style or line endings, so
// it compares parsed records, not bytes.
func wantCSV(t *testing.T, r evalkit.Result, want string) {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:\n%s\nstderr: %s", r.Code, r.Stdout, r.Stderr)
	}
	got, err := readCSV(r.Stdout)
	if err != nil {
		t.Fatalf("stdout is not CSV: %v\nstdout:\n%s", err, r.Stdout)
	}
	exp, err := readCSV(want)
	if err != nil {
		t.Fatalf("bad want: %v", err)
	}
	if !slices.EqualFunc(got, exp, slices.Equal[[]string]) {
		t.Errorf("stdout:\n%s\nwant:\n%s", r.Stdout, want)
	}
}

// wantJSON checks for exit 0 and a JSON array of objects, with string
// values, equal to want. It does not check key order or whitespace.
func wantJSON(t *testing.T, r evalkit.Result, want []map[string]string) {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:\n%s\nstderr: %s", r.Code, r.Stdout, r.Stderr)
	}
	var got []map[string]string
	if err := json.Unmarshal([]byte(r.Stdout), &got); err != nil {
		t.Fatalf("stdout is not a JSON array of objects with string values: %v\nstdout:\n%s", err, r.Stdout)
	}
	if !slices.EqualFunc(got, want, maps.Equal[map[string]string]) {
		t.Errorf("stdout:\n%s\nwant: %q", r.Stdout, want)
	}
}

// wantEmptyJSON checks for exit 0 and the output [], ignoring whitespace.
func wantEmptyJSON(t *testing.T, r evalkit.Result) {
	t.Helper()
	if r.Code != 0 || strings.Join(strings.Fields(r.Stdout), "") != "[]" {
		t.Errorf("exit %d, stdout %q; want exit 0 and []\nstderr: %s", r.Code, r.Stdout, r.Stderr)
	}
}

// wantFail checks the exit code and that stderr matches each pattern. It
// does not check stdout: the spec does not say if a run that fails may
// already have written rows.
func wantFail(t *testing.T, r evalkit.Result, code int, patterns ...string) {
	t.Helper()
	if r.Code != code {
		t.Errorf("exit %d, want %d\nstderr: %s", r.Code, code, r.Stderr)
	}
	for _, p := range patterns {
		if !regexp.MustCompile(p).MatchString(r.Stderr) {
			t.Errorf("stderr %q does not match %q", r.Stderr, p)
		}
	}
}

// line matches the number n with no digit next to it, so 4 does not match
// in "14" or "40".
func line(n int) string { return fmt.Sprintf(`(^|\D)%d(\D|$)`, n) }

type csvCase struct {
	name  string
	stdin string
	args  []string
	want  string
}

func runCSV(t *testing.T, cases []csvCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantCSV(t, run(t, c.stdin, c.args...), c.want)
		})
	}
}

func TestInput(t *testing.T) {
	runCSV(t, []csvCase{
		{"no flags copies the input", people, nil, people},
		{"format csv is the default", people, []string{"--format", "csv"}, people},
		{"header only", "name,dept,age\n", nil, "name,dept,age\n"},
		{"header only with sort and select", "name,dept,age\n", []string{"--sort", "age", "--select", "age"}, "age\n"},
		{"last line without newline", "name,age\nann,7", nil, "name,age\nann,7\n"},
		{"quoted fields", quoted, nil, quoted},
		{"where matches a quoted value", quoted, []string{"--where", "name=Smith, Jo", "--select", "note"}, "note\n\"say \"\"hi\"\"\"\n"},
	})
}

func TestWhere(t *testing.T) {
	runCSV(t, []csvCase{
		{"equals", people, []string{"--where", "dept=ops"}, `name,dept,age
bob,ops,100
erin,ops,25
`},
		{"not equals", people, []string{"--where", "dept!=eng"}, `name,dept,age
bob,ops,100
dave,sales,25
erin,ops,25
`},
		{"no row matches", people, []string{"--where", "name=zoe"}, "name,dept,age\n"},
		{"case must match", people, []string{"--where", "dept=OPS"}, "name,dept,age\n"},
		{"whole value must match", people, []string{"--where", "dept=op"}, "name,dept,age\n"},
		{"numbers compare as text", people, []string{"--where", "age=25.0"}, "name,dept,age\n"},
		{"every equals condition", people, []string{"--where", "dept=ops", "--where", "age=25"}, `name,dept,age
erin,ops,25
`},
		{"every not-equals condition", people, []string{"--where", "dept!=eng", "--where", "dept!=sales"}, `name,dept,age
bob,ops,100
erin,ops,25
`},
		{"mixed conditions", people, []string{"--where", "dept!=ops", "--where", "age!=30"}, `name,dept,age
carol,eng,9
dave,sales,25
frank,eng,-4.5
`},
	})
}

func TestSort(t *testing.T) {
	runCSV(t, []csvCase{
		{"text ascending keeps ties in input order", people, []string{"--sort", "dept"}, `name,dept,age
alice,eng,30
carol,eng,9
frank,eng,-4.5
bob,ops,100
erin,ops,25
dave,sales,25
`},
		{"text descending keeps ties in input order", people, []string{"--sort", "dept", "--desc"}, `name,dept,age
dave,sales,25
bob,ops,100
erin,ops,25
alice,eng,30
carol,eng,9
frank,eng,-4.5
`},
		{"text descending", people, []string{"--sort", "name", "--desc"}, `name,dept,age
frank,eng,-4.5
erin,ops,25
dave,sales,25
carol,eng,9
bob,ops,100
alice,eng,30
`},
		{"numbers ascending keeps ties in input order", people, []string{"--sort", "age"}, `name,dept,age
frank,eng,-4.5
carol,eng,9
dave,sales,25
erin,ops,25
alice,eng,30
bob,ops,100
`},
		{"numbers descending keeps ties in input order", people, []string{"--sort", "age", "--desc"}, `name,dept,age
bob,ops,100
alice,eng,30
dave,sales,25
erin,ops,25
carol,eng,9
frank,eng,-4.5
`},
		{"one value not a number sorts as text", mixed, []string{"--sort", "code"}, `id,code
a,10
d,100
b,9
c,x1
`},
		{"one value not a number sorts as text descending", mixed, []string{"--sort", "code", "--desc"}, `id,code
c,x1
b,9
d,100
a,10
`},
	})
}

func TestLimit(t *testing.T) {
	runCSV(t, []csvCase{
		{"first rows", people, []string{"--limit", "2"}, `name,dept,age
alice,eng,30
bob,ops,100
`},
		{"zero", people, []string{"--limit", "0"}, "name,dept,age\n"},
		{"more than the rows", people, []string{"--limit", "10"}, people},
	})
}

func TestSelect(t *testing.T) {
	runCSV(t, []csvCase{
		{"named columns in the given order", people, []string{"--select", "age,name"}, `age,name
30,alice
100,bob
9,carol
25,dave
25,erin
-4.5,frank
`},
		{"one column", people, []string{"--select", "dept"}, `dept
eng
ops
eng
sales
ops
eng
`},
		{"all columns", people, []string{"--select", "name,dept,age"}, people},
	})
}

func TestFlagOrder(t *testing.T) {
	const engYoungest = "name\nfrank\ncarol\n"
	runCSV(t, []csvCase{
		{"filter before limit", people, []string{"--where", "dept=ops", "--limit", "1"}, "name,dept,age\nbob,ops,100\n"},
		{"sort before limit", people, []string{"--sort", "age", "--limit", "2"}, `name,dept,age
frank,eng,-4.5
carol,eng,9
`},
		{"filter sort limit select", people, []string{"--where", "dept=eng", "--sort", "age", "--limit", "2", "--select", "name"}, engYoungest},
		{"command-line order does not matter", people, []string{"--select", "name", "--limit", "2", "--sort", "age", "--where", "dept=eng"}, engYoungest},
		{"filter and sort on columns select leaves out", people, []string{"--select", "name", "--where", "dept=ops", "--sort", "age"}, "name\nerin\nbob\n"},
		{"descending limit select", people, []string{"--sort", "age", "--desc", "--limit", "3", "--select", "name,dept"}, `name,dept
bob,ops
alice,eng
dave,sales
`},
	})
}

func TestJSON(t *testing.T) {
	t.Run("selected columns", func(t *testing.T) {
		wantJSON(t, run(t, people, "--format", "json", "--select", "name,age", "--where", "dept=ops"), []map[string]string{
			{"name": "bob", "age": "100"},
			{"name": "erin", "age": "25"},
		})
	})
	t.Run("all columns", func(t *testing.T) {
		wantJSON(t, run(t, people, "--format", "json", "--limit", "2"), []map[string]string{
			{"name": "alice", "dept": "eng", "age": "30"},
			{"name": "bob", "dept": "ops", "age": "100"},
		})
	})
	t.Run("sort limit select", func(t *testing.T) {
		wantJSON(t, run(t, people, "--format", "json", "--sort", "age", "--desc", "--limit", "1", "--select", "dept"), []map[string]string{
			{"dept": "ops"},
		})
	})
	t.Run("quoted fields", func(t *testing.T) {
		wantJSON(t, run(t, quoted, "--format", "json"), []map[string]string{
			{"name": "Smith, Jo", "note": `say "hi"`},
			{"name": "lee", "note": "plain"},
		})
	})
}

func TestJSONNoRows(t *testing.T) {
	cases := []struct {
		name  string
		stdin string
		args  []string
	}{
		{"no row matches", people, []string{"--where", "dept=hr"}},
		{"header only", "name,dept,age\n", nil},
		{"limit zero", people, []string{"--limit", "0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantEmptyJSON(t, run(t, c.stdin, append([]string{"--format", "json"}, c.args...)...))
		})
	}
}

func TestUnknownColumn(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"where equals", []string{"--where", "salary=10"}},
		{"where not equals", []string{"--where", "salary!=10"}},
		{"sort", []string{"--sort", "salary"}},
		{"select", []string{"--select", "name,salary"}},
		{"select with json", []string{"--format", "json", "--select", "salary"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantFail(t, run(t, people, c.args...), 1, "salary")
		})
	}
}

func TestWrongFieldCount(t *testing.T) {
	const tooFew = `name,dept,age
alice,eng,30
bob,ops,100
carol,eng
dave,sales,25
`
	const tooMany = `name,dept,age
alice,eng,30
bob,ops,100
carol,eng,9
dave,sales,25
erin,ops,25,extra
`
	const filteredOut = `name,dept,age
alice,eng,30
bob,ops,100
carol,eng,9
dave,sales
erin,eng,25
`
	cases := []struct {
		name  string
		stdin string
		args  []string
		line  int
	}{
		{"too few fields", tooFew, nil, 4},
		{"too many fields", tooMany, nil, 6},
		{"row the filter removes", filteredOut, []string{"--where", "dept=eng"}, 5},
		{"with sort and json", tooFew, []string{"--sort", "age", "--format", "json"}, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantFail(t, run(t, c.stdin, c.args...), 1, line(c.line))
		})
	}
}

func TestNoHeader(t *testing.T) {
	for _, args := range [][]string{nil, {"--format", "json"}} {
		t.Run(strings.Join(append([]string{"args"}, args...), " "), func(t *testing.T) {
			wantFail(t, run(t, "", args...), 1, `\S`)
		})
	}
}

func TestBadFlagValue(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"unknown format", []string{"--format", "xml"}},
		{"negative limit", []string{"--limit", "-1"}},
		{"limit not a number", []string{"--limit", "abc"}},
		{"where without equals", []string{"--where", "dept"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantFail(t, run(t, people, c.args...), 2)
		})
	}
}
