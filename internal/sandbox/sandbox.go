// Package sandbox runs a command inside bubblewrap. The command sees the
// system's /usr and /etc read-only, the folders it is given, a private /tmp
// and nothing else of the host: not the user's home, keys or other repos.
// It shares the network; bubblewrap cannot filter it by host.
package sandbox

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// Spec says what the sandboxed command may see.
type Spec struct {
	Dir  string   // working directory; must be inside RW or RO
	RW   []string // folders mounted read-write, at the same path
	RO   []string // files or folders mounted read-only, at the same path
	Home string   // HOME; must be inside RW
	Env  []string // the only environment variables, besides HOME and PATH=/usr/bin
}

// waitDelay bounds how long Wait waits for output pipes after the command is killed.
const waitDelay = 5 * time.Second

// Command returns name with args, wrapped in bwrap.
func Command(ctx context.Context, s Spec, name string, args ...string) *exec.Cmd {
	b := []string{
		"--unshare-all", "--share-net", "--die-with-parent", "--new-session", "--clearenv",
		"--ro-bind", "/usr", "/usr",
		"--ro-bind", "/etc", "/etc",
		"--symlink", "usr/bin", "/bin",
		"--symlink", "usr/bin", "/sbin",
		"--symlink", "usr/lib", "/lib",
		"--symlink", "usr/lib", "/lib64",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	}
	for _, p := range s.RO {
		b = append(b, "--ro-bind", p, p)
	}
	for _, p := range s.RW {
		b = append(b, "--bind", p, p)
	}
	b = append(b, "--chdir", s.Dir, "--setenv", "HOME", s.Home, "--setenv", "PATH", "/usr/bin")
	for _, kv := range s.Env {
		k, v, _ := strings.Cut(kv, "=")
		b = append(b, "--setenv", k, v)
	}
	b = append(b, "--", name)
	cmd := exec.CommandContext(ctx, "bwrap", slices.Concat(b, args)...)
	cmd.WaitDelay = waitDelay
	return cmd
}
