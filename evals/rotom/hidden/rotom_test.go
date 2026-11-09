package hidden

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/brokentari/galvantula/evals/evalkit"
)

var bin string

func TestMain(m *testing.M) { os.Exit(evalkit.Main(m, "rotom", "../reference", &bin)) }

// timeout limits each wait on the server, so that a hang fails one test.
const timeout = 10 * time.Second

// Replies that the spec fixes.
const (
	ok     = "+OK\r\n"
	pong   = "+PONG\r\n"
	null   = "$-1\r\n"
	errInt = "-ERR value is not an integer or out of range\r\n"
	errSyn = "-ERR syntax error\r\n"
)

func bulk(s string) string {
	return fmt.Sprintf("$%d\r\n%s\r\n", len(s), s)
}

func num(n int64) string {
	return fmt.Sprintf(":%d\r\n", n)
}

func array(items ...string) string {
	return fmt.Sprintf("*%d\r\n", len(items)) + strings.Join(items, "")
}

func wrongArgs(name string) string {
	return "-ERR wrong number of arguments for '" + name + "' command\r\n"
}

// encode writes a command as a RESP array of bulk strings.
func encode(args ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	return b.String()
}

// readReply reads one whole RESP reply and returns its raw bytes.
func readReply(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return line, err
	}
	head, hasCRLF := strings.CutSuffix(line, "\r\n")
	if !hasCRLF || head == "" {
		return line, fmt.Errorf("bad reply line %q", line)
	}
	switch head[0] {
	case '+', '-', ':':
		return line, nil
	case '$', '*':
		n, err := strconv.Atoi(head[1:])
		if err != nil {
			return line, fmt.Errorf("bad length in reply line %q", line)
		}
		if n < 0 {
			return line, nil
		}
		if head[0] == '$' {
			buf := make([]byte, n+2)
			_, err := io.ReadFull(r, buf)
			return line + string(buf), err
		}
		var raw strings.Builder
		raw.WriteString(line)
		for range n {
			item, err := readReply(r)
			raw.WriteString(item)
			if err != nil {
				return raw.String(), err
			}
		}
		return raw.String(), nil
	}
	return line, fmt.Errorf("unknown reply type in %q", line)
}

func parseInt(reply string) (int64, error) {
	digits, isInt := strings.CutPrefix(reply, ":")
	if !isInt {
		return 0, fmt.Errorf("%q is not an integer reply", reply)
	}
	return strconv.ParseInt(strings.TrimSuffix(digits, "\r\n"), 10, 64)
}

// output collects what the server writes to one stream. It closes line at
// the first newline.
type output struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	line chan struct{}
	seen func()
}

func newOutput() *output {
	o := &output{line: make(chan struct{})}
	o.seen = sync.OnceFunc(func() { close(o.line) })
	return o
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.buf.Write(p)
	if bytes.ContainsRune(p, '\n') {
		o.seen()
	}
	return len(p), nil
}

func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

type server struct {
	addr    string
	cmd     *exec.Cmd
	stdout  *output
	stderr  *output
	exited  chan struct{}
	waitErr error
}

var readyLine = regexp.MustCompile(`^ready (127\.0\.0\.1:[1-9][0-9]*)$`)

// start runs the server on a free port and waits for its ready line. The
// test's cleanup kills the server.
func start(t *testing.T) *server {
	t.Helper()
	s := &server{stdout: newOutput(), stderr: newOutput(), exited: make(chan struct{})}
	s.cmd = exec.Command(bin, "--addr", "127.0.0.1:0")
	s.cmd.Dir = t.TempDir()
	s.cmd.Stdout, s.cmd.Stderr = s.stdout, s.stderr
	s.cmd.WaitDelay = 5 * time.Second
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		s.waitErr = s.cmd.Wait()
		close(s.exited)
	}()
	t.Cleanup(func() {
		s.cmd.Process.Kill()
		<-s.exited
	})
	select {
	case <-s.stdout.line:
	case <-s.exited:
		t.Fatalf("server exited before it was ready: %v\nstdout: %s\nstderr: %s", s.waitErr, s.stdout, s.stderr)
	case <-time.After(timeout):
		t.Fatalf("no ready line after %s\nstdout: %s\nstderr: %s", timeout, s.stdout, s.stderr)
	}
	line, _, _ := strings.Cut(s.stdout.String(), "\n")
	m := readyLine.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("first stdout line is %q, want \"ready 127.0.0.1:PORT\"", line)
	}
	s.addr = m[1]
	return s
}

