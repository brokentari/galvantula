package hidden

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
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

// Replies the spec fixes.
const (
	okReply   = "+OK\r\n"
	nullReply = "$-1\r\n"
	pong      = "+PONG\r\n"
	errInt    = "-ERR value is not an integer or out of range\r\n"
	errSyntax = "-ERR syntax error\r\n"
)

func bulk(s string) string { return "$" + strconv.Itoa(len(s)) + "\r\n" + s + "\r\n" }

func integer(n int64) string { return ":" + strconv.FormatInt(n, 10) + "\r\n" }

func array(items ...string) string {
	return "*" + strconv.Itoa(len(items)) + "\r\n" + strings.Join(items, "")
}

func wrongArgs(name string) string {
	return "-ERR wrong number of arguments for '" + name + "' command\r\n"
}

func unknown(name string) string { return "-ERR unknown command '" + name + "'\r\n" }

// encode writes a command as a RESP array of bulk strings.
func encode(args ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	return b.String()
}

// short keeps failure messages readable when a value is large.
func short(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

// readReply reads one RESP reply and returns its raw bytes.
func readReply(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return line, err
	}
	body, found := strings.CutSuffix(line, "\r\n")
	if !found || body == "" {
		return line, fmt.Errorf("reply line %q does not end in CRLF", line)
	}
	switch body[0] {
	case '+', '-', ':':
		return line, nil
	case '$', '*':
		n, err := strconv.Atoi(body[1:])
		if err != nil || n > 64<<20 {
			return line, fmt.Errorf("bad length in reply line %q", line)
		}
		if n < 0 {
			return line, nil
		}
		if body[0] == '$' {
			data := make([]byte, n+2)
			_, err := io.ReadFull(r, data)
			return line + string(data), err
		}
		var b strings.Builder
		b.WriteString(line)
		for range n {
			e, err := readReply(r)
			b.WriteString(e)
			if err != nil {
				return b.String(), err
			}
		}
		return b.String(), nil
	}
	return line, fmt.Errorf("unknown reply type in %q", line)
}

// elems splits an array reply into the raw replies it holds.
func elems(raw string) ([]string, error) {
	r := bufio.NewReader(strings.NewReader(raw))
	head, err := r.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("not an array: %q", short(raw))
	}
	rest, isArray := strings.CutPrefix(head, "*")
	n, err := strconv.Atoi(strings.TrimSuffix(rest, "\r\n"))
	if !isArray || err != nil || n < 0 {
		return nil, fmt.Errorf("not an array: %q", short(raw))
	}
	var out []string
	for range n {
		e, err := readReply(r)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// output collects what the server writes to stdout or stderr. Its line
// channel closes when the first full line arrives.
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
	addr           string
	cmd            *exec.Cmd
	stdout, stderr *output
	exited         chan struct{}
}

var readyLine = regexp.MustCompile(`^ready (127\.0\.0\.1:([0-9]+))$`)

// launch starts the server with args and waits for its ready line. The test
// kills the server when it ends.
func launch(t *testing.T, args ...string) *server {
	t.Helper()
	s := &server{stdout: newOutput(), stderr: newOutput(), exited: make(chan struct{})}
	s.cmd = exec.Command(bin, args...)
	s.cmd.Dir = t.TempDir()
	s.cmd.Stdout, s.cmd.Stderr = s.stdout, s.stderr
	s.cmd.WaitDelay = 5 * time.Second
	if err := s.cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}
	go func() {
		s.cmd.Wait()
		close(s.exited)
	}()
	t.Cleanup(func() {
		s.cmd.Process.Kill()
		<-s.exited
	})
	select {
	case <-s.stdout.line:
	case <-s.exited:
		t.Fatalf("server exited (%v) before it printed a line\nstdout: %q\nstderr: %s", s.cmd.ProcessState, s.stdout, s.stderr)
	case <-time.After(10 * time.Second):
		t.Fatalf("server printed no line in 10s\nstderr: %s", s.stderr)
	}
	first, _, _ := strings.Cut(s.stdout.String(), "\n")
	m := readyLine.FindStringSubmatch(first)
	if m == nil || m[2] == "0" {
		t.Fatalf("first stdout line %q, want \"ready 127.0.0.1:PORT\" with the real port", first)
	}
	s.addr = m[1]
	return s
}

func startServer(t *testing.T) *server {
	t.Helper()
	return launch(t, "--addr", "127.0.0.1:0")
}

