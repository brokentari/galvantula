// Command phasereview reviews everything that changed since base, before a
// phase is called done. It asks the reviewer model in guardrails.yaml, in a
// new session, to follow ponytail-review, to check Go against the modern Go
// guidelines, and to check prose against the Orwell rules. It writes the
// report to docs/reviews/<phase>.md and exits 1 unless the verdict is Ship.
//
//	go run ./cmd/phasereview -phase phase-0 [-base ref]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/brokentari/galvantula/internal/claude"
	"github.com/brokentari/galvantula/internal/guardrails"
	"github.com/brokentari/galvantula/standards"
)

const (
	// emptyTree is git's id for a tree with no files: a base before the first commit.
	emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	// maxDiff is the largest diff put in the reviewer's instructions. Past it,
	// the reviewer reads the changed files itself.
	maxDiff = 400_000
)

// skipped are generated or vendored paths that the review leaves out.
var skipped = []string{"ui/src/gen", "ui/package-lock.json", "go.sum", "standards/vendor"}

const instructions = `You review a change in this repository before its phase is called done. Follow the ponytail-review skill below, with two additions:

- Check Go code against the modern Go guidelines in the standards above. A missed guideline is Should fix.
- Review the prose the change adds or edits (README and other docs, code comments, UI text, eval specs, commit messages) against the Orwell rules in the standards above. Report these findings in a fourth group named **Prose**, after Nice to have. They count as Should fix, and they need the same four parts.

You can read files but not run commands. The change itself is at the end of these instructions. Skip the block between the STANDARDS markers in AGENTS.md; a test keeps it in step with its sources.

`

func main() { os.Exit(run()) }

func run() int {
	phase := flag.String("phase", "", "phase name, such as phase-0; names the report file")
	base := flag.String("base", emptyTree, "review changes after this git ref (default: the whole history)")
	rails := flag.String("guardrails", "guardrails.yaml", "settings file with the reviewer model")
	flag.Parse()
	if *phase == "" {
		log.Print("usage: phasereview -phase name [-base ref]")
		return 2
	}
	if err := review(*phase, *base, *rails); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}

func review(phase, base, rails string) error {
	g, err := guardrails.Load(rails)
	if err != nil {
		return err
	}
	block, err := standards.Render(g.Go.Version)
	if err != nil {
		return err
	}
	skill, err := standards.Review()
	if err != nil {
		return err
	}
	head, err := git("rev-parse", "--short", "HEAD")
	if err != nil {
		return err
	}
	change, err := describeChange(base)
	if err != nil {
		return err
	}

	sys, err := os.CreateTemp("", "phasereview-*.md")
	if err != nil {
		return err
	}
	defer os.Remove(sys.Name())
	_, err = sys.WriteString(block + "\n" + instructions + skill + "\n" + change)
	if err := errors.Join(err, sys.Close()); err != nil {
		return err
	}

	r := g.Models.Reviewer
	log.Printf("reviewing %s..%s with %s at %s effort", short(base), head, r.Model, r.Effort)
	res, err := claude.Run(context.Background(), claude.Options{
		Dir:        ".",
		Prompt:     "Review the change described at the end of your instructions. Give the full report as your final answer.",
		Model:      r.Model,
		Effort:     r.Effort,
		SystemFile: sys.Name(),
		Tools:      []string{"Read", "Glob", "Grep"},
		Mode:       "dontAsk", // anything that would need permission is refused
	})
	if err != nil {
		return err
	}

	report := fmt.Sprintf("# Review: %s\n\nReviewer: %s at %s effort, in a new session. Range: %s..%s. Date: %s. Cost: $%.2f.\n\n%s\n",
		phase, r.Model, r.Effort, short(base), head, time.Now().Format(time.DateOnly), res.CostUSD, strings.TrimSpace(res.Text))
	out := filepath.Join("docs", "reviews", phase+".md")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(out, []byte(report), 0o644); err != nil {
		return err
	}
	verdict := "no verdict line"
	for line := range strings.Lines(res.Text) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Verdict:"); ok {
			verdict = strings.TrimSpace(v)
		}
	}
	log.Printf("wrote %s ($%.2f): verdict: %s", out, res.CostUSD, verdict)
	if verdict != "Ship." {
		return errors.New("the verdict is not Ship")
	}
	return nil
}

// describeChange returns the commit messages, the changed files and, if it
// is small enough, the diff, for the reviewer's instructions.
func describeChange(base string) (string, error) {
	pathspec := []string{"--", "."}
	for _, p := range skipped {
		pathspec = append(pathspec, ":(exclude)"+p)
	}
	logText, err := git("log", "--format=commit %h%n%B", base+"..HEAD")
	if base == emptyTree {
		logText, err = git("log", "--format=commit %h%n%B", "HEAD")
	}
	if err != nil {
		return "", err
	}
	stat, err := git(append([]string{"diff", "--stat=200", base, "HEAD"}, pathspec...)...)
	if err != nil {
		return "", err
	}
	diff, err := git(append([]string{"diff", base, "HEAD"}, pathspec...)...)
	if err != nil {
		return "", err
	}
	if len(diff) > maxDiff {
		diff = "The diff is too long to include. Read the changed files listed above."
	}
	return fmt.Sprintf("## The change\n\n### Commits\n\n%s\n\n### Changed files\n\n%s\n\n### Diff\n\n%s\n", logText, stat, diff), nil
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func short(ref string) string {
	if ref == emptyTree {
		return "start"
	}
	return ref
}