type conn struct {
	net.Conn
	r *bufio.Reader
}

func (s *server) dial(t *testing.T) *conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", s.addr, timeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &conn{Conn: c, r: bufio.NewReader(c)}
}

func (c *conn) send(raw string) error {
	c.SetWriteDeadline(time.Now().Add(timeout))
	_, err := io.WriteString(c.Conn, raw)
	return err
}

func (c *conn) reply() (string, error) {
	c.SetReadDeadline(time.Now().Add(timeout))
	return readReply(c.r)
}

func (c *conn) do(args ...string) (string, error) {
	if err := c.send(encode(args...)); err != nil {
		return "", err
	}
	return c.reply()
}

// want sends one command and checks the whole reply.
func (c *conn) want(t *testing.T, want string, args ...string) {
	t.Helper()
	got, err := c.do(args...)
	if err != nil {
		t.Fatalf("%q: %v (read %q)", args, err, got)
	}
	if got != want {
		t.Errorf("%q replied %q, want %q", args, got, want)
	}
}

// between sends one command and checks that it replies with an integer
// from lo to hi.
func (c *conn) between(t *testing.T, lo, hi int64, args ...string) {
	t.Helper()
	got, err := c.do(args...)
	if err != nil {
		t.Fatalf("%q: %v (read %q)", args, err, got)
	}
	if n, err := parseInt(got); err != nil || n < lo || n > hi {
		t.Errorf("%q replied %q, want an integer from %d to %d", args, got, lo, hi)
	}
}

// keys runs KEYS and returns the keys it replies with, sorted.
func (c *conn) keys(t *testing.T, pattern string) []string {
	t.Helper()
	got, err := c.do("KEYS", pattern)
	if err != nil {
		t.Fatalf("KEYS %q: %v (read %q)", pattern, err, got)
	}
	r := bufio.NewReader(strings.NewReader(got))
	head, _ := r.ReadString('\n')
	count, isArray := strings.CutPrefix(head, "*")
	n, err := strconv.Atoi(strings.TrimSuffix(count, "\r\n"))
	if !isArray || err != nil || n < 0 {
		t.Fatalf("KEYS %q replied %q, want an array", pattern, got)
	}
	keys := []string{}
	for range n {
		item, _ := readReply(r)
		_, rest, hasCRLF := strings.Cut(item, "\r\n")
		if !strings.HasPrefix(item, "$") || item == null || !hasCRLF {
			t.Fatalf("KEYS %q replied %q, want an array of bulk strings", pattern, got)
		}
		keys = append(keys, strings.TrimSuffix(rest, "\r\n"))
	}
	slices.Sort(keys)
	return keys
}

// wantClosed checks that the server closes the connection and sends
// nothing more.
func (c *conn) wantClosed(t *testing.T) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(timeout))
	rest, err := io.ReadAll(c.r)
	if len(rest) > 0 {
		t.Errorf("read %q, want the connection closed with nothing more", rest)
	}
	if ne, isNet := errors.AsType[net.Error](err); isNet && ne.Timeout() {
		t.Errorf("connection still open after %s, want it closed", timeout)
	}
}

func TestReady(t *testing.T) {
	t.Parallel()
	s := start(t)
	s.dial(t).want(t, pong, "PING")
}

func TestBadFlag(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--bogus"}, {"--addr"}} {
		evalkit.Run(t, bin, "", "", args...).WantFail(t, 2)
	}
}

func TestListenError(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	r := evalkit.Run(t, bin, "", "", "--addr", ln.Addr().String())
	r.WantFail(t, 1)
	if strings.TrimSpace(r.Stderr) == "" {
		t.Error("stderr is empty, want an error message")
	}
}

