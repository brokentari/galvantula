// Command calc evaluates arithmetic expressions, one per argument or one per
// line of standard input, and prints one result per line.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

func main() {
	flag.Parse() // no flags are defined, so any flag exits with status 2
	failed := false
	run := func(expr string) {
		v, err := eval(expr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", expr, err)
			failed = true
			return
		}
		fmt.Println(format(v))
	}
	if flag.NArg() > 0 {
		for _, expr := range flag.Args() {
			run(expr)
		}
	} else {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			if line := sc.Text(); strings.TrimSpace(line) != "" {
				run(line)
			}
		}
		if err := sc.Err(); err != nil {
			fmt.Fprintln(os.Stderr, "calc:", err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

// format prints whole numbers without a decimal point and other numbers in
// the shortest form that parses back to the same value.
func format(v float64) string {
	if v == 0 {
		return "0" // also for -0
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

var errDivZero = errors.New("division by zero")

type syntaxError struct {
	pos int
	msg string
}

func (e *syntaxError) Error() string { return fmt.Sprintf("%d: %s", e.pos, e.msg) }

// parser evaluates while it parses. A division by zero only sets divZero,
// so that a later syntax error still wins.
type parser struct {
	s       string
	pos     int
	divZero bool
}

func eval(s string) (float64, error) {
	p := &parser{s: s}
	v, err := p.expr()
	if err == nil && p.peek() != end {
		err = p.bad()
	}
	if err == nil && p.divZero {
		err = errDivZero
	}
	return v, err
}

const end = -1

// peek skips spaces and returns the next byte, or end.
func (p *parser) peek() int {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t') {
		p.pos++
	}
	if p.pos == len(p.s) {
		return end
	}
	return int(p.s[p.pos])
}

func (p *parser) bad() error {
	if p.pos == len(p.s) {
		return &syntaxError{p.pos, "unexpected end of expression"}
	}
	r, _ := utf8.DecodeRuneInString(p.s[p.pos:])
	return &syntaxError{p.pos, fmt.Sprintf("unexpected %q", r)}
}

// expr := term (("+" | "-") term)*
func (p *parser) expr() (float64, error) {
	v, err := p.term()
	for err == nil {
		op := p.peek()
		if op != '+' && op != '-' {
			break
		}
		p.pos++
		var r float64
		if r, err = p.term(); op == '+' {
			v += r
		} else {
			v -= r
		}
	}
	return v, err
}

// term := unary (("*" | "/" | "%") unary)*
func (p *parser) term() (float64, error) {
	v, err := p.unary()
	for err == nil {
		op := p.peek()
		if op != '*' && op != '/' && op != '%' {
			break
		}
		p.pos++
		var r float64
		if r, err = p.unary(); err != nil {
			break
		}
		switch {
		case op == '*':
			v *= r
		case r == 0:
			p.divZero = true
		case op == '/':
			v /= r
		default:
			v = math.Mod(v, r)
		}
	}
	return v, err
}

// unary := ("+" | "-") unary | power
func (p *parser) unary() (float64, error) {
	switch p.peek() {
	case '-':
		p.pos++
		v, err := p.unary()
		return -v, err
	case '+':
		p.pos++
		return p.unary()
	}
	return p.power()
}

// power := primary ("^" unary)?
func (p *parser) power() (float64, error) {
	v, err := p.primary()
	if err != nil || p.peek() != '^' {
		return v, err
	}
	p.pos++
	e, err := p.unary()
	return math.Pow(v, e), err
}

// primary := number | "(" expr ")"
func (p *parser) primary() (float64, error) {
	if p.peek() == '(' {
		p.pos++
		v, err := p.expr()
		if err != nil {
			return 0, err
		}
		if p.peek() != ')' {
			return 0, p.bad()
		}
		p.pos++
		return v, nil
	}
	i := p.pos
	for i < len(p.s) && isDigit(p.s[i]) {
		i++
	}
	if i < len(p.s) && p.s[i] == '.' {
		i++
		for i < len(p.s) && isDigit(p.s[i]) {
			i++
		}
	}
	num := p.s[p.pos:i]
	if !strings.ContainsAny(num, "0123456789") {
		return 0, p.bad()
	}
	// num is digits with at most one dot, so the only possible error is a
	// range error, which gives ±Inf.
	v, _ := strconv.ParseFloat(num, 64)
	p.pos = i
	return v, nil
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }
