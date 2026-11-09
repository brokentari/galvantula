// Command rotom is the reference answer for the rotom eval: a RESP2 server
// that keeps strings in memory.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:6379", "listen address")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rotom:", err)
		os.Exit(1)
	}
	fmt.Println("ready", ln.Addr())

	db := &store{m: map[string]item{}}
	go func() {
		// Lookups drop expired keys; this drops the ones nobody asks for.
		for range time.Tick(time.Second) {
			db.sweep()
		}
	}()

	var mu sync.Mutex
	conns := map[net.Conn]bool{}
	go func() {
		for {
			conn, err := ln.Accept()
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if err != nil {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			mu.Lock()
			conns[conn] = true
			mu.Unlock()
			go func() {
				db.serve(conn)
				mu.Lock()
				delete(conns, conn)
				mu.Unlock()
			}()
		}
	}()

	<-ctx.Done()
	ln.Close()
	mu.Lock()
	defer mu.Unlock()
	for conn := range conns {
		conn.Close()
	}
}

// serve answers the commands on one connection until it closes.
func (db *store) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReaderSize(conn, 64<<10)
	for {
		args, err := readCommand(r)
		if pe, ok := errors.AsType[protoError](err); ok {
			io.WriteString(conn, "-ERR Protocol error: "+string(pe)+"\r\n")
			return
		}
		if err != nil {
			return // the client closed the connection, maybe in the middle of a request
		}
		if len(args) == 0 {
			continue
		}
		reply, quit := db.exec(args)
		if _, err := io.WriteString(conn, reply); err != nil || quit {
			return
		}
	}
}

type protoError string

func (e protoError) Error() string { return "protocol error: " + string(e) }

const maxBulk = 512 << 20

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return "", protoError("too big request line")
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"), nil
}

// readCommand reads one request. An empty result means a blank inline line
// or an empty array, which get no reply.
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
	if err != nil {
		return nil, protoError("invalid multibulk length")
	}
	args := make([]string, 0, min(max(n, 0), 64))
	for range n {
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		length, isBulk := strings.CutPrefix(line, "$")
		if !isBulk {
			return nil, protoError("expected '$'")
		}
		size, err := strconv.Atoi(length)
		if err != nil || size < 0 || size > maxBulk {
			return nil, protoError("invalid bulk length")
		}
		// The buffer grows as data arrives, so a big length alone costs nothing.
		var b bytes.Buffer
		if _, err := io.CopyN(&b, r, int64(size)+2); err != nil {
			return nil, err
		}
		data := b.Bytes()
		if !bytes.HasSuffix(data, []byte("\r\n")) {
			return nil, protoError("expected CRLF after bulk data")
		}
		args = append(args, string(data[:size]))
	}
	return args, nil
}

const (
	okReply   = "+OK\r\n"
	nullReply = "$-1\r\n"
	errInt    = "-ERR value is not an integer or out of range\r\n"
	errSyntax = "-ERR syntax error\r\n"
)

func bulk(s string) string { return "$" + strconv.Itoa(len(s)) + "\r\n" + s + "\r\n" }

func integer(n int64) string { return ":" + strconv.FormatInt(n, 10) + "\r\n" }

func array(items []string) string {
	return "*" + strconv.Itoa(len(items)) + "\r\n" + strings.Join(items, "")
}

func wrongArgs(name string) string {
	return "-ERR wrong number of arguments for '" + name + "' command\r\n"
}

var lineBreaks = strings.NewReplacer("\r", " ", "\n", " ")

type command struct {
	// arity counts the command name, as in Redis: n means exactly n, -n at least n.
	arity int
	run   func(db *store, a []string) string
}

var commands = map[string]command{
	"ping":     {-1, pingCmd},
	"echo":     {2, echoCmd},
	"command":  {-1, commandCmd},
	"set":      {-3, setCmd},
	"get":      {2, getCmd},
	"mset":     {-3, msetCmd},
	"mget":     {-2, mgetCmd},
	"del":      {-2, delCmd},
	"exists":   {-2, existsCmd},
	"incr":     {2, incrCmd},
	"decr":     {2, decrCmd},
	"incrby":   {3, incrbyCmd},
	"decrby":   {3, decrbyCmd},
	"append":   {3, appendCmd},
	"strlen":   {2, strlenCmd},
	"expire":   {-3, expireCmd},
	"pexpire":  {-3, pexpireCmd},
	"ttl":      {2, ttlCmd},
	"pttl":     {2, pttlCmd},
	"persist":  {2, persistCmd},
	"keys":     {2, keysCmd},
	"type":     {2, typeCmd},
	"dbsize":   {1, dbsizeCmd},
	"flushall": {-1, flushallCmd},
}