func TestSignal(t *testing.T) {
	t.Parallel()
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()
			s := start(t)
			c := s.dial(t)
			c.want(t, pong, "PING")
			if err := s.cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			select {
			case <-s.exited:
			case <-time.After(timeout):
				t.Fatalf("still running %s after %v, with a client connected", timeout, sig)
			}
			if code := s.cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("exit code %d after %v, want 0 (stderr: %s)", code, sig, s.stderr)
			}
			c.wantClosed(t)
		})
	}
}

func TestInline(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	for _, step := range []struct{ raw, want string }{
		{"PING\r\n", pong},
		{"ping\n", pong},
		{"\r\n\n\r\nPING\r\n", pong}, // blank lines get no reply
		{"SET k hello\n", ok},
		{"get k\r\n", bulk("hello")},
		{"ECHO hi\n", bulk("hi")},
		{"nosuch a b\r\n", "-ERR unknown command 'nosuch'\r\n"},
		{"INCR n\r\nINCR n\n", num(1)},
		{"", num(2)},
	} {
		if err := c.send(step.raw); err != nil {
			t.Fatal(err)
		}
		got, err := c.reply()
		if err != nil {
			t.Fatalf("after %q: %v (read %q)", step.raw, err, got)
		}
		if got != step.want {
			t.Errorf("after %q got %q, want %q", step.raw, got, step.want)
		}
	}
}

func TestPipeline(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	cmds := []string{
		encode("SET", "k", "1"), "PING\r\n", encode("INCR", "k"), encode("GET", "k"),
		encode("ECHO", "x"), encode("NOSUCH"), encode("DEL", "k"), encode("GET", "k"),
	}
	want := []string{ok, pong, num(2), bulk("2"), bulk("x"), "-ERR unknown command 'NOSUCH'\r\n", num(1), null}
	if err := c.send(strings.Join(cmds, "")); err != nil {
		t.Fatal(err)
	}
	for i, w := range want {
		got, err := c.reply()
		if err != nil {
			t.Fatalf("reply %d: %v (read %q)", i+1, err, got)
		}
		if got != w {
			t.Errorf("reply %d is %q, want %q", i+1, got, w)
		}
	}

	// A long pipeline, read while it is still being written.
	const n = 20000
	var b strings.Builder
	for range n {
		b.WriteString(encode("INCR", "n"))
	}
	errc := make(chan error, 1)
	go func() { errc <- c.send(b.String()) }()
	for i := range int64(n) {
		got, err := c.reply()
		if err != nil {
			t.Fatalf("reply %d of %d: %v (read %q)", i+1, n, err, got)
		}
		if got != num(i+1) {
			t.Fatalf("reply %d of %d is %q, want %q", i+1, n, got, num(i+1))
		}
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentClients(t *testing.T) {
	t.Parallel()
	s := start(t)
	a, b := s.dial(t), s.dial(t)
	a.want(t, ok, "SET", "k", "v")
	b.want(t, bulk("v"), "GET", "k")
	b.want(t, num(1), "DEL", "k")
	a.want(t, null, "GET", "k")

	const clients, each = 10, 300
	conns := make([]*conn, clients)
	for i := range conns {
		conns[i] = s.dial(t)
	}
	var wg sync.WaitGroup
	for i, c := range conns {
		wg.Go(func() {
			key := fmt.Sprintf("key%d", i)
			var last int64
			for j := range each {
				got, err := c.do("INCR", "shared")
				n, perr := parseInt(got)
				if err != nil || perr != nil || n <= last {
					t.Errorf("client %d: INCR shared replied %q, %v after %d", i, got, err, last)
					return
				}
				last = n
				if got, err := c.do("SET", key, strconv.Itoa(j)); err != nil || got != ok {
					t.Errorf("client %d: SET replied %q, %v", i, got, err)
					return
				}
			}
		})
	}
	wg.Wait()
	a.want(t, bulk(strconv.Itoa(clients*each)), "GET", "shared")
	for i := range clients {
		a.want(t, bulk(strconv.Itoa(each-1)), "GET", fmt.Sprintf("key%d", i))
	}
}

func TestCaseAndBinary(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	c.want(t, ok, "set", "Key", "Value")
	c.want(t, bulk("Value"), "GeT", "Key")
	c.want(t, null, "GET", "key")
	c.want(t, null, "GET", "KEY")
	c.want(t, ok, "SET", "key", "other")
	c.want(t, bulk("Value"), "GET", "Key")
	c.want(t, bulk("other"), "get", "key")

	key, val := "k\r\n\x00", "v\r\n$-1\r\n\x00end"
	c.want(t, ok, "SET", key, val)
	c.want(t, bulk(val), "GET", key)
	c.want(t, num(int64(len(val))), "STRLEN", key)
	c.want(t, num(1), "EXISTS", key)
	c.want(t, bulk(val), "ECHO", val)

	big := strings.Repeat("0123456789abcdef", 1<<16)
	if got, err := c.do("SET", "big", big); err != nil || got != ok {
		t.Fatalf("SET of a 1 MiB value replied %q, %v; want %q", got, err, ok)
	}
	if got, err := c.do("GET", "big"); err != nil || got != bulk(big) {
		t.Errorf("GET of a 1 MiB value read %d bytes, %v; want the %d-byte value back", len(got), err, len(big))
	}
}

func TestProtocolError(t *testing.T) {
	t.Parallel()
	s := start(t)
	other := s.dial(t)
	other.want(t, ok, "SET", "k", "v")
	const prefix = "-ERR Protocol error: "
	for _, tc := range []struct{ name, raw, before string }{
		{"bad array length", "*x\r\n", ""},
		{"bad bulk length", "*1\r\n$x\r\n", ""},
		{"bulk length above 512 MiB", "*1\r\n$536870913\r\n", ""},
		{"no CRLF after bulk data", "*1\r\n$4\r\nPINGxx", ""},
		{"after a good command", "PING\r\n*1\r\n$x\r\n", pong},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := s.dial(t)
			if err := c.send(tc.raw); err != nil {
				t.Fatal(err)
			}
			if tc.before != "" {
				if got, err := c.reply(); err != nil || got != tc.before {
					t.Fatalf("first reply %q, %v; want %q", got, err, tc.before)
				}
			}
			got, err := c.reply()
			if err != nil {
				t.Fatalf("read reply: %v (read %q)", err, got)
			}
			if !strings.HasPrefix(got, prefix) || len(got) <= len(prefix)+2 {
				t.Errorf("reply %q, want %q and a reason", got, prefix)
			}
			c.wantClosed(t)
			other.want(t, bulk("v"), "GET", "k")
		})
	}
}

