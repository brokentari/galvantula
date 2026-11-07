package hidden

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/brokentari/galvantula/evals/evalkit"
)

var bin string

func TestMain(m *testing.M) { os.Exit(evalkit.Main(m, "calc", "../reference", &bin)) }

type valueCase struct{ expr, want string }

// runArgs runs calc once per case with the expression as its only argument.
func runArgs(t *testing.T, cases []valueCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			t.Parallel()
			evalkit.Run(t, bin, "", "", c.expr).Want(t, 0, c.want+"\n")
		})
	}
}

// runStdin runs calc once per case with the expression as the only line of
// standard input. Expressions that start with "-" go here, so that they
// cannot be taken for flags.
func runStdin(t *testing.T, cases []valueCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			t.Parallel()
			evalkit.Run(t, bin, "", c.expr+"\n").Want(t, 0, c.want+"\n")
		})
	}
}

var firstNumber = regexp.MustCompile(`[0-9]+`)

// wantSyntaxError fails the test unless a line of stderr starts with expr
// and is followed by a position. If pos >= 0, the first number after expr
// must be pos.
func wantSyntaxError(t *testing.T, stderr, expr string, pos int) {
	t.Helper()
	for line := range strings.Lines(stderr) {
		rest, ok := strings.CutPrefix(line, expr)
		if !ok {
			continue
		}
		got := firstNumber.FindString(rest)
		switch {
		case got == "":
			t.Errorf("stderr line %q has no position after the expression", line)
		case pos >= 0 && got != strconv.Itoa(pos):
			t.Errorf("stderr line %q gives position %s; want %d", line, got, pos)
		}
		return
	}
	t.Errorf("no stderr line starts with the expression %q; stderr:\n%s", expr, stderr)
}

func TestNumbers(t *testing.T) {
	runArgs(t, []valueCase{
		{"3", "3"},
		{"2.5", "2.5"},
		{".5", "0.5"},
		{"1.0", "1"},
		{"2.50", "2.5"},
		{"0.1", "0.1"},
		{"3.14159", "3.14159"},
	})
}

func TestOperators(t *testing.T) {
	runArgs(t, []valueCase{
		{"1+2", "3"},
		{"5-8", "-3"},
		{"2*3", "6"},
		{"7/2", "3.5"},
		{"7%3", "1"},
		{"2^10", "1024"},
	})
}

func TestPrecedence(t *testing.T) {
	runArgs(t, []valueCase{
		{"2+3*4", "14"},
		{"2*3+4", "10"},
		{"(2+3)*4", "20"},
		{"((1+2))*3", "9"},
		{"2*3^2", "18"},
		{"1+2^3", "9"},
		{"8*7%3", "2"},
		{"2+7%3", "3"},
		{"8/2^2", "2"},
	})
}

func TestAssociativity(t *testing.T) {
	runArgs(t, []valueCase{
		{"10-4-3", "3"},
		{"2-3+4", "3"},
		{"100/10/5", "2"},
		{"12/4*3", "9"},
		{"7%4/2", "1.5"},
		{"2^3^2", "512"},
		{"(2^3)^2", "64"},
	})
}

func TestSigns(t *testing.T) {
	runArgs(t, []valueCase{
		{"2*-3", "-6"},
		{"2--3", "5"},
		{"2-+3", "-1"},
		{"2+-3", "-1"},
		{"2*+3", "6"},
		{"+3", "3"},
		{"(-2)^2", "4"},
		{"2*-2^2", "-8"},
		{"2 * - 3", "-6"},
		{"(-(2+3))", "-5"},
	})
}

func TestLeadingSign(t *testing.T) {
	runStdin(t, []valueCase{
		{"-3", "-3"},
		{"-2^2", "-4"},
		{"-3^2", "-9"},
		{"-(2+3)", "-5"},
		{"-.5", "-0.5"},
		{"-7%3", "-1"},
		{"-2*-3", "6"},
	})
}

func TestRemainder(t *testing.T) {
	runArgs(t, []valueCase{
		{"(-7)%3", "-1"},
		{"7%-3", "1"},
		{"(-7)%-3", "-1"},
		{"5.5%2", "1.5"},
		{"(-5.5)%2", "-1.5"},
		{"7.5%2.5", "0"},
	})
}

func TestSpaces(t *testing.T) {
	runArgs(t, []valueCase{
		{"1 + 2", "3"},
		{"( 1 + 2 ) * 3", "9"},
		{"2 ^ 3 ^ 2", "512"},
		{"7  %  3", "1"},
		{"1.5 * 2", "3"},
	})
}

