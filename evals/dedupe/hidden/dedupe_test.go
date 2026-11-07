package hidden

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brokentari/galvantula/evals/evalkit"
)

var bin string

func TestMain(m *testing.M) { os.Exit(evalkit.Main(m, "dedupe", "../reference", &bin)) }

// run writes files into a new directory and runs dedupe there.
func run(t *testing.T, files map[string]string, stdin string, args ...string) evalkit.Result {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return evalkit.Run(t, bin, dir, stdin, args...)
}

// tc is one run that must exit 0 and print exactly want.
type tc struct {
	name  string
	files map[string]string
	stdin string
	args  []string
	want  string
}

func check(t *testing.T, cases []tc) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run(t, c.files, c.stdin, c.args...).Want(t, 0, c.want)
		})
	}
}

func TestDefault(t *testing.T) {
	check(t, []tc{
		{name: "repeats anywhere", stdin: "b\na\nb\nc\na\n", want: "b\na\nc\n"},
		{name: "adjacent repeats", stdin: "x\nx\nx\ny\n", want: "x\ny\n"},
		{name: "all distinct", stdin: "c\nb\na\n", want: "c\nb\na\n"},
		{name: "case matters", stdin: "a\nA\na\n", want: "a\nA\n"},
		{name: "whole line compared", stdin: "hello world\nhello\nhello world\n", want: "hello world\nhello\n"},
		{name: "spaces are part of the line", stdin: "a\na \n a\na\n", want: "a\na \n a\n"},
		{name: "last line without newline", stdin: "a\nb", want: "a\nb\n"},
		{name: "last line without newline repeats", stdin: "a\nb\na", want: "a\nb\n"},
		{name: "only line without newline", stdin: "hello", want: "hello\n"},
		{name: "empty lines", stdin: "\n\n\n", want: "\n"},
		{name: "empty line among others", stdin: "a\n\nb\n\na\n", want: "a\n\nb\n"},
		{name: "spaces-only lines", stdin: " \na\n \n  \n", want: " \na\n  \n"},
		{name: "spaces-only line differs from empty line", stdin: "\n \n\n \n", want: "\n \n"},
	})
}

func TestLongLine(t *testing.T) {
	long := strings.Repeat("x", 100_000)
	in := long + "\ny\n" + long + "\n"
	want := long + "\ny\n"
	r := run(t, nil, in)
	if r.Code != 0 || r.Stdout != want {
		t.Errorf("exit %d, stdout %d bytes; want exit 0, stdout %d bytes (the %d-byte line once, then y) (stderr: %s)",
			r.Code, len(r.Stdout), len(want), len(long), r.Stderr)
	}
}

func TestCount(t *testing.T) {
	c := []string{"-c"}
	check(t, []tc{
		{name: "first-appearance order", stdin: "b\na\nb\nc\nb\n", args: c, want: "3\tb\n1\ta\n1\tc\n"},
		{name: "each once", stdin: "x\ny\n", args: c, want: "1\tx\n1\ty\n"},
		{name: "more than nine", stdin: strings.Repeat("z\n", 12), args: c, want: "12\tz\n"},
		{name: "empty lines", stdin: "\nx\n\n", args: c, want: "2\t\n1\tx\n"},
		{name: "spaces-only line", stdin: "  \n  \n", args: c, want: "2\t  \n"},
		{name: "last line without newline", stdin: "a\nb\na", args: c, want: "2\ta\n1\tb\n"},
	})
}

func TestDuplicatesOnly(t *testing.T) {
	d := []string{"-d"}
	// The repeated lines in these inputs come in the same order by first and by
	// second appearance, so the tests do not depend on that choice.
	check(t, []tc{
		{name: "only repeated lines", stdin: "a\nc\nb\na\nb\nb\n", args: d, want: "a\nb\n"},
		{name: "each printed once", stdin: "a\na\na\n", args: d, want: "a\n"},
		{name: "repeat in last line without newline", stdin: "x\ny\nx", args: d, want: "x\n"},
		{name: "repeated empty line", stdin: "\na\n\n", args: d, want: "\n"},
		{name: "with -c", stdin: "a\nc\nb\na\nb\nb\n", args: []string{"-d", "-c"}, want: "2\ta\n3\tb\n"},
		{name: "with -c first", stdin: "a\nc\nb\na\nb\nb\n", args: []string{"-c", "-d"}, want: "2\ta\n3\tb\n"},
	})
}

func TestUniqueOnly(t *testing.T) {
	u := []string{"-u"}
	check(t, []tc{
		{name: "only single lines", stdin: "a\nb\nc\nb\nd\na\n", args: u, want: "c\nd\n"},
		{name: "input order", stdin: "z\ny\nx\ny\n", args: u, want: "z\nx\n"},
		{name: "repeat in last line without newline", stdin: "x\ny\nx", args: u, want: "y\n"},
		{name: "single empty line", stdin: "a\n\na\n", args: u, want: "\n"},
		{name: "with -c", stdin: "a\nb\na\n", args: []string{"-u", "-c"}, want: "1\tb\n"},
	})
}