func TestEarlyClose(t *testing.T) {
	t.Parallel()
	s := start(t)
	check := s.dial(t)
	for _, tc := range []struct{ name, raw string }{
		{"in bulk data", "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$5\r\nhel"},
		{"in a bulk length", "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$5"},
		{"before the last argument", "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n"},
		{"inline line with no end", "SET k v"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := s.dial(t)
			if err := c.send(tc.raw); err != nil {
				t.Fatal(err)
			}
			if err := c.Conn.(*net.TCPConn).CloseWrite(); err != nil {
				t.Fatal(err)
			}
			c.wantClosed(t)
			check.want(t, null, "GET", "k")
		})
	}
	check.want(t, pong, "PING")
}

func TestUnknownCommand(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	c.want(t, "-ERR unknown command 'FOO'\r\n", "FOO")
	c.want(t, "-ERR unknown command 'fooBar'\r\n", "fooBar", "a", "b")
	c.want(t, pong, "PING")
}

func TestWrongArgs(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	for _, args := range [][]string{
		{"GET"}, {"GET", "a", "b"}, {"SET", "a"}, {"ECHO"}, {"ECHO", "a", "b"}, {"PING", "a", "b"},
		{"MSET"}, {"MSET", "a"}, {"MSET", "a", "1", "b"}, {"MGET"}, {"DEL"}, {"EXISTS"},
		{"INCR"}, {"INCR", "a", "b"}, {"DECR"}, {"INCRBY", "a"}, {"DECRBY", "a"},
		{"APPEND", "a"}, {"STRLEN"}, {"EXPIRE", "a"}, {"PEXPIRE", "a"}, {"TTL"}, {"PTTL"},
		{"PERSIST"}, {"KEYS"}, {"TYPE"}, {"DBSIZE", "a"},
	} {
		c.want(t, wrongArgs(strings.ToLower(args[0])), args...)
	}
	c.want(t, wrongArgs("get"), "GeT")
	c.want(t, wrongArgs("mset"), "mSeT", "a", "1", "b")
	c.want(t, num(0), "DBSIZE")
}