func TestFormat(t *testing.T) {
	runArgs(t, []valueCase{
		{"1/4", "0.25"},
		{"10/4", "2.5"},
		{"1/3", "0.3333333333333333"},
		{"2/3", "0.6666666666666666"},
		{"0.1+0.2", "0.30000000000000004"},
		{"0.1*3", "0.30000000000000004"},
		{"10/2", "5"},
		{"2.5*2", "5"},
		{"0.5^2", "0.25"},
		{"4^0.5", "2"},
		{"1-2.5", "-1.5"},
		// Shortest %g would print these with a decimal point.
		{"2^20", "1048576"},
		{"123456*1000", "123456000"},
	})
}

func TestNegativeZero(t *testing.T) {
	runArgs(t, []valueCase{
		{"0*-1", "0"},
		{"0/-5", "0"},
		{"(-5)%5", "0"},
		{"2*-0", "0"},
	})
	runStdin(t, []valueCase{
		{"-0", "0"},
		{"-(1-1)", "0"},
	})
}

func TestArguments(t *testing.T) {
	evalkit.Run(t, bin, "", "", "1+1", "2*3", "10/4").Want(t, 0, "2\n6\n2.5\n")
}

func TestArgumentsIgnoreStdin(t *testing.T) {
	evalkit.Run(t, bin, "", "5*5\n", "1+1").Want(t, 0, "2\n")
}

func TestStdin(t *testing.T) {
	cases := []struct{ name, stdin, want string }{
		{"lines", "1+2\n3*4\n10/4\n", "3\n12\n2.5\n"},
		{"empty lines", "\n1\n\n\n2\n\n", "1\n2\n"},
		{"last line without newline", "1+1\n2*3", "2\n6\n"},
		{"only empty lines", "\n\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			evalkit.Run(t, bin, "", c.stdin).Want(t, 0, c.want)
		})
	}
}

func TestSyntaxErrorPosition(t *testing.T) {
	cases := []struct {
		expr string
		pos  int
	}{
		{"1+*2", 2},
		{"1 + * 2", 4},
		{"*1", 0},
		{"1+2)", 3},
		{"(1+2))", 5},
		{"1+2 )", 4},
		{"2$3", 1},
		{"abc", 0},
		{"4/ x", 3},
		{"()", 1},
		{"2^^3", 2},
		{"1+(2*)", 5},
	}
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			t.Parallel()
			r := evalkit.Run(t, bin, "", "", c.expr)
			r.WantFail(t, 1)
			wantSyntaxError(t, r.Stderr, c.expr, c.pos)
		})
	}
}

// TestSyntaxErrorAtEnd checks expressions that end too early. The spec does
// not say which position names the missing character, so any is accepted.
func TestSyntaxErrorAtEnd(t *testing.T) {
	for _, expr := range []string{"2+", "(1+2", "(", "2*(3+"} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			r := evalkit.Run(t, bin, "", "", expr)
			r.WantFail(t, 1)
			wantSyntaxError(t, r.Stderr, expr, -1)
		})
	}
}

func TestSyntaxErrorOnStdin(t *testing.T) {
	r := evalkit.Run(t, bin, "", "1+1\n3+*\n4\n7 * ) 1\n2^3\n")
	r.Want(t, 1, "2\n4\n8\n")
	wantSyntaxError(t, r.Stderr, "3+*", 2)
	wantSyntaxError(t, r.Stderr, "7 * ) 1", 4)
}

func TestDivisionByZero(t *testing.T) {
	for _, expr := range []string{"1/0", "5%0", "1/(2-2)", "0/0", "2.5%0", "1/-0"} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			r := evalkit.Run(t, bin, "", "", expr)
			r.WantFail(t, 1)
			if strings.TrimSpace(r.Stderr) == "" {
				t.Errorf("no error on stderr")
			}
		})
	}
}

func TestDivisionByZeroOnStdin(t *testing.T) {
	r := evalkit.Run(t, bin, "", "1/0\n2+2\n7%0\n3*3\n")
	r.Want(t, 1, "4\n9\n")
	if strings.TrimSpace(r.Stderr) == "" {
		t.Errorf("no error on stderr")
	}
}

func TestErrorsDoNotStopOtherExpressions(t *testing.T) {
	r := evalkit.Run(t, bin, "", "", "1+1", "2+*", "1/0", "3*3")
	r.Want(t, 1, "2\n9\n")
	wantSyntaxError(t, r.Stderr, "2+*", 2)
}

func TestBadOption(t *testing.T) {
	for _, args := range [][]string{
		{"--no-such-option"},
		{"-no-such-option"},
		{"--no-such-option", "1+1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			r := evalkit.Run(t, bin, "", "1+1\n", args...)
			if r.Code != 2 {
				t.Errorf("exit %d; want 2 (stdout %q, stderr %q)", r.Code, r.Stdout, r.Stderr)
			}
		})
	}
}