type client struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func (s *server) dial(t *testing.T) *client {
	t.Helper()
	conn, err := net.DialTimeout("tcp", s.addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", s.addr, err)
	}
	t.Cleanup(func() { conn.Close() })
	return &client{t: t, conn: conn, r: bufio.NewReader(conn)}
}

// send writes raw bytes to the server.
func (c *client) send(raw string) {
	c.t.Helper()
	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c.conn, raw); err != nil {
		c.t.Fatalf("write %q: %v", short(raw), err)
	}
}

// reply reads the next reply.
func (c *client) reply() string {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := readReply(c.r)
	if err != nil {
		c.t.Fatalf("read reply: %v (got %q)", err, short(got))
	}
	return got
}

// expect reads the next reply and compares it with want.
func (c *client) expect(want string) {
	c.t.Helper()
	if got := c.reply(); got != want {
		c.t.Errorf("reply %q, want %q", short(got), short(want))
	}
}

// roundTrip sends one command and reads its reply. It is safe to call from
// any goroutine.
func (c *client) roundTrip(args ...string) (string, error) {
	c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c.conn, encode(args...)); err != nil {
		return "", err
	}
	return readReply(c.r)
}

func (c *client) do(args ...string) string {
	c.t.Helper()
	got, err := c.roundTrip(args...)
	if err != nil {
		c.t.Fatalf("%q: %v (got %q)", short(strings.Join(args, " ")), err, short(got))
	}
	return got
}

// want sends one command and compares the raw reply with want.
func (c *client) want(want string, args ...string) {
	c.t.Helper()
	if got := c.do(args...); got != want {
		c.t.Errorf("%q: reply %q, want %q", short(strings.Join(args, " ")), short(got), short(want))
	}
}

// wantInt sends one command and wants an integer reply from lo to hi.
func (c *client) wantInt(lo, hi int64, args ...string) {
	c.t.Helper()
	got := c.do(args...)
	rest, isInt := strings.CutPrefix(got, ":")
	n, err := strconv.ParseInt(strings.TrimSuffix(rest, "\r\n"), 10, 64)
	if !isInt || err != nil || n < lo || n > hi {
		c.t.Errorf("%q: reply %q, want an integer from %d to %d", strings.Join(args, " "), got, lo, hi)
	}
}

// closed wants the server to close the connection with nothing more to read.
func (c *client) closed() {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := c.r.ReadByte()
	if err == nil {
		c.t.Errorf("read %q, want the server to close the connection", b)
		return
	}
	if ne, isNet := errors.AsType[net.Error](err); isNet && ne.Timeout() {
		c.t.Errorf("connection still open after 5s, want the server to close it")
	}
}

// Server

func TestReadyLine(t *testing.T) {
	s := startServer(t)
	s.dial(t).want(pong, "PING")
	if got := s.stdout.String(); got != "ready "+s.addr+"\n" {
		t.Errorf("stdout %q, want only the line \"ready %s\"", got, s.addr)
	}
}

func TestDefaultAddr(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:6379")
	if err != nil {
		// Another program holds the default address, so the server cannot listen.
		r := evalkit.Run(t, bin, "", "")
		r.WantFail(t, 1)
		if strings.TrimSpace(r.Stderr) == "" {
			t.Error("stderr is empty, want an error message")
		}
		return
	}
	ln.Close()
	s := launch(t)
	if s.addr != "127.0.0.1:6379" {
		t.Errorf("ready on %s, want the default 127.0.0.1:6379", s.addr)
	}
	s.dial(t).want(pong, "PING")
}

func TestListenFailure(t *testing.T) {
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

func TestBadFlags(t *testing.T) {
	for _, args := range [][]string{{"--no-such-flag"}, {"--addr"}, {"-x", "1"}} {
		r := evalkit.Run(t, bin, "", "", args...)
		if r.Code != 2 {
			t.Errorf("rotom %s: exit %d, want 2 (stderr: %s)", strings.Join(args, " "), r.Code, r.Stderr)
		}
	}
}

func TestSignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			s := startServer(t)
			busy := s.dial(t)
			busy.want(okReply, "SET", "k", "v")
			idle := s.dial(t)
			if err := s.cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			busy.closed()
			idle.closed()
			select {
			case <-s.exited:
			case <-time.After(10 * time.Second):
				t.Fatalf("server still running 10s after %v", sig)
			}
			if code := s.cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("exit code %d after %v, want 0 (stderr: %s)", code, sig, s.stderr)
			}
		})
	}
}