func TestPingEchoCommand(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	c.want(t, pong, "PING")
	c.want(t, pong, "ping")
	c.want(t, bulk("hello world"), "PING", "hello world")
	c.want(t, bulk("hi"), "ECHO", "hi")
	c.want(t, bulk(""), "ECHO", "")
	c.want(t, "*0\r\n", "COMMAND")
	c.want(t, "*0\r\n", "command")
}

func TestQuit(t *testing.T) {
	t.Parallel()
	s := start(t)
	c := s.dial(t)
	c.want(t, ok, "QUIT")
	c.wantClosed(t)
	d := s.dial(t)
	if err := d.send("quit\r\n"); err != nil {
		t.Fatal(err)
	}
	if got, err := d.reply(); err != nil || got != ok {
		t.Errorf("inline quit replied %q, %v; want %q", got, err, ok)
	}
	d.wantClosed(t)
	s.dial(t).want(t, pong, "PING")
}

func TestSet(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	c.want(t, ok, "SET", "k", "v1")
	c.want(t, bulk("v1"), "GET", "k")
	c.want(t, ok, "SET", "k", "v2")
	c.want(t, bulk("v2"), "GET", "k")

	c.want(t, null, "SET", "k", "v3", "NX")
	c.want(t, bulk("v2"), "GET", "k")
	c.want(t, ok, "SET", "n", "v", "NX")
	c.want(t, bulk("v"), "GET", "n")

	c.want(t, null, "SET", "x", "v", "XX")
	c.want(t, null, "GET", "x")
	c.want(t, num(0), "EXISTS", "x")
	c.want(t, ok, "SET", "k", "v4", "XX")
	c.want(t, bulk("v4"), "GET", "k")

	for _, opts := range [][]string{
		{"EX", "10", "PX", "100"}, {"PX", "100", "EX", "10"}, {"NX", "XX"}, {"XX", "NX"},
		{"EX", "10", "NX", "XX"}, {"BOGUS"}, {"EX"},
	} {
		c.want(t, errSyn, append([]string{"SET", "k", "bad"}, opts...)...)
	}
	c.want(t, errInt, "SET", "k", "bad", "EX", "ten")
	c.want(t, errInt, "SET", "k", "bad", "PX", "1.5")
	for _, opts := range [][]string{{"EX", "0"}, {"EX", "-1"}, {"PX", "0"}, {"PX", "-100"}} {
		args := append([]string{"SET", "k", "bad"}, opts...)
		if got, err := c.do(args...); err != nil || !strings.HasPrefix(got, "-") {
			t.Errorf("%q replied %q, %v; want an error", args, got, err)
		}
	}
	c.want(t, bulk("v4"), "GET", "k")
	c.want(t, num(-1), "TTL", "k")
}

