package executor

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// A product may use a module outside the standard library, but only one that
// the architect declared in deps.txt, one line each: "module version reason".
// A person sees the list at the contract gate. The runtime, not an agent,
// downloads the modules and copies them into vendor/, so that every later
// build is offline and the code of a module is in the product's history.

const (
	depsFile = "deps.txt"
	maxDeps  = 3
)

var (
	modulePath    = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*\.[a-z]+(/[A-Za-z0-9._~-]+)*$`)
	moduleVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
)

// dep is one module the architect asks for.
type dep struct{ Module, Version, Reason string }

func parseDeps(text string) ([]dep, error) {
	var deps []dep
	for line := range strings.Lines(text) {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) < 3 || !modulePath.MatchString(f[0]) || strings.Contains(f[0], "..") || !moduleVersion.MatchString(f[1]) {
			return nil, fmt.Errorf("%s: %q must be \"module vX.Y.Z reason\", with an exact release version and the reason in plain words", depsFile, strings.TrimSpace(line))
		}
		deps = append(deps, dep{f[0], f[1], strings.Join(f[2:], " ")})
	}
	if len(deps) > maxDeps {
		return nil, fmt.Errorf("%s lists %d modules; at most %d are allowed", depsFile, len(deps), maxDeps)
	}
	return deps, nil
}

// readDeps parses deps.txt in dir. A missing file means no modules.
func readDeps(dir string) ([]dep, error) {
	data, err := os.ReadFile(filepath.Join(dir, depsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseDeps(string(data))
}

// goTool runs the go command on the host, not in the sandbox, with a bare
// environment. env adds settings. Only the factory's own commands use it:
// go mod edit, tidy and vendor run no code of the product.
func goTool(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.TempDir(), "GOENV=off", "GOTOOLCHAIN=local", "GOTELEMETRY=off", "GOFLAGS=-buildvcs=false"}, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// checkDeps requires that the modules that go.mod needs directly are exactly
// the modules in deps.txt, at the same versions. It reads files only.
func checkDeps(ctx context.Context, dir string) error {
	want, err := readDeps(dir)
	if err != nil {
		return err
	}
	out, err := goTool(ctx, dir, nil, "mod", "edit", "-json")
	if err != nil {
		return err
	}
	var mod struct {
		Require []struct {
			Path, Version string
			Indirect      bool
		}
	}
	if err := json.Unmarshal(out, &mod); err != nil {
		return err
	}
	have := map[string]string{}
	for _, r := range mod.Require {
		if !r.Indirect {
			have[r.Path] = r.Version
		}
	}
	var errs []error
	for _, d := range want {
		if have[d.Module] != d.Version {
			errs = append(errs, fmt.Errorf("%s lists %s %s, but go.mod has %q", depsFile, d.Module, d.Version, have[d.Module]))
		}
		delete(have, d.Module)
	}
	for m := range have {
		errs = append(errs, fmt.Errorf("go.mod requires %s, which %s does not list", m, depsFile))
	}
	slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	return errors.Join(errs...)
}

// settleDeps runs after the architect. It checks that go.mod matches
// deps.txt, then rebuilds go.sum and vendor/ from the module proxy in a new
// module cache. It discards the architect's own go.sum and vendor/, so that
// the code in vendor/ is what the proxy served, checked against the Go
// checksum database, and not what an agent wrote.
func (c Claude) settleDeps(ctx context.Context, wt string, log func(string)) error {
	if err := errors.Join(os.RemoveAll(filepath.Join(wt, "vendor")), os.RemoveAll(filepath.Join(wt, "go.sum"))); err != nil {
		return &InfraError{Err: err}
	}
	deps, err := readDeps(wt)
	if err != nil {
		return err
	}
	if err := checkDeps(ctx, wt); err != nil || len(deps) == 0 {
		return err
	}
	cache, err := os.MkdirTemp("", "go-mod-")
	if err != nil {
		return &InfraError{Err: err}
	}
	defer func() { // Go makes the module cache read-only
		filepath.WalkDir(cache, func(p string, d os.DirEntry, _ error) error { return os.Chmod(p, 0o755) })
		os.RemoveAll(cache)
	}()
	env := []string{"GOMODCACHE=" + cache, "GOFLAGS=-mod=mod -buildvcs=false", "GOPROXY=" + cmp.Or(c.Proxy, "https://proxy.golang.org"), "GOSUMDB=" + cmp.Or(c.SumDB, "sum.golang.org")}
	for _, step := range [][]string{{"mod", "tidy"}, {"mod", "vendor"}} {
		if _, err := goTool(ctx, wt, env, step...); err != nil {
			return err
		}
	}
	if err := checkDeps(ctx, wt); err != nil {
		return err
	}
	for _, d := range deps {
		log(fmt.Sprintf("dependency: %s %s: %s", d.Module, d.Version, d.Reason))
	}
	return nil
}