// exec runs one command and reports whether the connection must close.
func (db *store) exec(args []string) (string, bool) {
	name := strings.ToLower(args[0])
	if name == "quit" {
		return okReply, true
	}
	cmd, found := commands[name]
	switch {
	case !found:
		return "-ERR unknown command '" + lineBreaks.Replace(args[0]) + "'\r\n", false
	case cmd.arity > 0 && len(args) != cmd.arity, cmd.arity < 0 && len(args) < -cmd.arity:
		return wrongArgs(name), false
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	return cmd.run(db, args[1:]), false
}

type item struct {
	val string
	// exp is the expiry as Unix time in milliseconds, or 0 for none.
	exp int64
}

func (it item) expired(now int64) bool { return it.exp != 0 && it.exp <= now }

type store struct {
	mu sync.Mutex
	m  map[string]item
}

// lookup returns the live item for key and drops it if it has expired.
// The caller holds db.mu.
func (db *store) lookup(key string) (item, bool) {
	it, ok := db.m[key]
	if ok && it.expired(time.Now().UnixMilli()) {
		delete(db.m, key)
		return item{}, false
	}
	return it, ok
}

func (db *store) sweep() {
	db.mu.Lock()
	defer db.mu.Unlock()
	now := time.Now().UnixMilli()
	maps.DeleteFunc(db.m, func(_ string, it item) bool { return it.expired(now) })
}

// parseInt accepts what Redis accepts: an optional '-', then digits with no
// leading zero. It rejects '+', spaces and "-0".
func parseInt(s string) (int64, bool) {
	digits := strings.TrimPrefix(s, "-")
	if s != "0" && (digits == "" || digits[0] < '1' || digits[0] > '9') {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// expiry returns the Unix time in ms that is n units of per ms from now.
// n must be positive. It reports false on overflow.
func expiry(n, per int64) (int64, bool) {
	now := time.Now().UnixMilli()
	limit := (math.MaxInt64 - now) / per
	if n > limit {
		return 0, false
	}
	return now + n*per, true
}

func pingCmd(_ *store, a []string) string {
	switch len(a) {
	case 0:
		return "+PONG\r\n"
	case 1:
		return bulk(a[0])
	}
	return wrongArgs("ping")
}

func echoCmd(_ *store, a []string) string { return bulk(a[0]) }

func commandCmd(*store, []string) string { return "*0\r\n" }

func setCmd(db *store, a []string) string {
	var nx, xx bool
	var unit string
	var exp int64
	for i := 2; i < len(a); i++ {
		opt := strings.ToUpper(a[i])
		switch {
		case opt == "NX" && !xx:
			nx = true
		case opt == "XX" && !nx:
			xx = true
		case (opt == "EX" || opt == "PX") && (unit == "" || unit == opt) && i+1 < len(a):
			i++
			n, ok := parseInt(a[i])
			if !ok || n <= 0 {
				return errInt
			}
			per := int64(1)
			if opt == "EX" {
				per = 1000
			}
			if exp, ok = expiry(n, per); !ok {
				return errInt
			}
			unit = opt
		default:
			return errSyntax
		}
	}
	_, exists := db.lookup(a[0])
	if nx && exists || xx && !exists {
		return nullReply
	}
	db.m[a[0]] = item{a[1], exp}
	return okReply
}

func getCmd(db *store, a []string) string {
	if it, ok := db.lookup(a[0]); ok {
		return bulk(it.val)
	}
	return nullReply
}

func msetCmd(db *store, a []string) string {
	if len(a)%2 != 0 {
		return wrongArgs("mset")
	}
	for i := 0; i < len(a); i += 2 {
		db.m[a[i]] = item{val: a[i+1]}
	}
	return okReply
}

func mgetCmd(db *store, a []string) string {
	out := make([]string, 0, len(a))
	for _, k := range a {
		if it, ok := db.lookup(k); ok {
			out = append(out, bulk(it.val))
		} else {
			out = append(out, nullReply)
		}
	}
	return array(out)
}

func delCmd(db *store, a []string) string {
	var n int64
	for _, k := range a {
		if _, ok := db.lookup(k); ok {
			delete(db.m, k)
			n++
		}
	}
	return integer(n)
}

func existsCmd(db *store, a []string) string {
	var n int64
	for _, k := range a {
		if _, ok := db.lookup(k); ok {
			n++
		}
	}
	return integer(n)
}

func incrCmd(db *store, a []string) string { return db.incrBy(a[0], 1) }

func decrCmd(db *store, a []string) string { return db.incrBy(a[0], -1) }

func incrbyCmd(db *store, a []string) string {
	n, ok := parseInt(a[1])
	if !ok {
		return errInt
	}
	return db.incrBy(a[0], n)
}

func decrbyCmd(db *store, a []string) string {
	n, ok := parseInt(a[1])
	if !ok || n == math.MinInt64 {
		return errInt
	}
	return db.incrBy(a[0], -n)
}

// incrBy adds by to the integer at key and keeps the expiry.
func (db *store) incrBy(key string, by int64) string {
	it, exists := db.lookup(key)
	var cur int64
	if exists {
		var ok bool
		if cur, ok = parseInt(it.val); !ok {
			return errInt
		}
	}
	sum := cur + by
	if by > 0 && sum < cur || by < 0 && sum > cur {
		return errInt
	}
	it.val = strconv.FormatInt(sum, 10)
	db.m[key] = it
	return integer(sum)
}

func appendCmd(db *store, a []string) string {
	it, _ := db.lookup(a[0])
	it.val += a[1]
	db.m[a[0]] = it
	return integer(int64(len(it.val)))
}

func strlenCmd(db *store, a []string) string {
	it, _ := db.lookup(a[0])
	return integer(int64(len(it.val)))
}

func expireCmd(db *store, a []string) string { return db.expire(a, 1000) }

func pexpireCmd(db *store, a []string) string { return db.expire(a, 1) }

func (db *store) expire(a []string, per int64) string {
	n, ok := parseInt(a[1])
	if !ok {
		return errInt
	}
	if len(a) > 2 {
		return errSyntax // NX, XX, GT and LT are not in the spec
	}
	it, exists := db.lookup(a[0])
	if !exists {
		return integer(0)
	}
	if n <= 0 {
		delete(db.m, a[0])
		return integer(1)
	}
	if it.exp, ok = expiry(n, per); !ok {
		return errInt
	}
	db.m[a[0]] = it
	return integer(1)
}

func ttlCmd(db *store, a []string) string { return db.ttl(a[0], 1000) }

func pttlCmd(db *store, a []string) string { return db.ttl(a[0], 1) }

func (db *store) ttl(key string, per int64) string {
	it, ok := db.lookup(key)
	switch {
	case !ok:
		return integer(-2)
	case it.exp == 0:
		return integer(-1)
	}
	left := max(it.exp-time.Now().UnixMilli(), 0)
	rounded := (left + per/2) / per
	return integer(rounded)
}

func persistCmd(db *store, a []string) string {
	it, ok := db.lookup(a[0])
	if !ok || it.exp == 0 {
		return integer(0)
	}
	it.exp = 0
	db.m[a[0]] = it
	return integer(1)
}

func keysCmd(db *store, a []string) string {
	var out []string
	for k := range db.m {
		if _, ok := db.lookup(k); ok && match(a[0], k) {
			out = append(out, bulk(k))
		}
	}
	return array(out)
}

func typeCmd(db *store, a []string) string {
	if _, ok := db.lookup(a[0]); ok {
		return "+string\r\n"
	}
	return "+none\r\n"
}

func dbsizeCmd(db *store, _ []string) string {
	var n int64
	for k := range db.m {
		if _, ok := db.lookup(k); ok {
			n++
		}
	}
	return integer(n)
}

func flushallCmd(db *store, _ []string) string {
	clear(db.m)
	return okReply
}

// match reports whether s matches the glob pattern p, with the rules of
// Redis KEYS: *, ?, [abc], [a-c], [^a] and \ to escape.
func match(p, s string) bool {
	for len(p) > 0 {
		switch p[0] {
		case '*':
			for len(p) > 0 && p[0] == '*' {
				p = p[1:]
			}
			if len(p) == 0 {
				return true
			}
			for i := range len(s) + 1 {
				if match(p, s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			p, s = p[1:], s[1:]
		case '[':
			if len(s) == 0 {
				return false
			}
			p = p[1:]
			negate := len(p) > 0 && p[0] == '^'
			if negate {
				p = p[1:]
			}
			found := false
			for len(p) > 0 && p[0] != ']' {
				switch {
				case p[0] == '\\' && len(p) > 1:
					found = found || p[1] == s[0]
					p = p[2:]
				case len(p) > 2 && p[1] == '-':
					lo, hi := min(p[0], p[2]), max(p[0], p[2])
					found = found || lo <= s[0] && s[0] <= hi
					p = p[3:]
				default:
					found = found || p[0] == s[0]
					p = p[1:]
				}
			}
			p = strings.TrimPrefix(p, "]")
			if found == negate {
				return false
			}
			s = s[1:]
		case '\\':
			if len(p) > 1 {
				p = p[1:]
			}
			fallthrough
		default:
			if len(s) == 0 || p[0] != s[0] {
				return false
			}
			p, s = p[1:], s[1:]
		}
	}
	return len(s) == 0
}
