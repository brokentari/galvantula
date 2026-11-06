// Hidden acceptance tests for evals/wordfreq/spec.md. The factory never sees them.
package hidden

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brokentari/galvantula/evals/evalkit"
)

var bin string

func TestMain(m *testing.M) { os.Exit(evalkit.Main(m, "wordfreq", "../reference", &bin)) }

func TestCountsAndOrder(t *testing.T) {
	evalkit.Run(t, bin, "", "b a c b c b").Want(t, 0, "3 b\n2 c\n1 a\n")
}

func TestTiesAreAlphabetical(t *testing.T) {
	evalkit.Run(t, bin, "", "pear apple fig apple fig pear").Want(t, 0, "2 apple\n2 fig\n2 pear\n")
}

func TestCaseIsIgnored(t *testing.T) {
	evalkit.Run(t, bin, "", "Go GO go Gopher").Want(t, 0, "3 go\n1 gopher\n")
}

func TestSeparatorsAndUnicode(t *testing.T) {
	evalkit.Run(t, bin, "", "Straße, STRASSE; straße! café-café 42 42\n\tnaïve").
		Want(t, 0, "2 42\n2 café\n2 straße\n1 naïve\n1 strasse\n")
}

func TestDefaultLimitIsTen(t *testing.T) {
	res := evalkit.Run(t, bin, "", "a b c d e f g h i j k l")
	res.Want(t, 0, "1 a\n1 b\n1 c\n1 d\n1 e\n1 f\n1 g\n1 h\n1 i\n1 j\n")
}

func TestLimitFlag(t *testing.T) {
	evalkit.Run(t, bin, "", "x x y z", "-n", "2").Want(t, 0, "2 x\n1 y\n")
	evalkit.Run(t, bin, "", "a b c d e f g h i j k l", "-n", "0").
		Want(t, 0, "1 a\n1 b\n1 c\n1 d\n1 e\n1 f\n1 g\n1 h\n1 i\n1 j\n1 k\n1 l\n")
}

func TestFilesAreCombined(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "one.txt"), []byte("red blue"), 0o644)
	os.WriteFile(filepath.Join(dir, "two.txt"), []byte("blue green blue"), 0o644)
	evalkit.Run(t, bin, dir, "ignored stdin", "one.txt", "two.txt").Want(t, 0, "3 blue\n1 green\n1 red\n")
}

func TestEmptyInput(t *testing.T) {
	evalkit.Run(t, bin, "", "").Want(t, 0, "")
	evalkit.Run(t, bin, "", " ... ,,, ").Want(t, 0, "")
}

func TestMissingFile(t *testing.T) {
	evalkit.Run(t, bin, "", "", "no-such-file.txt").WantFail(t, 1, "no-such-file.txt")
}

func TestBadFlags(t *testing.T) {
	evalkit.Run(t, bin, "", "a", "-n", "-1").WantFail(t, 2)
	evalkit.Run(t, bin, "", "a", "-x").WantFail(t, 2)
}
