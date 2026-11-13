// Package sandbox runs a command inside bubblewrap. The command sees the
// system's /usr and /etc read-only, the folders it is given, a private /tmp
// and nothing else of the host: not the user's home, keys or other repos.
// It shares the network; bubblewrap cannot filter it by host.
package sandbox

import (
	"cmp"
	"context"
	"os"
	"os/exec"
	"slices"
	"sync"
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

// rootLinks copies the host's links such as /lib64 -> usr/lib64, since binaries name their loader by that path.
var rootLinks = sync.OnceValue(func() []string {
	var b []string
	for _, l := range [][2]string{{"/bin", "usr/bin"}, {"/sbin", "usr/bin"}, {"/lib", "usr/lib"}, {"/lib64", "usr/lib"}} {
		target, _ := os.Readlink(l[0])
		b = append(b, "--symlink", cmp.Or(target, l[1]), l[0])
	}
	return b
})

// Command returns name with args, wrapped in bwrap.
func Command(ctx context.Context, s Spec, name string, args ...string) *exec.Cmd {
	b := slices.Concat([]string{
		"--unshare-all", "--share-net", "--die-with-parent", "--new-session",
		"--ro-bind", "/usr", "/usr",
		"--ro-bind", "/etc", "/etc",
	}, rootLinks(), []string{
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	})
	for _, p := range s.RO {
		b = append(b, "--ro-bind", p, p)
	}
	for _, p := range s.RW {
		b = append(b, "--bind", p, p)
	}
	b = append(b, "--chdir", s.Dir, "--", name)
	cmd := exec.CommandContext(ctx, "bwrap", slices.Concat(b, args)...)
	// bwrap passes its environment on. Other users can read a command line
	// but not an environment, so a credential goes here, not in --setenv.
	cmd.Env = append([]string{"HOME=" + s.Home, "PATH=/usr/bin"}, s.Env...)
	cmd.WaitDelay = waitDelay
	return cmd
}
