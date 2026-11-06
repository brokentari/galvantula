// Package workspace manages a product's git repo: one repo per run, with a
// worktree per agent node. The runtime commits and merges; agents never run git.
package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo is a product's repository. Main holds the merged work.
type Repo struct{ Dir string }

// Open returns the repo at dir, creating it with an empty first commit if needed.
func Open(dir string) (Repo, error) {
	r := Repo{Dir: dir}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return r, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return r, err
	}
	if _, err := r.git(dir, "init", "-q", "-b", "main"); err != nil {
		return r, err
	}
	_, err := r.git(dir, "commit", "-q", "--allow-empty", "-m", "start")
	return r, err
}

// CommitAll commits every change in dir (the repo or one of its worktrees).
// It reports false if there was nothing to commit.
func (r Repo) CommitAll(dir, msg string) (bool, error) {
	if _, err := r.git(dir, "add", "-A"); err != nil {
		return false, err
	}
	if _, err := r.git(dir, "diff", "--cached", "--quiet"); err == nil {
		return false, nil
	}
	_, err := r.git(dir, "commit", "-q", "-m", msg)
	return err == nil, err
}

// Worktree returns the worktree for a node, on branch node/<id>. It creates
// it from main if needed.
func (r Repo) Worktree(node string) (string, error) {
	dir := filepath.Join(filepath.Dir(r.Dir), "nodes", node)
	if _, err := os.Stat(dir); err == nil {
		return dir, nil
	}
	_, err := r.git(r.Dir, "worktree", "add", "-q", "-B", "node/"+node, dir, "main")
	return dir, err
}

// Reset puts a worktree back to main and removes every file main does not have.
func (r Repo) Reset(dir string) error {
	if _, err := r.git(dir, "reset", "-q", "--hard", "main"); err != nil {
		return err
	}
	_, err := r.git(dir, "clean", "-q", "-fdx")
	return err
}

// Changed lists files that main has and dir has changed or deleted,
// committed or not. New files are not listed.
func (r Repo) Changed(dir string) ([]string, error) {
	out, err := r.git(dir, "diff", "--name-only", "main", "--", ".")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// Merge moves main forward to a node's branch.
func (r Repo) Merge(node string) error {
	_, err := r.git(r.Dir, "merge", "-q", "--ff-only", "node/"+node)
	return err
}

func (r Repo) git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-c", "user.name=galvantula", "-c", "user.email=factory@localhost"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}