func TestConcurrentClients(t *testing.T) {
	s := startServer(t)
	const clients, rounds = 50, 20
	conns := make([]*client, clients)
	for i := range conns {
		conns[i] = s.dial(t)
	}
	var wg sync.WaitGroup
	for i, c := range conns {
		wg.Go(func() {
			key := fmt.Sprintf("client%d", i)
			if got, err := c.roundTrip("SET", key, key); err != nil || got != okReply {
				t.Errorf("SET %s: reply %q, error %v", key, got, err)
			}
			for range rounds {
				if got, err := c.roundTrip("INCR", "counter"); err != nil || !strings.HasPrefix(got, ":") {
					t.Errorf("INCR counter: reply %q, error %v", got, err)
					return
				}
			}
		})
	}
	wg.Wait()
	c := s.dial(t)
	c.want(bulk("1000"), "GET", "counter")
	for i := range clients {
		key := fmt.Sprintf("client%d", i)
		c.want(bulk(key), "GET", key)
	}
	c.want(integer(clients+1), "DBSIZE")
}

func TestPartialRequestDoesNotBlockOthers(t *testing.T) {
	s := startServer(t)
	slow := s.dial(t)
	slow.send("*2\r\n$4\r\nECHO\r\n$5\r\nhel")
	fast := s.dial(t)
	fast.want(pong, "PING")
	fast.want(okReply, "SET", "shared", "yes")
	slow.send("lo\r\n")
	slow.expect(bulk("hello"))
	slow.want(bulk("yes"), "GET", "shared")
}

func TestPipelining(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	var req strings.Builder
	var want []string
	for i := range 100 {
		k, v := fmt.Sprintf("key%d", i), fmt.Sprintf("value%d", i)
		n := int64(i) + 1
		req.WriteString(encode("SET", k, v))
		req.WriteString(encode("GET", k))
		req.WriteString("INCR n\r\n")
		want = append(want, okReply, bulk(v), integer(n))
	}
	c.send(req.String())
	for i, w := range want {
		if got := c.reply(); got != w {
			t.Fatalf("pipelined reply %d: %q, want %q", i, got, w)
		}
	}

	// One command split over many writes.
	raw := encode("SET", "split", "a\r\nb")
	for i := range len(raw) {
		c.send(raw[i : i+1])
	}
	c.expect(okReply)
	c.want(bulk("a\r\nb"), "GET", "split")
}

// Protocol

func TestInlineCommands(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	c.send("PING\r\n")
	c.expect(pong)
	c.send("ECHO hello\n")
	c.expect(bulk("hello"))
	c.send("set k v\r\n")
	c.expect(okReply)
	c.send("\r\n\n\r\nGET k\n")
	c.expect(bulk("v"))
	c.send("ping\r\n\nPING hi\r\nMGET k nope\r\n")
	c.expect(pong)
	c.expect(bulk("hi"))
	c.expect(array(bulk("v"), nullReply))
	c.send("\r\n\n")
	// The blank lines got no reply, so this reply comes next.
	c.want(bulk("end"), "ECHO", "end")
	c.send("nosuch a\r\n")
	c.expect(unknown("nosuch"))
	c.send("get\r\n")
	c.expect(wrongArgs("get"))
}

func TestBinarySafe(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	key, val := "k\x00\r\ney", "a\r\nb\x00c\r\n"
	c.want(okReply, "SET", key, val)
	c.want(bulk(val), "GET", key)
	c.want(integer(int64(len(val))), "STRLEN", key)
	c.want(nullReply, "GET", "k")
	c.want(bulk(val), "ECHO", val)
	c.want(array(bulk(key)), "KEYS", "k*")
	big := strings.Repeat("0123456789abcdef", 1<<16)
	c.want(okReply, "SET", "big", big)
	c.want(bulk(big), "GET", "big")
	c.want(integer(int64(len(big))), "STRLEN", "big")
}

func TestCaseRules(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	c.want(okReply, "sEt", "Key", "v1")
	c.want(bulk("v1"), "get", "Key")
	c.want(nullReply, "GET", "key")
	c.want(okReply, "set", "key", "v2")
	c.want(bulk("v1"), "Get", "Key")
	c.want(bulk("v2"), "GET", "key")
	c.want(pong, "pInG")
	c.want(bulk("MiXed"), "echo", "MiXed")
	c.want(integer(2), "dbsize")
}

