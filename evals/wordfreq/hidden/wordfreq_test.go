package hidden

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brokentari/galvantula/evals/evalkit"
)

var bin string

func TestMain(m *testing.M) { os.Exit(evalkit.Main(m, "wordfreq", "../reference", &bin)) }

// writeFiles creates the named files in a new temp dir and returns the dir.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// stdinCase is one run that reads standard input.
type stdinCase struct {
	name, stdin string
	args        []string
	want        string
}

func runStdinCases(t *testing.T, cases []stdinCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			evalkit.Run(t, bin, "", c.stdin, c.args...).Want(t, 0, c.want)
		})
	}
}

func TestStdin(t *testing.T) {
	runStdinCases(t, []stdinCase{
		{name: "sentence", stdin: "the cat and the hat\n", want: "2 the\n1 and\n1 cat\n1 hat\n"},
		{name: "no trailing newline", stdin: "b a b", want: "2 b\n1 a\n"},
		{name: "many lines", stdin: "one\ntwo two\n\nthree three three\n", want: "3 three\n2 two\n1 one\n"},
	})
}

func TestSortByCount(t *testing.T) {
	// Words first appear in the reverse of the wanted order, and the counts
	// 10, 9 and 2 sort differently as numbers and as text.
	in := "w x x " + strings.Repeat("z ", 9) + strings.Repeat("y ", 10) + "\n"
	evalkit.Run(t, bin, "", in).Want(t, 0, "10 y\n9 z\n2 x\n1 w\n")
}

func TestTiesAlphabetical(t *testing.T) {
	runStdinCases(t, []stdinCase{
		{name: "equal counts", stdin: "pear fig apple banana apple pear\n", want: "2 apple\n2 pear\n1 banana\n1 fig\n"},
		{name: "prefixes", stdin: "abc ab a\n", want: "1 a\n1 ab\n1 abc\n"},
		{name: "compared in lower case", stdin: "Cherry banana Apple\n", want: "1 apple\n1 banana\n1 cherry\n"},
	})
}

func TestCaseInsensitive(t *testing.T) {
	runStdinCases(t, []stdinCase{
		{name: "ascii", stdin: "The THE the tHe Go go\n", want: "4 the\n2 go\n"},
		{name: "upper only", stdin: "HELLO\n", want: "1 hello\n"},
	})
}

func TestUnicodeLetters(t *testing.T) {
	// Precomposed letters: U+00EF ï, U+00CF Ï, U+00E9 é, U+00C9 É.
	in := "Naïve naïve NAÏVE naïve " +
		"Café café CAFÉ " +
		"Привет ПРИВЕТ " +
		"日本語\n"
	want := "4 naïve\n3 café\n2 привет\n1 日本語\n"
	evalkit.Run(t, bin, "", in).Want(t, 0, want)
}

func TestDigits(t *testing.T) {
	runStdinCases(t, []stdinCase{
		{name: "digits and letters", stdin: "2024 abc123 ABC123 x9y 2024 2024\n", want: "3 2024\n2 abc123\n1 x9y\n"},
		{name: "number split by punctuation", stdin: "3.14 3,14 3\n", want: "3 3\n2 14\n"},
	})
}

func TestSeparators(t *testing.T) {
	seps := []string{
		" ", "  ", "\t", "\n", "\r\n", "-", "_", "'", ".", ",", ";", ":", "!", "?",
		"/", "\\", "(", ")", "\"", "+", "=", "@", "#", "*",
		"’", // right single quotation mark
		"—", // em dash
		" ", // no-break space
		"«", // left-pointing double angle quotation mark
		"\U0001F600",
	}
	var parts []string
	for _, s := range seps {
		parts = append(parts, "one"+s+"two")
	}
	in := strings.Join(parts, " ") + "\n"
	want := fmt.Sprintf("%d one\n%d two\n", len(seps), len(seps))
	evalkit.Run(t, bin, "", in).Want(t, 0, want)
}

// twelve has twelve distinct words, each once, in reverse alphabetical order.
const twelve = "l k j i h g f e d c b a\n"

func TestDefaultLimit(t *testing.T) {
	evalkit.Run(t, bin, "", twelve).Want(t, 0, "1 a\n1 b\n1 c\n1 d\n1 e\n1 f\n1 g\n1 h\n1 i\n1 j\n")
}

func TestNFlag(t *testing.T) {
	runStdinCases(t, []stdinCase{
		{name: "two", stdin: "a a a b b c\n", args: []string{"-n", "2"}, want: "3 a\n2 b\n"},
		{name: "one", stdin: "a b b\n", args: []string{"-n", "1"}, want: "2 b\n"},
		{name: "cut inside a tie", stdin: "c b a\n", args: []string{"-n", "2"}, want: "1 a\n1 b\n"},
		{name: "more than words", stdin: "a b b c c c\n", args: []string{"-n", "50"}, want: "3 c\n2 b\n1 a\n"},
		{name: "above default", stdin: twelve, args: []string{"-n", "11"},
			want: "1 a\n1 b\n1 c\n1 d\n1 e\n1 f\n1 g\n1 h\n1 i\n1 j\n1 k\n"},
		{name: "zero prints all", stdin: twelve, args: []string{"-n", "0"},
			want: "1 a\n1 b\n1 c\n1 d\n1 e\n1 f\n1 g\n1 h\n1 i\n1 j\n1 k\n1 l\n"},
	})
}