func TestSetExpiry(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	c.want(t, ok, "SET", "k", "v", "EX", "100")
	c.between(t, 99, 100, "TTL", "k")
	c.between(t, 98000, 100000, "PTTL", "k")
	c.want(t, ok, "SET", "p", "v", "PX", "100000")
	c.between(t, 99, 100, "TTL", "p")

	// A failed NX keeps the value and the expiry.
	c.want(t, null, "SET", "k", "w", "NX")
	c.want(t, bulk("v"), "GET", "k")
	c.between(t, 99, 100, "TTL", "k")

	// A SET without EX or PX removes the expiry.
	c.want(t, ok, "SET", "k", "w")
	c.want(t, num(-1), "TTL", "k")
	c.want(t, ok, "SET", "p", "w", "XX")
	c.want(t, num(-1), "TTL", "p")

	c.want(t, ok, "SET", "q", "v", "EX", "50", "NX")
	c.between(t, 49, 50, "TTL", "q")
	c.want(t, ok, "SET", "q", "v2", "PX", "30000", "XX")
	c.between(t, 29, 30, "TTL", "q")
	c.want(t, bulk("v2"), "GET", "q")

	c.want(t, ok, "SET", "short", "v", "PX", "200")
	c.want(t, bulk("v"), "GET", "short")
	time.Sleep(400 * time.Millisecond)
	c.want(t, null, "GET", "short")
}

func TestGetMsetMget(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	c.want(t, null, "GET", "missing")
	c.want(t, ok, "MSET", "a", "1", "b", "2")
	c.want(t, bulk("1"), "GET", "a")
	c.want(t, array(bulk("1"), bulk("2"), null, bulk("1")), "MGET", "a", "b", "c", "a")
	c.want(t, array(null), "MGET", "c")
	c.want(t, ok, "MSET", "c", "3")
	c.want(t, array(bulk("3")), "MGET", "c")
}

func TestDelExists(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	c.want(t, ok, "MSET", "a", "1", "b", "2", "c", "3")
	c.want(t, num(3), "EXISTS", "a", "b", "c")
	c.want(t, num(3), "EXISTS", "a", "a", "x", "b")
	c.want(t, num(0), "EXISTS", "x")
	c.want(t, num(2), "DEL", "a", "b", "x")
	c.want(t, num(0), "DEL", "a")
	c.want(t, num(1), "EXISTS", "a", "b", "c")
	c.want(t, num(1), "DEL", "c", "c")
	c.want(t, null, "GET", "c")
	c.want(t, num(0), "DBSIZE")
}

func TestIncr(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	c.want(t, num(1), "INCR", "n")
	c.want(t, num(2), "INCR", "n")
	c.want(t, num(1), "DECR", "n")
	c.want(t, num(11), "INCRBY", "n", "10")
	c.want(t, num(6), "DECRBY", "n", "5")
	c.want(t, num(3), "INCRBY", "n", "-3")
	c.want(t, num(5), "DECRBY", "n", "-2")
	c.want(t, bulk("5"), "GET", "n")
	c.want(t, num(-1), "DECR", "a")
	c.want(t, num(7), "INCRBY", "b", "7")
	c.want(t, num(-7), "DECRBY", "c", "7")
	c.want(t, ok, "SET", "neg", "-10")
	c.want(t, num(-9), "INCR", "neg")
	c.want(t, bulk("-9"), "GET", "neg")

	// A value that is not a 64-bit integer gets the error and stays.
	for _, v := range []string{"abc", "1.5", "12abc", "", "9223372036854775808"} {
		c.want(t, ok, "SET", "s", v)
		c.want(t, errInt, "INCR", "s")
		c.want(t, errInt, "DECR", "s")
		c.want(t, errInt, "INCRBY", "s", "1")
		c.want(t, errInt, "DECRBY", "s", "1")
		c.want(t, bulk(v), "GET", "s")
	}

	// So does a step that is not a 64-bit integer.
	for _, by := range []string{"abc", "1.0", "9223372036854775808"} {
		c.want(t, errInt, "INCRBY", "n", by)
		c.want(t, errInt, "DECRBY", "n", by)
	}
	c.want(t, bulk("5"), "GET", "n")
	c.want(t, errInt, "INCRBY", "none", "x")
	c.want(t, num(0), "EXISTS", "none")

	// A result that would overflow gets the error and changes nothing.
	c.want(t, ok, "SET", "max", "9223372036854775807")
	c.want(t, errInt, "INCR", "max")
	c.want(t, errInt, "INCRBY", "max", "1")
	c.want(t, errInt, "DECRBY", "max", "-1")
	c.want(t, bulk("9223372036854775807"), "GET", "max")
	c.want(t, num(9223372036854775806), "DECR", "max")
	c.want(t, ok, "SET", "min", "-9223372036854775808")
	c.want(t, errInt, "DECR", "min")
	c.want(t, errInt, "DECRBY", "min", "1")
	c.want(t, errInt, "INCRBY", "min", "-1")
	c.want(t, bulk("-9223372036854775808"), "GET", "min")
	c.want(t, num(-9223372036854775807), "INCR", "min")
	c.want(t, errInt, "DECRBY", "zero", "-9223372036854775808")
	c.want(t, num(0), "EXISTS", "zero")
	c.want(t, num(-9223372036854775808), "INCRBY", "low", "-9223372036854775808")
	c.want(t, num(9223372036854775807), "INCRBY", "high", "9223372036854775807")

	// These commands keep the expiry.
	c.want(t, ok, "SET", "e", "10", "EX", "100")
	c.want(t, num(11), "INCR", "e")
	c.want(t, num(10), "DECR", "e")
	c.want(t, num(14), "INCRBY", "e", "4")
	c.want(t, num(12), "DECRBY", "e", "2")
	c.between(t, 98, 100, "TTL", "e")
}

