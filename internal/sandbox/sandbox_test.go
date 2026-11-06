package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Other users on the host can read a command line, so a credential must
// not be on it.
func TestEnvIsNotOnTheCommandLine(t *testing.T) {
	cmd := Command(t.Context(), Spec{Dir: "/w", Home: "/h", Env: []string{"TOKEN=secret"}}, "true")
	if strings.Contains(strings.Join(cmd.Args, " "), "secret") {
		t.Errorf("args %q hold the credential", cmd.Args)
	}
	if !slices.Contains(cmd.Env, "TOKEN=secret") {
		t.Errorf("env %q lacks the credential", cmd.Env)
	}
}

func TestSandboxHidesTheHost(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap is not installed")
	}
	work, home, secretDir := t.TempDir(), t.TempDir(), t.TempDir()
	secret := filepath.Join(secretDir, "secret")
	if err := os.WriteFile(secret, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEAK", "yes")
	s := Spec{Dir: work, RW: []string{work, home}, Home: home, Env: []string{"KEEP=1"}}

	script := `echo "$KEEP:$LEAK"; cat ` + secret + ` 2>/dev/null && echo SEEN; echo hi > out.txt; go version >/dev/null && echo GO`
	out, err := Command(t.Context(), s, "sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := string(out)
	if !strings.HasPrefix(got, "1:\n") {
		t.Errorf("environment: got %q, want only KEEP passed in", got)
	}
	if strings.Contains(got, "SEEN") {
		t.Error("the sandbox could read a file outside its folders")
	}
	if !strings.Contains(got, "GO") {
		t.Errorf("go does not run in the sandbox: %q", got)
	}
	if data, _ := os.ReadFile(filepath.Join(work, "out.txt")); string(data) != "hi\n" {
		t.Error("the sandbox could not write to its work folder")
	}
}