func TestLongLine(t *testing.T) {
	// About 2 MB on one line, past the usual line buffer limits.
	in := strings.Repeat("alpha ", 200000) + strings.Repeat("beta ", 150000)
	evalkit.Run(t, bin, "", in).Want(t, 0, "200000 alpha\n150000 beta\n")
}

func TestEmptyInput(t *testing.T) {
	runStdinCases(t, []stdinCase{
		{name: "empty stdin", stdin: "", want: ""},
		{name: "no words", stdin: "  ,.!\n\t-- —\n", want: ""},
		{name: "empty stdin with -n 0", stdin: "", args: []string{"-n", "0"}, want: ""},
	})
	t.Run("empty file", func(t *testing.T) {
		dir := writeFiles(t, map[string]string{"empty.txt": ""})
		evalkit.Run(t, bin, dir, "", "empty.txt").Want(t, 0, "")
	})
}

func TestFiles(t *testing.T) {
	files := map[string]string{
		"notes.txt": "Go is fun.\nGo is fast!\n",
		"a.txt":     "apple banana\napple\n",
		"b.txt":     "Banana cherry\nBANANA\n",
		"nonl.txt":  "x y x",
		"empty.txt": "",
	}
	cases := []struct {
		name, stdin string
		args        []string
		want        string
	}{
		{name: "one file", args: []string{"notes.txt"}, want: "2 go\n2 is\n1 fast\n1 fun\n"},
		{name: "counts combine across files", args: []string{"a.txt", "b.txt"}, want: "3 banana\n2 apple\n1 cherry\n"},
		{name: "file order does not matter", args: []string{"b.txt", "a.txt"}, want: "3 banana\n2 apple\n1 cherry\n"},
		{name: "no trailing newline", args: []string{"nonl.txt"}, want: "2 x\n1 y\n"},
		{name: "with an empty file", args: []string{"empty.txt", "a.txt"}, want: "2 apple\n1 banana\n"},
		{name: "n flag with files", args: []string{"-n", "1", "a.txt", "b.txt"}, want: "3 banana\n"},
		{name: "stdin ignored when files given", stdin: "zzz zzz zzz\n", args: []string{"a.txt"}, want: "2 apple\n1 banana\n"},
	}
	dir := writeFiles(t, files)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			evalkit.Run(t, bin, dir, c.stdin, c.args...).Want(t, 0, c.want)
		})
	}
}

func TestMissingFile(t *testing.T) {
	evalkit.Run(t, bin, "", "a b\n", "nosuchfile.txt").WantFail(t, 1, "nosuchfile.txt")
}

func TestMissingFileAmongReadable(t *testing.T) {
	// Whether the readable file's counts reach stdout is left open.
	dir := writeFiles(t, map[string]string{"good.txt": "apple\n"})
	for _, args := range [][]string{{"good.txt", "absent.txt"}, {"absent.txt", "good.txt"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r := evalkit.Run(t, bin, dir, "", args...)
			if r.Code != 1 {
				t.Errorf("exit %d, want 1 (stderr: %s)", r.Code, r.Stderr)
			}
			if !strings.Contains(r.Stderr, "absent.txt") {
				t.Errorf("stderr %q does not mention %q", r.Stderr, "absent.txt")
			}
		})
	}
}

func TestUnreadableFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{"secret.txt": "hidden words\n"})
	path := filepath.Join(dir, "secret.txt")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("file mode 000 does not stop reads here (running as root?)")
	}
	evalkit.Run(t, bin, dir, "", "secret.txt").WantFail(t, 1, "secret.txt")
}

func TestDirectoryArgument(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "somedir"), 0o755); err != nil {
		t.Fatal(err)
	}
	evalkit.Run(t, bin, dir, "", "somedir").WantFail(t, 1, "somedir")
}

func TestBadFlag(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "apple\n"})
	for _, args := range [][]string{
		{"-x"},
		{"-bogus", "a.txt"},
		{"-n"},
		{"-n", "abc"},
		{"-n", "-1"},
		{"-n", "-5", "a.txt"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			// The spec names no stderr text for flag errors, so only the
			// exit status is checked.
			r := evalkit.Run(t, bin, dir, "a b\n", args...)
			if r.Code != 2 {
				t.Errorf("exit %d, want 2 (stdout: %q, stderr: %q)", r.Code, r.Stdout, r.Stderr)
			}
		})
	}
}