func TestAppendStrlen(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	c.want(t, num(0), "STRLEN", "s")
	c.want(t, num(2), "APPEND", "s", "ab")
	c.want(t, num(4), "APPEND", "s", "cd")
	c.want(t, bulk("abcd"), "GET", "s")
	c.want(t, num(4), "STRLEN", "s")
	c.want(t, num(4), "APPEND", "s", "")
	c.want(t, ok, "SET", "u", "héllo")
	c.want(t, num(6), "STRLEN", "u")
	c.want(t, num(9), "APPEND", "u", "\x00\r\n")
	c.want(t, bulk("héllo\x00\r\n"), "GET", "u")
}

func TestExpire(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	c.want(t, num(0), "EXPIRE", "k", "100")
	c.want(t, num(0), "PEXPIRE", "k", "100000")
	c.want(t, num(-2), "TTL", "k")
	c.want(t, num(-2), "PTTL", "k")
	c.want(t, num(0), "PERSIST", "k")
	c.want(t, num(0), "EXISTS", "k")

	c.want(t, ok, "SET", "k", "v")
	c.want(t, num(-1), "TTL", "k")
	c.want(t, num(-1), "PTTL", "k")
	c.want(t, num(0), "PERSIST", "k")
	c.want(t, num(1), "EXPIRE", "k", "100")
	c.between(t, 99, 100, "TTL", "k")
	c.between(t, 98000, 100000, "PTTL", "k")
	c.want(t, num(1), "PEXPIRE", "k", "50000")
	c.between(t, 49, 50, "TTL", "k")
	c.between(t, 48000, 50000, "PTTL", "k")
	c.want(t, num(1), "PERSIST", "k")
	c.want(t, num(-1), "TTL", "k")
	c.want(t, num(0), "PERSIST", "k")
	c.want(t, bulk("v"), "GET", "k")

	c.want(t, errInt, "EXPIRE", "k", "ten")
	c.want(t, errInt, "PEXPIRE", "k", "1.5")
	c.want(t, num(-1), "TTL", "k")

	// A zero or negative time deletes the key at once.
	for i, args := range [][]string{{"EXPIRE", "0"}, {"EXPIRE", "-5"}, {"PEXPIRE", "0"}, {"PEXPIRE", "-5"}} {
		key := fmt.Sprintf("d%d", i)
		c.want(t, ok, "SET", key, "v", "EX", "100")
		c.want(t, num(1), args[0], key, args[1])
		c.want(t, null, "GET", key)
		c.want(t, num(0), "EXISTS", key)
		c.want(t, num(-2), "TTL", key)
	}
	c.want(t, num(0), "EXPIRE", "missing", "0")
}

func TestTTLRounds(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	c.want(t, ok, "SET", "up", "v", "PX", "2800")
	c.want(t, num(3), "TTL", "up")
	c.want(t, ok, "SET", "down", "v", "PX", "2200")
	c.want(t, num(2), "TTL", "down")
	c.want(t, num(1), "PEXPIRE", "up", "1800")
	c.want(t, num(2), "TTL", "up")
}