func TestIgnoreCase(t *testing.T) {
	i := []string{"-i"}
	check(t, []tc{
		{name: "first spelling printed", stdin: "Hello\nhello\nHELLO\nworld\n", args: i, want: "Hello\nworld\n"},
		{name: "mixed-case first spelling", stdin: "hELLo\nHello\nhello\n", args: i, want: "hELLo\n"},
		{name: "first-appearance order", stdin: "b\nA\nB\na\n", args: i, want: "b\nA\n"},
		{name: "whole line compared", stdin: "Foo Bar\nfoo\nfoo bar\n", args: i, want: "Foo Bar\nfoo\n"},
		{name: "non-ASCII letters", stdin: "Ärger\nÉCOLE\närger\nécole\n", args: i, want: "Ärger\nÉCOLE\n"},
		{name: "with -c", stdin: "Apple\napple\nBanana\nAPPLE\n", args: []string{"-i", "-c"}, want: "3\tApple\n1\tBanana\n"},
		{name: "with -c first", stdin: "Apple\napple\nBanana\nAPPLE\n", args: []string{"-c", "-i"}, want: "3\tApple\n1\tBanana\n"},
		{name: "with -d", stdin: "Apple\nbanana\nAPPLE\n", args: []string{"-i", "-d"}, want: "Apple\n"},
		{name: "with -u", stdin: "Apple\nbanana\nAPPLE\n", args: []string{"-i", "-u"}, want: "banana\n"},
	})
}

func TestFiles(t *testing.T) {
	both := map[string]string{"one.txt": "a\nb\n", "two.txt": "b\nc\na\n"}
	check(t, []tc{
		{name: "one file", files: map[string]string{"in.txt": "b\na\nb\n"}, args: []string{"in.txt"}, want: "b\na\n"},
		{name: "files form one list", files: both, args: []string{"one.txt", "two.txt"}, want: "a\nb\nc\n"},
		{name: "argument order", files: both, args: []string{"two.txt", "one.txt"}, want: "b\nc\na\n"},
		{name: "counts across files", files: both, args: []string{"-c", "one.txt", "two.txt"}, want: "2\ta\n2\tb\n1\tc\n"},
		{name: "single lines across files", files: both, args: []string{"-u", "one.txt", "two.txt"}, want: "c\n"},
		{
			name:  "repeats across files",
			files: map[string]string{"one.txt": "a\nb\n", "two.txt": "c\nb\n"},
			args:  []string{"-d", "one.txt", "two.txt"},
			want:  "b\n",
		},
		{
			name:  "case folded across files",
			files: map[string]string{"one.txt": "Go\n", "two.txt": "GO\ngo\nRust\n"},
			args:  []string{"-i", "one.txt", "two.txt"},
			want:  "Go\nRust\n",
		},
		{
			name:  "same file twice",
			files: map[string]string{"in.txt": "x\ny\n"},
			args:  []string{"-c", "in.txt", "in.txt"},
			want:  "2\tx\n2\ty\n",
		},
		{
			name:  "last line of each file without newline",
			files: map[string]string{"one.txt": "a\nb", "two.txt": "b\nc"},
			args:  []string{"-c", "one.txt", "two.txt"},
			want:  "1\ta\n2\tb\n1\tc\n",
		},
		{
			name:  "standard input ignored",
			files: map[string]string{"in.txt": "a\n"},
			stdin: "z\n",
			args:  []string{"in.txt"},
			want:  "a\n",
		},
		{
			name:  "empty file among others",
			files: map[string]string{"empty.txt": "", "in.txt": "a\na\n"},
			args:  []string{"empty.txt", "in.txt", "empty.txt"},
			want:  "a\n",
		},
	})
}

// The spec says empty input prints nothing, so these cases expect no output.
func TestEmptyInput(t *testing.T) {
	check(t, []tc{
		{name: "standard input"},
		{name: "standard input with -c", args: []string{"-c"}},
		{name: "standard input with -d", args: []string{"-d"}},
		{name: "standard input with -u", args: []string{"-u"}},
		{name: "standard input with -i", args: []string{"-i"}},
		{name: "empty file", files: map[string]string{"empty.txt": ""}, args: []string{"empty.txt"}},
		{name: "two empty files", files: map[string]string{"a.txt": "", "b.txt": ""}, args: []string{"a.txt", "b.txt"}},
	})
}

func TestUnreadableFile(t *testing.T) {
	good := map[string]string{"good.txt": "a\na\nb\n"}
	cases := []struct {
		name string
		args []string
	}{
		{"missing file", []string{"nosuch.txt"}},
		{"missing file after a good one", []string{"good.txt", "nosuch.txt"}},
		{"missing file before a good one", []string{"nosuch.txt", "good.txt"}},
		{"missing file with -c", []string{"-c", "good.txt", "nosuch.txt"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run(t, good, "a\n", c.args...).WantFail(t, 1, "nosuch.txt")
		})
	}
	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "somedir"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "good.txt"), []byte("a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		evalkit.Run(t, bin, dir, "", "good.txt", "somedir").WantFail(t, 1, "somedir")
	})
}

// The spec fixes only the exit status for usage errors, so only that is checked.
func TestUsageErrors(t *testing.T) {
	files := map[string]string{"in.txt": "a\na\nb\n"}
	cases := []struct {
		name string
		args []string
	}{
		{"-d and -u", []string{"-d", "-u"}},
		{"-u and -d", []string{"-u", "-d"}},
		{"-d and -u with other flags", []string{"-c", "-i", "-d", "-u"}},
		{"-d and -u with a file", []string{"-d", "-u", "in.txt"}},
		{"unknown flag", []string{"--no-such-flag"}},
		{"unknown short flag", []string{"-Q"}},
		{"unknown flag with a file", []string{"--no-such-flag", "in.txt"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := run(t, files, "a\na\nb\n", c.args...)
			if r.Code != 2 {
				t.Errorf("exit %d, want 2 (stdout %q, stderr %q)", r.Code, r.Stdout, r.Stderr)
			}
		})
	}
}
