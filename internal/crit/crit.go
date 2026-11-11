// Package crit runs Crit (https://crit.md) on a copy of a contract, so a
// person can review it there, and reads the comments back from its review
// file. Crit is optional: Find returns nil when it is not installed.
package crit

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const (
	startTimeout = 15 * time.Second
	stopTimeout  = 5 * time.Second
)

var daemonLine = regexp.MustCompile(`crit daemon at (http://\S+)`)

// Sessions are the Crit processes of one server, one for each gate.
type Sessions struct {
	path string
	mu   sync.Mutex
	open map[string]*session // by gate folder
}

type session struct {
	round string // the round folder
	url   string
	cmd   *exec.Cmd
}

// Find looks for crit on PATH. It returns nil if there is none.
func Find() *Sessions {
	p, err := exec.LookPath("crit")
	if err != nil {
		return nil
	}
	return &Sessions{path: p, open: map[string]*session{}}
}

// Path is the program that Find found.
func (s *Sessions) Path() string { return s.path }

func roundDir(gate string, round int) (string, error) {
	abs, err := filepath.Abs(gate) // HOME must not depend on the working directory
	return filepath.Join(abs, "round-"+strconv.Itoa(round)), err
}

// env is all that Crit gets: its own home with no config or login token, and no sharing.
func env(round string) []string {
	return []string{"HOME=" + filepath.Join(round, "home"), "PATH=" + os.Getenv("PATH"),
		"CRIT_SHARE_URL=", "CRIT_NO_UPDATE_CHECK=1", "CRIT_NO_INTEGRATION_CHECK=1"}
}

// Open writes markdown to CONTRACT.md in the folder of the round and runs
// Crit on it. It returns Crit's address. A second call for the same gate and
// round returns the same address.
func (s *Sessions) Open(gate string, round int, markdown string) (string, error) {
	dir, err := roundDir(gate, round)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old := s.open[gate]; old != nil {
		if old.round == dir {
			return old.url, nil
		}
		s.stop(gate, old)
	}
	if err := os.MkdirAll(filepath.Join(dir, "home"), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "CONTRACT.md"), []byte(markdown), 0o644); err != nil {
		return "", err
	}
	cmd := exec.Command(s.path, "--no-open", "CONTRACT.md")
	cmd.Dir, cmd.Env = dir, env(dir)
	pr, pw := io.Pipe()
	cmd.Stderr = pw
	cmd.WaitDelay = time.Second // a daemon that keeps stderr open must not hold Wait
	if err := cmd.Start(); err != nil {
		return "", err
	}
	go func() { cmd.Wait(); pw.Close() }()

	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			if m := daemonLine.FindStringSubmatch(sc.Text()); m != nil {
				found <- m[1]
				break
			}
		}
		io.Copy(io.Discard, pr) // so Crit never blocks on a full pipe
		close(found)
	}()
	var addr string
	select {
	case addr = <-found:
	case <-time.After(startTimeout):
	}
	if err := checkAddr(addr); err != nil {
		cmd.Process.Signal(syscall.SIGTERM)
		s.stopDaemon(dir)
		return "", err
	}
	s.open[gate] = &session{round: dir, url: addr, cmd: cmd}
	return addr, nil
}

// checkAddr accepts only an address on this machine.
func checkAddr(addr string) error {
	if addr == "" {
		return errors.New("crit did not print its address in 15 seconds")
	}
	u, err := url.Parse(addr)
	if err != nil {
		return fmt.Errorf("crit printed a bad address %q: %w", addr, err)
	}
	h := u.Hostname()
	if ip, err := netip.ParseAddr(h); h != "localhost" && (err != nil || !ip.IsLoopback()) {
		return fmt.Errorf("crit printed an address that is not on this machine: %q", addr)
	}
	return nil
}

// Stop ends the Crit of a gate, if one runs.
func (s *Sessions) Stop(gate string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss := s.open[gate]; ss != nil {
		s.stop(gate, ss)
	}
}

// StopAll ends every Crit.
func (s *Sessions) StopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for g, ss := range s.open {
		s.stop(g, ss)
	}
}

func (s *Sessions) stop(gate string, ss *session) {
	delete(s.open, gate)
	ss.cmd.Process.Signal(syscall.SIGTERM)
	s.stopDaemon(ss.round)
}

// stopDaemon runs "crit stop --all" in the round's home: Crit's server can outlive the command.
func (s *Sessions) stopDaemon(round string) {
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.path, "stop", "--all")
	cmd.Dir, cmd.Env = round, env(round)
	cmd.Run()
}

// Running returns the address of the Crit of a gate, or "".
func (s *Sessions) Running(gate string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss := s.open[gate]; ss != nil {
		return ss.url
	}
	return ""
}

// ReviewFile returns the review file of a round, or "" if Crit wrote none.
func ReviewFile(gate string, round int) string {
	dir, err := roundDir(gate, round)
	if err != nil {
		return ""
	}
	files, _ := filepath.Glob(filepath.Join(dir, "home", ".crit", "reviews", "*", "review.json"))
	if len(files) == 0 {
		return ""
	}
	return files[0]
}
