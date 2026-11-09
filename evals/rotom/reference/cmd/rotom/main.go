// Command rotom is the reference answer for the rotom eval: a RESP2 server
// that keeps strings in memory. It exists only to prove the hidden tests right.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:6379", "listen address; port 0 picks a free port")
	flag.Parse()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rotom:", err)
		os.Exit(1)
	}
	fmt.Println("ready", ln.Addr())

	s := newStore()
	var mu sync.Mutex
	conns := map[net.Conn]bool{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				// shortcut: any accept error stops accepting; retry temporary errors if this serves real load
				return
			}
			mu.Lock()
			conns[c] = true
			mu.Unlock()
			go func() {
				s.serve(c)
				mu.Lock()
				delete(conns, c)
				mu.Unlock()
			}()
		}
	}()
	<-stop
	ln.Close()
	mu.Lock()
	for c := range conns {
		c.Close()
	}
	mu.Unlock()
}

// flushReader sends the buffered replies before each wait for more input,
// so pipelined replies go out together and no reply waits behind a read.
type flushReader struct {
	c net.Conn
	w *bufio.Writer
}

func (f flushReader) Read(p []byte) (int, error) {
	if err := f.w.Flush(); err != nil {
		return 0, err
	}
	return f.c.Read(p)
}

func (s *store) serve(c net.Conn) {
	defer c.Close()
	w := bufio.NewWriter(c)
	defer w.Flush()
	r := bufio.NewReader(flushReader{c, w})
	for {
		a, err := readCommand(r)
		if reason, bad := errors.AsType[protoError](err); bad {
			w.WriteString("-ERR Protocol error: " + string(reason) + "\r\n")
		}
		if err != nil {
			return
		}
		if len(a) == 0 {
			continue
		}
		w.WriteString(s.exec(a))
		if strings.EqualFold(a[0], "quit") {
			return
		}
	}
}

// protoError is a malformed request. The server sends it and closes the
// connection.
type protoError string

func (e protoError) Error() string {
	return string(e)
}

const maxBulk = 512 << 20

// readCommand reads one request. A blank inline line gives no arguments.
// Any other error means the client closed the connection mid-request.
func readCommand(r *bufio.Reader) ([]string, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	count, isArray := strings.CutPrefix(line, "*")
	if !isArray {
		return strings.Fields(line), nil
	}
	n, err := strconv.Atoi(count)
	if err != nil || n < 0 {
		return nil, protoError("invalid multibulk length")
	}
	var args []string
	for range n {
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		size, isBulk := strings.CutPrefix(line, "$")
		l, err := strconv.Atoi(size)
		if !isBulk || err != nil || l < 0 || l > maxBulk {
			return nil, protoError("invalid bulk length")
		}
		// CopyN grows the buffer with the data that arrives, not with the length the client claims.
		var b strings.Builder
		if _, err := io.CopyN(&b, r, int64(l)+2); err != nil {
			return nil, err
		}
		arg, hasCRLF := strings.CutSuffix(b.String(), "\r\n")
		if !hasCRLF {
			return nil, protoError("expected CRLF after bulk string")
		}
		args = append(args, arg)
	}
	return args, nil
}

// readLine reads a line that ends in "\n" or "\r\n" and drops the ending.
func readLine(r *bufio.Reader) (string, error) {
	// shortcut: a line has no length limit; cap it if untrusted clients connect
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(line[:len(line)-1], "\r"), nil
}

type entry struct {
	val string
	exp time.Time // zero means no expiry
}

func (e entry) expired(now time.Time) bool {
	return !e.exp.IsZero() && !now.Before(e.exp)
}

// store holds the keys. One mutex guards the map, and every command holds it.
type store struct {
	mu sync.Mutex
	m  map[string]entry
}

func newStore() *store {
	s := &store{m: map[string]entry{}}
	go func() {
		for range time.Tick(time.Second) {
			s.mu.Lock()
			now := time.Now()
			maps.DeleteFunc(s.m, func(_ string, e entry) bool { return e.expired(now) })
			s.mu.Unlock()
		}
	}()
	return s
}

// get returns the live entry for key. It deletes the key if it has expired.
func (s *store) get(key string) (entry, bool) {
	e, found := s.m[key]
	if found && e.expired(time.Now()) {
		delete(s.m, key)
		return entry{}, false
	}
	return e, found
}

func (s *store) value(key string) string {
	e, found := s.get(key)
	if !found {
		return replyNull
	}
	return bulk(e.val)
}

