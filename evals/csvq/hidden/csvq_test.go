// Hidden acceptance tests for evals/csvq/spec.md. The factory never sees them.
package hidden

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/brokentari/galvantula/evals/evalkit"
)

var bin string

func TestMain(m *testing.M) { os.Exit(evalkit.Main(m, "csvq", "../reference", &bin)) }

const people = `name,team,age
ada,core,36
bo,web,9
cy,core,10
di,web,36
`

func csvq(t *testing.T, stdin string, args ...string) evalkit.Result {
	t.Helper()
	return evalkit.Run(t, bin, "", stdin, args...)
}

func TestPassThrough(t *testing.T) {
	csvq(t, people).Want(t, 0, people)
}

func TestSelect(t *testing.T) {
	csvq(t, people, "--select", "age,name").Want(t, 0, "age,name\n36,ada\n9,bo\n10,cy\n36,di\n")
}

func TestWhere(t *testing.T) {
	csvq(t, people, "--where", "team=core").Want(t, 0, "name,team,age\nada,core,36\ncy,core,10\n")
	csvq(t, people, "--where", "team!=core").Want(t, 0, "name,team,age\nbo,web,9\ndi,web,36\n")
	csvq(t, people, "--where", "team=web", "--where", "age=36").Want(t, 0, "name,team,age\ndi,web,36\n")
	csvq(t, people, "--where", "team=nobody").Want(t, 0, "name,team,age\n")
}

func TestWhereOnUnselectedColumn(t *testing.T) {
	csvq(t, people, "--where", "team=web", "--select", "name").Want(t, 0, "name\nbo\ndi\n")
}

func TestNumericSort(t *testing.T) {
	csvq(t, people, "--sort", "age", "--select", "name,age").Want(t, 0, "name,age\nbo,9\ncy,10\nada,36\ndi,36\n")
}

func TestTextSort(t *testing.T) {
	in := "v\nb10\na\nb9\n"
	csvq(t, in, "--sort", "v").Want(t, 0, "v\na\nb10\nb9\n")
}

func TestDescKeepsTiesInOrder(t *testing.T) {
	csvq(t, people, "--sort", "age", "--desc", "--select", "name").Want(t, 0, "name\nada\ndi\ncy\nbo\n")
}

func TestLimitAfterSort(t *testing.T) {
	csvq(t, people, "--sort", "age", "--limit", "2", "--select", "name").Want(t, 0, "name\nbo\ncy\n")
	csvq(t, people, "--limit", "0").Want(t, 0, "name,team,age\n")
}

func TestQuotedFields(t *testing.T) {
	in := "id,note\n1,\"hello, world\"\n2,\"say \"\"hi\"\"\"\n"
	csvq(t, in).Want(t, 0, in)
}

func TestJSON(t *testing.T) {
	res := csvq(t, people, "--format", "json", "--where", "team=core", "--select", "name,age")
	var got []map[string]string
	if err := json.Unmarshal([]byte(res.Stdout), &got); err != nil || res.Code != 0 {
		t.Fatalf("exit %d, stdout %q is not a JSON array of objects: %v", res.Code, res.Stdout, err)
	}
	want := []map[string]string{{"name": "ada", "age": "36"}, {"name": "cy", "age": "10"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestJSONEmpty(t *testing.T) {
	res := csvq(t, people, "--format", "json", "--where", "team=none")
	var got []map[string]string
	if err := json.Unmarshal([]byte(res.Stdout), &got); err != nil || got == nil || len(got) != 0 {
		t.Errorf("stdout %q, want an empty JSON array", res.Stdout)
	}
}

func TestUnknownColumn(t *testing.T) {
	csvq(t, people, "--select", "name,salary").WantFail(t, 1, "salary")
	csvq(t, people, "--where", "salary=1").WantFail(t, 1, "salary")
	csvq(t, people, "--sort", "salary").WantFail(t, 1, "salary")
}

func TestBadInput(t *testing.T) {
	csvq(t, "a,b\n1,2\n3\n").WantFail(t, 1, "3")
	csvq(t, "").WantFail(t, 1)
}

func TestBadFlags(t *testing.T) {
	csvq(t, people, "--format", "xml").WantFail(t, 2)
	csvq(t, people, "--limit", "-1").WantFail(t, 2)
	csvq(t, people, "--where", "team").WantFail(t, 2)
}