func TestProtocolErrors(t *testing.T) {
	s := startServer(t)
	witness := s.dial(t)
	witness.want(okReply, "SET", "w", "1")
	cases := []struct {
		name, req, before string
	}{
		{"bad array length", "*x\r\n", ""},
		{"bad bulk length", "*1\r\n$x\r\n", ""},
		{"bulk length above 512 MiB", "*1\r\n$536870913\r\n", ""},
		{"no CRLF after bulk data", "*1\r\n$4\r\nPINGxx\r\n", ""},
		{"array element not a bulk string", "*1\r\n:1\r\n", ""},
		{"after a good command", "PING\r\n*1\r\n$y\r\n", pong},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := s.dial(t)
			c.send(tc.req)
			if tc.before != "" {
				c.expect(tc.before)
			}
			got := c.reply()
			reason, found := strings.CutPrefix(got, "-ERR Protocol error: ")
			if !found || strings.TrimSpace(reason) == "" {
				t.Errorf("request %q: reply %q, want \"-ERR Protocol error: \" and a reason", tc.req, got)
			}
			c.closed()
		})
	}
	// Other clients are not affected.
	witness.want(pong, "PING")
	witness.want(bulk("1"), "GET", "w")
}

func TestRequestCutShort(t *testing.T) {
	s := startServer(t)
	for _, tail := range []string{
		"*3\r\n",
		"*2\r\n$3\r\nGET\r\n$1\r\n",
		"*2\r\n$3\r\nGET\r\n$5\r\nab",
		"*1\r\n$4\r\nPI",
		"ECHO x",
	} {
		c := s.dial(t)
		c.send("PING\r\n" + tail)
		c.expect(pong)
		if err := c.conn.(*net.TCPConn).CloseWrite(); err != nil {
			t.Fatal(err)
		}
		// The server may close the connection or stay silent, but it must not reply.
		c.conn.SetReadDeadline(time.Now().Add(time.Second))
		if rest, _ := io.ReadAll(c.r); len(rest) > 0 {
			t.Errorf("after PING and %q, then close: got %q, want no more replies", tail, rest)
		}
	}
	s.dial(t).want(pong, "PING")
}

func TestErrorReplies(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	c.want(okReply, "SET", "k", "v")
	c.want(unknown("FOO"), "FOO")
	c.want(unknown("fOo"), "fOo", "a", "b")
	c.want(unknown("GETT"), "GETT", "k")
	// The first word is the name the error must use.
	for _, tc := range [][]string{
		{"get", "gEt"},
		{"get", "GET", "a", "b"},
		{"set", "SET"},
		{"set", "Set", "k"},
		{"ping", "PING", "a", "b"},
		{"echo", "ECHO"},
		{"echo", "ECHO", "a", "b"},
		{"mset", "MSET"},
		{"mset", "MSET", "a"},
		{"mset", "MSET", "a", "1", "b"},
		{"mget", "MGET"},
		{"del", "DEL"},
		{"exists", "EXISTS"},
		{"incr", "INCR"},
		{"incr", "INCR", "k", "k"},
		{"decr", "DECR"},
		{"incrby", "INCRBY", "k"},
		{"incrby", "INCRBY", "k", "1", "2"},
		{"decrby", "DECRBY", "k"},
		{"append", "APPEND", "k"},
		{"strlen", "STRLEN"},
		{"strlen", "STRLEN", "a", "b"},
		{"expire", "EXPIRE", "k"},
		{"pexpire", "PEXPIRE", "k"},
		{"ttl", "TTL"},
		{"pttl", "PTTL", "a", "b"},
		{"persist", "PERSIST"},
		{"keys", "KEYS"},
		{"keys", "KEYS", "a", "b"},
		{"type", "TYPE"},
		{"dbsize", "DBSIZE", "x"},
	} {
		c.want(wrongArgs(tc[0]), tc[1:]...)
	}
	// The failed commands changed nothing, and the connection still works.
	c.want(integer(0), "EXISTS", "a")
	c.want(bulk("v"), "GET", "k")
	c.want(integer(1), "DBSIZE")
	c.want(pong, "PING")
}

// Commands

func TestPingEchoCommandQuit(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	c.want(pong, "PING")
	c.want(bulk("hello world"), "PING", "hello world")
	c.want(bulk(""), "PING", "")
	c.want(bulk("hi"), "ECHO", "hi")
	c.want(bulk(""), "ECHO", "")
	c.want("*0\r\n", "COMMAND")
	c.want(okReply, "SET", "k", "v")
	c.want(okReply, "QUIT")
	c.closed()
	s.dial(t).want(bulk("v"), "GET", "k")
}

