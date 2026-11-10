package executor

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDeps(t *testing.T) {
	good := "github.com/x/y v1.2.3 replaces 300 lines of parsing\n\n"
	if d, err := parseDeps(good); err != nil || len(d) != 1 || d[0].Reason != "replaces 300 lines of parsing" {
		t.Fatalf("got %+v, %v", d, err)
	}
	for name, text := range map[string]string{
		"no reason":      "github.com/x/y v1.2.3\n",
		"a branch":       "github.com/x/y main because\n",
		"a pre-release":  "github.com/x/y v1.2.3-rc1 because\n",
		"not a module":   "y v1.2.3 because\n",
		"too many":       strings.Repeat("github.com/x/y v1.2.3 because\n", maxDeps+1),
		"a path outside": "github.com/x/../y v1.2.3 because\n",
	} {
		if _, err := parseDeps(text); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// fakeProxy serves one module, example.com/hello v1.0.0, from a folder.
func fakeProxy(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "example.com", "hello", "@v")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mod := "module example.com/hello\n\ngo 1.27\n"
	files := map[string]string{
		"v1.0.0.info": `{"Version":"v1.0.0","Time":"2020-01-01T00:00:00Z"}`,
		"v1.0.0.mod":  mod,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Create(filepath.Join(dir, "v1.0.0.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, data := range map[string]string{"go.mod": mod, "hello.go": "package hello\n\nfunc Hi() string { return \"hi\" }\n"} {
		w, _ := zw.Create("example.com/hello@v1.0.0/" + name)
		w.Write([]byte(data))
	}
	if err := errors.Join(zw.Close(), f.Close()); err != nil {
		t.Fatal(err)
	}
	return "file://" + filepath.Dir(filepath.Dir(filepath.Dir(dir)))
}

func TestSettleDepsVendorsTheDeclaredModule(t *testing.T) {
	c := Claude{Proxy: fakeProxy(t), SumDB: "off"}
	product := func(deps, require string) string {
		wt := t.TempDir()
		for name, data := range map[string]string{
			"go.mod":   "module product\n\ngo 1.27\n" + require,
			"main.go":  "package main\n\nimport \"example.com/hello\"\n\nfunc main() { println(hello.Hi()) }\n",
			depsFile:   deps,
			"go.sum":   "planted by the agent\n",
			"vendor/x": "planted by the agent\n",
		} {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(wt, name)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(wt, name), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return wt
	}
	req := "\nrequire example.com/hello v1.0.0\n"

	wt := product("example.com/hello v1.0.0 a greeting\n", req)
	if err := c.settleDeps(t.Context(), wt, func(string) {}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"vendor/example.com/hello/hello.go", "vendor/modules.txt", "go.sum"} {
		if _, err := os.Stat(filepath.Join(wt, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(wt, "vendor", "x")); err == nil {
		t.Error("the agent's file in vendor/ stayed")
	}
	if err := checkDeps(t.Context(), wt); err != nil {
		t.Errorf("checkDeps after settleDeps: %v", err)
	}

	for name, tc := range map[string]struct{ deps, require, want string }{
		"a module not in go.mod":      {"example.com/hello v1.0.0 a greeting\n", "", "go.mod has"},
		"a module not in deps.txt":    {"", req, "does not list"},
		"another version than listed": {"example.com/hello v1.0.1 a greeting\n", req, "go.mod has"},
	} {
		t.Run(name, func(t *testing.T) {
			err := c.settleDeps(t.Context(), product(tc.deps, tc.require), func(string) {})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error with %q", err, tc.want)
			}
		})
	}
}
