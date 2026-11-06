package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCountTests(t *testing.T) {
	tests := []struct {
		name           string
		out            string
		passed, failed int
	}{
		{"mixed", `{"Action":"run","Test":"TestA"}
{"Action":"pass","Test":"TestA"}
{"Action":"pass","Test":"TestA/sub"}
{"Action":"fail","Test":"TestB"}
{"Action":"fail","Test":"TestB/sub"}
{"Action":"fail"}`, 1, 1},
		{"build error, no tests ran", `# evalwork/x/hidden
x_test.go:3:2: undefined: nope
{"Action":"fail"}`, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			passed, failed, _ := countTests(tt.out)
			if passed != tt.passed || failed != tt.failed {
				t.Errorf("got %d passed, %d failed; want %d and %d", passed, failed, tt.passed, tt.failed)
			}
		})
	}
}

func TestInstall(t *testing.T) {
	setUp := func(t *testing.T) (work, dir string) {
		work, dir = t.TempDir(), t.TempDir()
		must(t, writeFiles(dir, map[string][]byte{
			"spec.md":                 []byte("spec"),
			"hidden/x_test.go":        []byte("old test"),
			"reference/cmd/x/main.go": []byte("old ref"),
			"assumptions.md":          []byte("old notes"),
		}))
		must(t, writeFiles(work, map[string][]byte{
			"x/hidden/x_test.go":        []byte(`import "evalwork/evalkit"`),
			"x/reference/cmd/x/main.go": []byte("new ref"),
			"x/assumptions.md":          []byte("new notes"),
		}))
		return work, dir
	}
	read := func(t *testing.T, path string) string {
		data, err := os.ReadFile(path)
		must(t, err)
		return string(data)
	}

	t.Run("tests pass", func(t *testing.T) {
		work, dir := setUp(t)
		must(t, install(work, dir, "x", func(string) error { return nil }))
		if got := read(t, filepath.Join(dir, "hidden/x_test.go")); got != `import "`+repoModule+`/evalkit"` {
			t.Errorf("hidden test = %q, want the repo import path", got)
		}
		if got := read(t, filepath.Join(dir, "assumptions.md")); got != "new notes" {
			t.Errorf("assumptions.md = %q, want the new file", got)
		}
	})

	t.Run("tests fail", func(t *testing.T) {
		work, dir := setUp(t)
		err := install(work, dir, "x", func(string) error { return errors.New("boom") })
		if err == nil {
			t.Fatal("install succeeded although the tests failed")
		}
		for path, want := range map[string]string{
			"hidden/x_test.go":        "old test",
			"reference/cmd/x/main.go": "old ref",
			"assumptions.md":          "old notes",
		} {
			if got := read(t, filepath.Join(dir, path)); got != want {
				t.Errorf("%s = %q, want the old file back (%q)", path, got, want)
			}
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 4 { // spec.md, hidden, reference, assumptions.md
			t.Errorf("dir holds %d entries, want 4: the staging folder must be gone", len(entries))
		}
	})
}

// If moving the old parts aside stops halfway, putting them back must not
// delete the parts that never moved.
func TestPutBackAfterHalfMove(t *testing.T) {
	dir, old := t.TempDir(), t.TempDir()
	must(t, writeFiles(dir, map[string][]byte{"reference/main.go": []byte("ref"), "assumptions.md": []byte("notes")}))
	must(t, writeFiles(old, map[string][]byte{"hidden/x_test.go": []byte("test")}))

	must(t, putBack(dir, old, nil, []string{"hidden"}))
	for path, want := range map[string]string{"hidden/x_test.go": "test", "reference/main.go": "ref", "assumptions.md": "notes"} {
		got, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", path, got, err, want)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