func TestExpiredKeyIsMissing(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	keys := []string{"get", "mget", "exists", "ttl", "strlen", "type", "del", "persist", "expire", "xx", "nx", "incr", "append"}
	for _, k := range keys {
		c.want(t, ok, "SET", k, "5", "PX", "300")
	}
	c.want(t, bulk("5"), "GET", "get")
	c.want(t, num(int64(len(keys))), "DBSIZE")
	time.Sleep(600 * time.Millisecond)

	c.want(t, num(0), "DBSIZE")
	if got := c.keys(t, "*"); len(got) != 0 {
		t.Errorf("KEYS * replied %q after the keys expired, want none", got)
	}
	c.want(t, null, "GET", "get")
	c.want(t, array(null), "MGET", "mget")
	c.want(t, num(0), "EXISTS", "exists")
	c.want(t, num(-2), "TTL", "ttl")
	c.want(t, num(-2), "PTTL", "ttl")
	c.want(t, num(0), "STRLEN", "strlen")
	c.want(t, "+none\r\n", "TYPE", "type")
	c.want(t, num(0), "DEL", "del")
	c.want(t, num(0), "PERSIST", "persist")
	c.want(t, num(0), "EXPIRE", "expire", "100")
	c.want(t, null, "SET", "xx", "new", "XX")
	c.want(t, null, "GET", "xx")
	c.want(t, ok, "SET", "nx", "new", "NX")
	c.want(t, num(-1), "TTL", "nx")
	c.want(t, num(1), "INCR", "incr")
	c.want(t, num(-1), "TTL", "incr")
	c.want(t, num(1), "APPEND", "append", "x")
	c.want(t, bulk("x"), "GET", "append")
	c.want(t, num(-1), "TTL", "append")
}

func TestKeys(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	c.want(t, "*0\r\n", "KEYS", "*")
	c.want(t, ok, "MSET", "hello", "1", "hallo", "1", "hxllo", "1", "hllo", "1", "heeello", "1",
		"a*b", "1", "a?b", "1", "axb", "1")
	for _, tc := range []struct{ pattern, want string }{
		{"*", "hello hallo hxllo hllo heeello a*b a?b axb"},
		{"hello", "hello"},
		{"h?llo", "hello hallo hxllo"},
		{"h*llo", "hello hallo hxllo hllo heeello"},
		{"h*", "hello hallo hxllo hllo heeello"},
		{"*llo", "hello hallo hxllo hllo heeello"},
		{"h[ae]llo", "hello hallo"},
		{"h[^e]llo", "hallo hxllo"},
		{"h[a-e]llo", "hello hallo"},
		{"h[w-z]llo", "hxllo"},
		{"a?b", "a*b a?b axb"},
		{`a\*b`, "a*b"},
		{`a\?b`, "a?b"},
		{"x*", ""},
	} {
		want := strings.Fields(tc.want)
		slices.Sort(want)
		if got := c.keys(t, tc.pattern); !slices.Equal(got, want) {
			t.Errorf("KEYS %q replied %q, want %q in any order", tc.pattern, got, want)
		}
	}
	c.want(t, "*0\r\n", "KEYS", "x*")
}

func TestTypeDbsizeFlushall(t *testing.T) {
	t.Parallel()
	c := start(t).dial(t)
	c.want(t, "+none\r\n", "TYPE", "k")
	c.want(t, num(0), "DBSIZE")
	c.want(t, ok, "MSET", "k", "v", "j", "w")
	c.want(t, "+string\r\n", "TYPE", "k")
	c.want(t, num(2), "DBSIZE")
	c.want(t, ok, "SET", "e", "v", "EX", "100")
	c.want(t, num(3), "DBSIZE")
	c.want(t, ok, "FLUSHALL")
	c.want(t, num(0), "DBSIZE")
	c.want(t, null, "GET", "k")
	c.want(t, num(-2), "TTL", "e")
	c.want(t, "*0\r\n", "KEYS", "*")
}