func TestSet(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	c.want(okReply, "SET", "k", "v")
	c.want(bulk("v"), "GET", "k")
	c.want(okReply, "SET", "k", "w")
	c.want(bulk("w"), "GET", "k")
	c.want(nullReply, "GET", "nope")

	c.want(nullReply, "SET", "k", "x", "NX")
	c.want(bulk("w"), "GET", "k")
	c.want(okReply, "SET", "n", "1", "NX")
	c.want(bulk("1"), "GET", "n")
	c.want(nullReply, "SET", "nope", "x", "XX")
	c.want(integer(0), "EXISTS", "nope")
	c.want(okReply, "SET", "k", "y", "XX")
	c.want(bulk("y"), "GET", "k")

	c.want(okReply, "SET", "k", "v", "EX", "100")
	c.wantInt(98, 100, "TTL", "k")
	c.wantInt(98000, 100000, "PTTL", "k")
	c.want(okReply, "SET", "k", "v", "PX", "50000")
	c.wantInt(48, 50, "TTL", "k")
	c.wantInt(48000, 50000, "PTTL", "k")
	// A SET without EX or PX removes the expiry.
	c.want(okReply, "SET", "k", "v2")
	c.want(integer(-1), "TTL", "k")
	c.want(bulk("v2"), "GET", "k")

	// Redis takes the options in any order and any case.
	c.want(okReply, "SET", "p", "1", "px", "100000", "nx")
	c.wantInt(98, 100, "TTL", "p")
	c.want(nullReply, "SET", "p", "2", "NX", "EX", "10")
	c.want(bulk("1"), "GET", "p")
	c.wantInt(98, 100, "TTL", "p")
	c.want(okReply, "SET", "p", "3", "Ex", "10", "xX")
	c.want(bulk("3"), "GET", "p")
	c.wantInt(8, 10, "TTL", "p")
	c.want(nullReply, "SET", "q", "1", "XX", "EX", "10")
	c.want(integer(0), "EXISTS", "q")

	for _, opts := range [][]string{
		{"EX", "10", "PX", "100"},
		{"PX", "100", "EX", "10"},
		{"NX", "XX"},
		{"XX", "NX"},
		{"EX"},
		{"PX"},
		{"BOGUS"},
		{"EX", "10", "KEEP"},
	} {
		c.want(errSyntax, append([]string{"SET", "k", "bad"}, opts...)...)
	}
	for _, opts := range [][]string{{"EX", "ten"}, {"PX", "1.5"}, {"EX", "99999999999999999999"}} {
		c.want(errInt, append([]string{"SET", "k", "bad"}, opts...)...)
	}
	// EX and PX need a positive integer. The spec does not fix this message.
	for _, opts := range [][]string{{"EX", "0"}, {"EX", "-1"}, {"PX", "0"}, {"PX", "-100"}} {
		if got := c.do("SET", "k", "bad", opts[0], opts[1]); !strings.HasPrefix(got, "-ERR ") {
			t.Errorf("SET k bad %s %s: reply %q, want an error", opts[0], opts[1], got)
		}
	}
	c.want(bulk("v2"), "GET", "k")
	c.want(integer(-1), "TTL", "k")
}

func TestMSetMGet(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	c.want(okReply, "MSET", "a", "1", "b", "2", "c", "3")
	c.want(array(bulk("1"), bulk("2"), nullReply, bulk("3")), "MGET", "a", "b", "nope", "c")
	c.want(array(bulk("1")), "MGET", "a")
	c.want(array(nullReply), "MGET", "nope")
	c.want(okReply, "MSET", "a", "x", "a", "y")
	c.want(bulk("y"), "GET", "a")
	// Redis MSET replaces the value and drops the expiry, as SET does.
	c.want(okReply, "SET", "t", "v", "EX", "100")
	c.want(okReply, "MSET", "t", "w")
	c.want(integer(-1), "TTL", "t")
	c.want(bulk("w"), "GET", "t")
}

func TestDelExists(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	c.want(okReply, "MSET", "a", "1", "b", "2", "c", "3")
	c.want(integer(3), "EXISTS", "a", "b", "c")
	c.want(integer(3), "EXISTS", "a", "a", "b", "nope")
	c.want(integer(0), "EXISTS", "nope")
	c.want(integer(2), "DEL", "a", "b", "nope")
	c.want(integer(0), "EXISTS", "a", "b")
	c.want(nullReply, "GET", "a")
	c.want(integer(1), "DEL", "c", "c")
	c.want(integer(0), "DEL", "c")
	c.want(integer(0), "DBSIZE")
}