type command struct {
	name     string
	min, max int
}

// commands lists each command with its least and greatest argument count,
// name included. A max of 0 means no limit.
var commands = []command{
	{"ping", 1, 2}, {"echo", 2, 2}, {"quit", 1, 0}, {"command", 1, 0},
	{"set", 3, 0}, {"get", 2, 2}, {"mset", 3, 0}, {"mget", 2, 0},
	{"del", 2, 0}, {"exists", 2, 0}, {"incr", 2, 2}, {"decr", 2, 2},
	{"incrby", 3, 3}, {"decrby", 3, 3}, {"append", 3, 3}, {"strlen", 2, 2},
	{"expire", 3, 3}, {"pexpire", 3, 3}, {"ttl", 2, 2}, {"pttl", 2, 2},
	{"persist", 2, 2}, {"keys", 2, 2}, {"type", 2, 2}, {"dbsize", 1, 1},
	{"flushall", 1, 1},
}

func (s *store) exec(a []string) string {
	name := strings.ToLower(a[0])
	i := slices.IndexFunc(commands, func(c command) bool { return c.name == name })
	if i < 0 {
		return "-ERR unknown command '" + a[0] + "'\r\n"
	}
	if c := commands[i]; len(a) < c.min || (c.max > 0 && len(a) > c.max) {
		return wrongArgs(name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch name {
	case "ping":
		if len(a) == 2 {
			return bulk(a[1])
		}
		return "+PONG\r\n"
	case "echo":
		return bulk(a[1])
	case "quit":
		return replyOK
	case "command":
		return "*0\r\n"
	case "set":
		return s.set(a)
	case "get":
		return s.value(a[1])
	case "mset":
		if len(a)%2 == 0 {
			return wrongArgs(name)
		}
		for i := 1; i < len(a); i += 2 {
			s.m[a[i]] = entry{val: a[i+1]}
		}
		return replyOK
	case "mget":
		out := make([]string, 0, len(a)-1)
		for _, k := range a[1:] {
			out = append(out, s.value(k))
		}
		return array(out)
	case "del":
		n := 0
		for _, k := range a[1:] {
			if _, found := s.get(k); found {
				delete(s.m, k)
				n++
			}
		}
		return integer(n)
	case "exists":
		n := 0
		for _, k := range a[1:] {
			if _, found := s.get(k); found {
				n++
			}
		}
		return integer(n)
	case "incr", "incrby":
		return s.add(a, false)
	case "decr", "decrby":
		return s.add(a, true)
	case "append":
		e, _ := s.get(a[1])
		e.val += a[2]
		s.m[a[1]] = e
		return integer(len(e.val))
	case "strlen":
		e, _ := s.get(a[1])
		return integer(len(e.val))
	case "expire":
		return s.expire(a, time.Second)
	case "pexpire":
		return s.expire(a, time.Millisecond)
	case "ttl":
		return s.ttl(a[1], time.Second)
	case "pttl":
		return s.ttl(a[1], time.Millisecond)
	case "persist":
		e, found := s.get(a[1])
		if !found || e.exp.IsZero() {
			return integer(0)
		}
		e.exp = time.Time{}
		s.m[a[1]] = e
		return integer(1)
	case "keys":
		var out []string
		for k := range s.m {
			if _, found := s.get(k); found && match(a[1], k) {
				out = append(out, bulk(k))
			}
		}
		return array(out)
	case "type":
		if _, found := s.get(a[1]); found {
			return "+string\r\n"
		}
		return "+none\r\n"
	case "dbsize":
		n := 0
		for k := range s.m {
			if _, found := s.get(k); found {
				n++
			}
		}
		return integer(n)
	default: // flushall, the last name in the table
		clear(s.m)
		return replyOK
	}
}

func (s *store) set(a []string) string {
	var ttl time.Duration
	var nx, xx bool
	for i := 3; i < len(a); i++ {
		switch opt := strings.ToUpper(a[i]); opt {
		case "NX":
			nx = true
		case "XX":
			xx = true
		case "EX", "PX":
			if ttl != 0 || i+1 == len(a) {
				return errSyntax
			}
			i++
			unit := time.Second
			if opt == "PX" {
				unit = time.Millisecond
			}
			d, valid := duration(a[i], unit)
			if !valid || d <= 0 {
				return errInt
			}
			ttl = d
		default:
			return errSyntax
		}
	}
	if nx && xx {
		return errSyntax
	}
	if _, found := s.get(a[1]); (nx && found) || (xx && !found) {
		return replyNull
	}
	e := entry{val: a[2]}
	if ttl > 0 {
		e.exp = time.Now().Add(ttl)
	}
	s.m[a[1]] = e
	return replyOK
}

// add adds the step in a[2], or 1 without one, to the integer at key a[1].
// With sub it subtracts the step.
func (s *store) add(a []string, sub bool) string {
	step := int64(1)
	if len(a) == 3 {
		var err error
		if step, err = strconv.ParseInt(a[2], 10, 64); err != nil {
			return errInt
		}
	}
	e, found := s.get(a[1])
	var n int64
	if found {
		var err error
		if n, err = strconv.ParseInt(e.val, 10, 64); err != nil {
			return errInt
		}
	}
	r := n + step
	fits := (r > n) == (step > 0)
	if sub {
		r = n - step
		fits = (r < n) == (step > 0)
	}
	if !fits {
		return errInt
	}
	e.val = strconv.FormatInt(r, 10)
	s.m[a[1]] = e
	return integer(r)
}

func (s *store) expire(a []string, unit time.Duration) string {
	d, valid := duration(a[2], unit)
	if !valid {
		return errInt
	}
	e, found := s.get(a[1])
	switch {
	case !found:
		return integer(0)
	case d <= 0:
		delete(s.m, a[1])
	default:
		e.exp = time.Now().Add(d)
		s.m[a[1]] = e
	}
	return integer(1)
}

func (s *store) ttl(key string, unit time.Duration) string {
	e, found := s.get(key)
	switch {
	case !found:
		return integer(-2)
	case e.exp.IsZero():
		return integer(-1)
	}
	return integer(int64(time.Until(e.exp).Round(unit) / unit))
}

// duration parses n as a count of unit. It reports false if n is not an
// integer or the duration does not fit in a time.Duration.
func duration(n string, unit time.Duration) (time.Duration, bool) {
	v, err := strconv.ParseInt(n, 10, 64)
	limit := int64(math.MaxInt64 / unit)
	if err != nil || v > limit || v < -limit {
		return 0, false
	}
	return time.Duration(v) * unit, true
}

// match reports whether s matches the glob pattern p. It supports *, ?,
// [abc], [a-c], [^a] and \ to escape a character.
func match(p, s string) bool {
	for p != "" {
		switch p[0] {
		case '*':
			p = strings.TrimLeft(p, "*")
			if p == "" {
				return true
			}
			for i := range len(s) + 1 {
				if match(p, s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if s == "" {
				return false
			}
			p, s = p[1:], s[1:]
		case '[':
			if s == "" {
				return false
			}
			hit, rest := class(p[1:], s[0])
			if !hit {
				return false
			}
			p, s = rest, s[1:]
		case '\\':
			if len(p) > 1 {
				p = p[1:]
			}
			fallthrough
		default:
			if s == "" || p[0] != s[0] {
				return false
			}
			p, s = p[1:], s[1:]
		}
	}
	return s == ""
}

// class matches c against the set that starts after a '[' in p. It also
// returns the rest of p after the closing ']'.
func class(p string, c byte) (bool, string) {
	p, negate := strings.CutPrefix(p, "^")
	hit := false
	for p != "" && p[0] != ']' {
		lo := p[0]
		if lo == '\\' && len(p) > 1 {
			p = p[1:]
			lo = p[0]
		}
		p = p[1:]
		hi := lo
		if len(p) > 1 && p[0] == '-' && p[1] != ']' {
			hi, p = p[1], p[2:]
		}
		if min(lo, hi) <= c && c <= max(lo, hi) {
			hit = true
		}
	}
	return hit != negate, strings.TrimPrefix(p, "]")
}

const (
	replyOK   = "+OK\r\n"
	replyNull = "$-1\r\n"
	errInt    = "-ERR value is not an integer or out of range\r\n"
	errSyntax = "-ERR syntax error\r\n"
)

func bulk(s string) string {
	return "$" + strconv.Itoa(len(s)) + "\r\n" + s + "\r\n"
}

func integer[T int | int64](n T) string {
	return ":" + strconv.FormatInt(int64(n), 10) + "\r\n"
}

func array(items []string) string {
	return "*" + strconv.Itoa(len(items)) + "\r\n" + strings.Join(items, "")
}

func wrongArgs(name string) string {
	return "-ERR wrong number of arguments for '" + name + "' command\r\n"
}
