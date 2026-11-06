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

// emptyTree is git's id for a tree with no files: a base before the first commit.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

const instructions = `You review a change in this repository before its phase is called done. Follow the ponytail-review skill below, with two additions:

- Check Go code against the modern Go guidelines in the standards above. A missed guideline is Should fix.
- Review the prose the change adds or edits (README and other docs, code comments, UI text, eval specs, commit messages) against the Orwell rules in the standards above. Report these findings in a fourth group named **Prose**, after Nice to have. They count as Should fix, and they need the same four parts.

`

const task = `Review everything that changed from %[1]s to HEAD. Start with git diff --stat %[1]s HEAD and git log %[1]s..HEAD, then read the changes and the code they touch.

Skip generated and vendored files: ui/src/gen/, ui/package-lock.json, go.sum, standards/vendor/, and the block between the STANDARDS markers in AGENTS.md.

Give the full report as your final answer.`

func main() {
	phase := flag.String("phase", "", "phase name, such as phase-0; names the report file")
	base := flag.String("base", emptyTree, "review changes after this git ref (default: the whole history)")
	rails := flag.String("guardrails", "guardrails.yaml", "settings file with the reviewer model")
	flag.Parse()
	if *phase == "" {
		log.Fatal("usage: phasereview -phase name [-base ref]")
	}
	g, err := guardrails.Load(*rails)
	if err != nil {
		log.Fatal(err)
	}
	block, err := standards.Render(g.Go.Version)
	if err != nil {
		log.Fatal(err)
	}
	review, err := standards.Review()
	if err != nil {
		log.Fatal(err)
	}
	sys, err := os.CreateTemp("", "phasereview-*.md")
	if err != nil {
		log.Fatal(err)
	}
	defer os.Remove(sys.Name())
	if _, err := sys.WriteString(block + "\n" + instructions + review); err != nil {
		log.Fatal(err)
	}
	sys.Close()

	head, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		log.Fatal("git rev-parse HEAD: ", err)
	}
	r := g.Models.Reviewer
	log.Printf("reviewing %s..%s with %s at %s effort", short(*base), strings.TrimSpace(string(head)), r.Model, r.Effort)
	res, err := claude.Run(context.Background(), claude.Options{
		Dir:        ".",
		Prompt:     fmt.Sprintf(task, *base),
		Model:      r.Model,
		Effort:     r.Effort,
		SystemFile: sys.Name(),
		Tools:      []string{"Read", "Glob", "Grep", "Bash"},
		Allow:      []string{"Bash(git diff *)", "Bash(git log *)", "Bash(git show *)", "Bash(go doc *)"},
		Mode:       "dontAsk", // read-only: anything not allowed above is refused
	})
	if err != nil {
		log.Fatal(err)
	}

	report := fmt.Sprintf("# Review: %s\n\nReviewer: %s at %s effort, in a new session. Range: %s..%s. Date: %s. Cost: $%.2f.\n\n%s\n",
		*phase, r.Model, r.Effort, short(*base), strings.TrimSpace(string(head)), time.Now().Format(time.DateOnly), res.CostUSD, strings.TrimSpace(res.Text))
	out := filepath.Join("docs", "reviews", *phase+".md")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(out, []byte(report), 0o644); err != nil {
		log.Fatal(err)
	}
	verdict := "no verdict line"
	for line := range strings.Lines(res.Text) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Verdict:"); ok {
			verdict = strings.TrimSpace(v)
		}
	}
	log.Printf("wrote %s ($%.2f): verdict: %s", out, res.CostUSD, verdict)
	if verdict != "Ship." {
		os.Exit(1)
	}
}

func short(ref string) string {
	if ref == emptyTree {
		return "start"
	}
	return ref
}