func TestIncrDecr(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	c.want(integer(1), "INCR", "n")
	c.want(integer(2), "INCR", "n")
	c.want(integer(1), "DECR", "n")
	c.want(integer(11), "INCRBY", "n", "10")
	c.want(integer(-9), "DECRBY", "n", "20")
	c.want(integer(-10), "INCRBY", "n", "-1")
	c.want(integer(-5), "DECRBY", "n", "-5")
	c.want(bulk("-5"), "GET", "n")
	c.want(integer(-1), "DECR", "m")
	c.want(integer(5), "INCRBY", "o", "5")
	c.want(integer(-5), "DECRBY", "p", "5")
	c.want(okReply, "SET", "s", "42")
	c.want(integer(43), "INCR", "s")
	c.want(bulk("43"), "GET", "s")
	c.want("+string\r\n", "TYPE", "s")

	// A value or argument that is not an integer changes nothing.
	c.want(okReply, "SET", "w", "abc")
	for _, args := range [][]string{{"INCR", "w"}, {"DECR", "w"}, {"INCRBY", "w", "1"}, {"DECRBY", "w", "1"}} {
		c.want(errInt, args...)
	}
	c.want(bulk("abc"), "GET", "w")
	c.want(errInt, "INCRBY", "n", "x")
	c.want(errInt, "DECRBY", "n", "1.5")
	c.want(errInt, "INCRBY", "n", "9223372036854775808")
	c.want(bulk("-5"), "GET", "n")
	c.want(errInt, "INCRBY", "fresh", "x")
	c.want(integer(0), "EXISTS", "fresh")

	// A result that would overflow changes nothing.
	c.want(okReply, "SET", "max", "9223372036854775807")
	c.want(errInt, "INCR", "max")
	c.want(errInt, "INCRBY", "max", "1")
	c.want(errInt, "DECRBY", "max", "-1")
	c.want(bulk("9223372036854775807"), "GET", "max")
	c.want(integer(math.MaxInt64-1), "DECR", "max")
	c.want(okReply, "SET", "min", "-9223372036854775808")
	c.want(errInt, "DECR", "min")
	c.want(errInt, "INCRBY", "min", "-1")
	c.want(bulk("-9223372036854775808"), "GET", "min")
	c.want(errInt, "DECRBY", "zero", "-9223372036854775808")
	c.want(integer(0), "EXISTS", "zero")
	c.want(integer(math.MinInt64), "INCRBY", "low", "-9223372036854775808")
	c.want(integer(math.MaxInt64), "INCRBY", "high", "9223372036854775807")

	// These commands keep the expiry.
	c.want(okReply, "SET", "e", "5", "EX", "100")
	c.want(integer(6), "INCR", "e")
	c.want(integer(4), "DECRBY", "e", "2")
	c.want(integer(3), "DECR", "e")
	c.want(integer(8), "INCRBY", "e", "5")
	c.wantInt(98, 100, "TTL", "e")
}

// TestIntegerSyntaxMatchesRedis checks that integers are read as Redis 7
// reads them: an optional '-' and digits with no leading zero, nothing else.
func TestIntegerSyntaxMatchesRedis(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	for _, v := range []string{"+1", "01", "-0", " 1", "1 ", "", "1.0", "0x1", "1e3"} {
		c.want(okReply, "SET", "v", v)
		c.want(errInt, "INCR", "v")
		c.want(bulk(v), "GET", "v")
		c.want(errInt, "INCRBY", "n", v)
		c.want(errInt, "EXPIRE", "v", v)
	}
	c.want(integer(0), "EXISTS", "n")
	c.want(integer(-1), "TTL", "v")
	c.want(errInt, "SET", "v", "x", "EX", "+5")
	c.want(errInt, "SET", "v", "x", "PX", "05")
	c.want(okReply, "SET", "v", "0")
	c.want(integer(1), "INCR", "v")
	c.want(okReply, "SET", "v", "-10")
	c.want(integer(-9), "INCR", "v")
	c.want(integer(-19), "INCRBY", "v", "-10")
}

func TestAppendStrlen(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	c.want(integer(3), "APPEND", "k", "abc")
	c.want(integer(5), "APPEND", "k", "de")
	c.want(bulk("abcde"), "GET", "k")
	c.want(integer(5), "STRLEN", "k")
	c.want(integer(0), "STRLEN", "nope")
	c.want(integer(0), "APPEND", "empty", "")
	c.want(integer(1), "EXISTS", "empty")
	c.want(integer(3), "APPEND", "bin", "\x00\r\n")
	c.want(integer(6), "APPEND", "bin", "\x00\r\n")
	c.want(bulk("\x00\r\n\x00\r\n"), "GET", "bin")
	c.want(integer(6), "STRLEN", "bin")
	c.want(okReply, "SET", "u", "héllo")
	c.want(integer(6), "STRLEN", "u")
	// Redis APPEND changes the value in place and keeps the expiry.
	c.want(okReply, "SET", "t", "a", "EX", "100")
	c.want(integer(2), "APPEND", "t", "b")
	c.wantInt(98, 100, "TTL", "t")
}

func TestExpireTTLPersist(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	c.want(okReply, "SET", "k", "v")
	c.want(integer(-1), "TTL", "k")
	c.want(integer(-1), "PTTL", "k")
	c.want(integer(-2), "TTL", "nope")
	c.want(integer(-2), "PTTL", "nope")
	c.want(integer(1), "EXPIRE", "k", "100")
	c.wantInt(98, 100, "TTL", "k")
	c.wantInt(98000, 100000, "PTTL", "k")
	c.want(integer(1), "PEXPIRE", "k", "50000")
	c.wantInt(48, 50, "TTL", "k")
	c.wantInt(48000, 50000, "PTTL", "k")
	c.want(bulk("v"), "GET", "k")
	c.want(integer(0), "EXPIRE", "nope", "10")
	c.want(integer(0), "PEXPIRE", "nope", "10")
	c.want(integer(0), "EXISTS", "nope")
	c.want(integer(1), "PERSIST", "k")
	c.want(integer(-1), "TTL", "k")
	c.want(integer(0), "PERSIST", "k")
	c.want(integer(0), "PERSIST", "nope")
	c.want(errInt, "EXPIRE", "k", "soon")
	c.want(errInt, "PEXPIRE", "k", "1.5")
	c.want(integer(-1), "TTL", "k")

	// A zero or negative time deletes the key at once.
	for i, args := range [][]string{{"EXPIRE", "0"}, {"EXPIRE", "-10"}, {"PEXPIRE", "0"}, {"PEXPIRE", "-1"}} {
		key := fmt.Sprintf("d%d", i)
		c.want(okReply, "SET", key, "v")
		c.want(integer(1), args[0], key, args[1])
		c.want(nullReply, "GET", key)
		c.want(integer(0), "EXISTS", key)
		c.want(integer(-2), "TTL", key)
	}
	c.want(integer(1), "DBSIZE")
}

func TestTTLRounding(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	// Each TTL runs well under 400 ms after its expiry was set.
	c.want(okReply, "SET", "a", "v", "PX", "2400")
	c.want(integer(2), "TTL", "a")
	c.want(okReply, "SET", "b", "v", "PX", "2900")
	c.want(integer(3), "TTL", "b")
	c.want(integer(1), "PEXPIRE", "a", "1300")
	c.want(integer(1), "TTL", "a")
	c.want(integer(1), "PEXPIRE", "a", "1800")
	c.want(integer(2), "TTL", "a")
}

func TestExpiredKeysAreMissing(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	c.want(okReply, "SET", "keep", "v")
	expireSoon := func() {
		t.Helper()
		c.want(okReply, "SET", "s", "v", "PX", "100")
		c.want(okReply, "SET", "n", "abc", "PX", "100")
		c.want(okReply, "SET", "a", "abc", "PX", "100")
		c.want(okReply, "SET", "e", "v")
		c.want(integer(1), "PEXPIRE", "e", "100")
	}

	expireSoon()
	c.want(integer(5), "DBSIZE")
	c.want(bulk("v"), "GET", "s")
	time.Sleep(300 * time.Millisecond)
	c.want(integer(1), "DBSIZE")
	c.want(array(bulk("keep")), "KEYS", "*")
	c.want(nullReply, "GET", "s")
	c.want(array(nullReply, bulk("v")), "MGET", "e", "keep")
	c.want(integer(1), "EXISTS", "n", "e", "keep")
	c.want(integer(-2), "TTL", "s")
	c.want(integer(-2), "PTTL", "e")
	c.want("+none\r\n", "TYPE", "n")
	c.want(integer(0), "STRLEN", "a")
	c.want(integer(0), "PERSIST", "e")
	c.want(integer(0), "EXPIRE", "a", "100")
	c.want(integer(0), "DEL", "s", "n")

	// Writes treat an expired key as missing too.
	expireSoon()
	time.Sleep(300 * time.Millisecond)
	c.want(nullReply, "SET", "s", "x", "XX")
	c.want(integer(0), "EXISTS", "s")
	c.want(okReply, "SET", "e", "x", "NX")
	c.want(integer(-1), "TTL", "e")
	c.want(integer(1), "INCR", "n")
	c.want(integer(-1), "TTL", "n")
	c.want(integer(2), "APPEND", "a", "xy")
	c.want(bulk("xy"), "GET", "a")
	c.want(integer(-1), "TTL", "a")
	c.want(integer(4), "DBSIZE")
}

func TestKeys(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	c.want(array(), "KEYS", "*")
	all := []string{"hello", "hallo", "hxllo", "hllo", "heeello", "a*b", "a?b", "axb", "a[b", "other"}
	for _, k := range all {
		c.want(okReply, "SET", k, "1")
	}
	hs := []string{"hello", "hallo", "hxllo", "hllo", "heeello"}
	cases := []struct {
		pattern string
		want    []string
	}{
		{"*", all},
		{"h*", hs},
		{"h*llo", hs},
		{"*o", hs},
		{"h?llo", []string{"hello", "hallo", "hxllo"}},
		{"h[ae]llo", []string{"hello", "hallo"}},
		{"h[^e]llo", []string{"hallo", "hxllo"}},
		{"h[a-e]llo", []string{"hello", "hallo"}},
		{"h[a-c]llo", []string{"hallo"}},
		{"a?b", []string{"a*b", "a?b", "axb", "a[b"}},
		{`a\*b`, []string{"a*b"}},
		{`a\?b`, []string{"a?b"}},
		{`a\[b`, []string{"a[b"}},
		{"hello", []string{"hello"}},
		{"HELLO", nil},
		{"h", nil},
		{"nomatch*", nil},
	}
	for _, tc := range cases {
		raw := c.do("KEYS", tc.pattern)
		got, err := elems(raw)
		if err != nil {
			t.Errorf("KEYS %s: %v", tc.pattern, err)
			continue
		}
		var want []string
		for _, k := range tc.want {
			want = append(want, bulk(k))
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("KEYS %s: reply %q, want the keys %q in any order", tc.pattern, raw, tc.want)
		}
	}
}

func TestTypeDbsizeFlushall(t *testing.T) {
	s := startServer(t)
	c := s.dial(t)
	c.want("+none\r\n", "TYPE", "k")
	c.want(integer(0), "DBSIZE")
	c.want(okReply, "SET", "k", "v")
	c.want("+string\r\n", "TYPE", "k")
	c.want(okReply, "MSET", "a", "1", "b", "2")
	c.want(integer(3), "DBSIZE")
	other := s.dial(t)
	other.want(integer(3), "DBSIZE")
	other.want(bulk("v"), "GET", "k")
	c.want(okReply, "FLUSHALL")
	other.want(integer(0), "DBSIZE")
	other.want(nullReply, "GET", "k")
	c.want(array(), "KEYS", "*")
	c.want(okReply, "SET", "k", "again")
	c.want(integer(1), "DBSIZE")
}

// Compatibility

func TestRedisCLI(t *testing.T) {
	cli, err := exec.LookPath("redis-cli")
	if err != nil {
		t.Fatalf("the spec names redis-cli, which is not on PATH: %v", err)
	}
	s := startServer(t)
	_, port, err := net.SplitHostPort(s.addr)
	if err != nil {
		t.Fatal(err)
	}
	run := func(want string, args ...string) {
		t.Helper()
		r := evalkit.Run(t, cli, "", "", append([]string{"-p", port}, args...)...)
		if r.Code != 0 || strings.TrimRight(r.Stdout, "\n") != want {
			t.Errorf("redis-cli -p PORT %s: exit %d, stdout %q, want %q (stderr: %s)", strings.Join(args, " "), r.Code, r.Stdout, want, r.Stderr)
		}
	}
	run("OK", "SET", "k", "v")
	run("v", "GET", "k")
	run("PONG", "PING")
	run("1", "INCR", "n")
	run("2", "EXISTS", "k", "n")
}
