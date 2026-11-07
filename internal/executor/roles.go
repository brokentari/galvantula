package executor

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/brokentari/galvantula/internal/guardrails"
	"github.com/brokentari/galvantula/internal/review"
	"github.com/brokentari/galvantula/internal/workspace"
	"github.com/brokentari/galvantula/standards"
)

// plan says what one role of agent does: which model it uses, what it may
// touch, how it is briefed and how its work is checked.
type plan struct {
	model func(guardrails.Models) guardrails.Role
	// brief returns the prompt for a first attempt.
	brief func(repo workspace.Repo, t Task) (string, error)
	tools []string
	allow []string
	mode  string
	// readOnly agents write no code. The runtime keeps their final text as
	// the review report, and nothing is checked or merged.
	readOnly bool
	// frozen says which files from main the agent must not change. Nil
	// freezes all of them.
	frozen func(path string) bool
	// red makes the check require that the tests fail on the code, as they
	// must on the architect's stubs.
	red bool
	// needs lists files that must exist when the agent is done.
	needs []string
	// minTests is how many tests must run, and pass, when the agent is done.
	// A test binary that exits early with status 0 would otherwise pass.
	minTests int
	// skip returns a reason to skip the node without running an agent.
	skip func(repo workspace.Repo) string
	// after runs when the work is merged. It reports to the run's log.
	after func(repo workspace.Repo, log func(string))
}

const rules = `- Use only the standard library.
- Do not leave build output, such as compiled programs, in the directory.
- Do not use t.Skip, t.Skipf or t.SkipNow: the check fails if a test file has one.
- Before you finish, make all of these pass: gofmt -l . (prints nothing), go vet ./..., go fix -diff ./... (prints nothing) and go build ./...`

const builderBrief = `Build the program that spec.md describes, in this directory. AGENTS.md, which is part of your instructions, holds the rules for the code and its prose.

- Write the code and its tests.
- Do not change or delete a file that was here when you started: AGENTS.md, CLAUDE.md, spec.md, go.mod and .factory/. The check fails if you do.
- Also make go test ./... pass.
- Write NOTES.md: in a few lines, what you built and each assumption you made where the spec is unclear.
` + rules

// architectBrief is built from minTests and minRed, so it always states the rule the check enforces.
var architectBrief = fmt.Sprintf(architectTemplate, minTests, int(minRed*100))

const architectTemplate = `Design the program that spec.md describes, in this directory. Do not implement it. AGENTS.md, which is part of your instructions, holds the rules for the code and its prose.

Write these files:
- The packages, types and function signatures of the program. Each function body is a stub that compiles and returns zero values or an error such as errors.New("not implemented"). A stub must never panic: a panic stops the test run, so the other tests cannot run.
- Acceptance tests (_test.go files) for every behavior that spec.md states. Test through the program's interface. For a command, put its logic in a function that main calls, such as run(args, stdin, stdout, stderr) int, and test that function. Where the spec fixes the output, check it exactly.
- CONTRACT.md: the packages and their interfaces, what the tests check, and each place where spec.md is unclear, with the choice your tests make. A person reads this file to approve the contract, so keep it short and plain.

Rules for the tests:
- A builder will implement the stubs, and may not change your tests. Test what the spec says, and nothing it leaves open.
- Every test must fail on the stubs, except a test of a case where doing nothing is correct. The check runs the tests on your stubs and requires at least %d tests, with at least %d%% failing.
- Do not change or delete a file that was here when you started: AGENTS.md, CLAUDE.md, spec.md, go.mod and .factory/.
` + rules

const implementerBrief = `Implement the program that spec.md describes, in this directory. The architect has written the contract: CONTRACT.md, the interfaces and stubs, and acceptance tests. AGENTS.md, which is part of your instructions, holds the rules for the code and its prose.

- Fill in the stubs so that go test ./... passes.
- Do not change or delete the tests (_test.go files), testdata, CONTRACT.md, spec.md, AGENTS.md, CLAUDE.md, go.mod or .factory/. The check fails if you do. You may add new test files for code that needs more tests.
- If a test seems wrong, do not change it. The check cannot pass while a test fails, so the run will stop. Before you finish, say in NOTES.md which test you think is wrong and why, so that a person can fix the contract. Do not spend attempts on a test that spec.md contradicts.
- Write NOTES.md: in a few lines, what you built and each assumption you made.
` + rules

const fixerBrief = `A reviewer found problems in the program in this directory. The review report is below. Fix every Must fix and Should fix finding, and every finding in the Prose group. Do not act on Nice to have items. AGENTS.md, which is part of your instructions, holds the rules for the code and its prose.

- Make the smallest change that fixes each finding. Do not add features.
- Do not change or delete the tests (_test.go files), testdata, CONTRACT.md, spec.md, AGENTS.md, CLAUDE.md, go.mod or .factory/. The check fails if you do. You may add new test files.
- Also make go test ./... pass.
- Add one line to NOTES.md for each finding that you chose not to fix, with the reason.
` + rules + `

The review report:

`

const reviewerBrief = `Review the program in this directory. spec.md says what it must do. CONTRACT.md and the _test.go files are the contract the builder had to meet. NOTES.md holds the builder's assumptions. Read the code and the prose that the builder wrote: every file in the list below. Read the tests only to check the code against them.

Give the full report as your final answer.

Files in the product:

`

const reviewerRules = `You review a program that an agent built, before it is accepted. Follow the ponytail-review skill below, with two additions:

- Check Go code against the modern Go guidelines in the standards above. A missed guideline is Should fix.
- Review the prose the builder wrote (README or other docs, code comments, CLI help and error messages, NOTES.md) against the Orwell rules in the standards above. Report these findings in a fourth group named **Prose**, after Nice to have. They count as Should fix, and they need the same four parts.

You can read files but not run commands. Your report is advisory: tests, not you, decide whether the program is accepted.

`

// standardsFile reports whether a path is a file that the implementer and
// the fixer may not change: the standards node's files, .factory/, and the
// architect's CONTRACT.md.
func standardsFile(path string) bool {
	switch path {
	case "AGENTS.md", "CLAUDE.md", "spec.md", "go.mod", "CONTRACT.md":
		return true
	}
	return strings.HasPrefix(path, ".factory/")
}

// testFile reports whether a path is a test or test data, which the
// architect owns once the contract is approved.
func testFile(path string) bool {
	return strings.HasSuffix(path, "_test.go") || strings.HasPrefix(path, "testdata/") || strings.Contains(path, "/testdata/")
}

func frozenForBuilders(path string) bool { return standardsFile(path) || testFile(path) }

var plans = map[string]plan{
	"builder": {
		model: func(m guardrails.Models) guardrails.Role { return m.Implementer },
		brief: func(workspace.Repo, Task) (string, error) { return builderBrief, nil },
		tools: tools, allow: allow, mode: "acceptEdits",
		minTests: 1,
	},
	"architect": {
		model: func(m guardrails.Models) guardrails.Role { return m.Architect },
		brief: func(workspace.Repo, Task) (string, error) { return architectBrief, nil },
		tools: tools, allow: allow, mode: "acceptEdits",
		red:   true,
		needs: []string{"CONTRACT.md"},
		after: logContract,
	},
	"implementer": {
		model:    func(m guardrails.Models) guardrails.Role { return m.Implementer },
		brief:    func(workspace.Repo, Task) (string, error) { return implementerBrief, nil },
		tools:    tools,
		allow:    allow,
		mode:     "acceptEdits",
		frozen:   frozenForBuilders,
		minTests: minTests,
	},
	"reviewer": {
		model:    func(m guardrails.Models) guardrails.Role { return m.Reviewer },
		brief:    reviewBrief,
		tools:    []string{"Read", "Glob", "Grep"},
		mode:     "dontAsk", // anything not listed in tools is refused
		readOnly: true,
	},
	"fixer": {
		model:    func(m guardrails.Models) guardrails.Role { return m.Implementer },
		brief:    fixBrief,
		tools:    tools,
		allow:    allow,
		mode:     "acceptEdits",
		frozen:   frozenForBuilders,
		skip:     nothingToFix,
		minTests: minTests,
	},
}

const reviewFile = ".factory/review.md"

func reviewBrief(repo workspace.Repo, _ Task) (string, error) {
	files, err := repo.Tracked(repo.Dir)
	if err != nil {
		return "", err
	}
	var list []string
	for _, f := range files {
		if !standardsFile(f) && !testFile(f) || f == "CONTRACT.md" {
			list = append(list, "- "+f)
		}
	}
	return reviewerBrief + strings.Join(list, "\n"), nil
}

func fixBrief(repo workspace.Repo, _ Task) (string, error) {
	report, err := os.ReadFile(filepath.Join(repo.Dir, reviewFile))
	if err != nil {
		return "", err
	}
	return fixerBrief + string(report), nil
}

// nothingToFix skips the fix node when the review has nothing for it.
func nothingToFix(repo workspace.Repo) string {
	report, err := os.ReadFile(filepath.Join(repo.Dir, reviewFile))
	switch {
	case err != nil:
		return "there is no review report"
	case review.Clean(string(report)):
		return "the review found nothing to fix"
	}
	return ""
}

// logContract shows the contract in the run's log, so a person can read it
// at the approval gate without opening the repo.
func logContract(repo workspace.Repo, log func(string)) {
	data, err := os.ReadFile(filepath.Join(repo.Dir, "CONTRACT.md"))
	if err != nil {
		return
	}
	log("contract (the full file is " + filepath.Join(repo.Dir, "CONTRACT.md") + "):")
	for line := range strings.Lines(string(data)) {
		if line = strings.TrimRight(line, "\n"); line != "" {
			log("  " + line)
		}
	}
}

// shortcut: skipCall matches any receiver, so a method named Skip or a comment
// can fail a test that does not skip; narrow the match if that happens.
// A narrower match now would let a contract skip its tests through another
// receiver name, and the implementer cannot edit the architect's tests.
var skipCall = regexp.MustCompile(`\.Skip(f|Now)?\(`)

// noSkips fails if a test file in dir calls t.Skip: a skipped test does not
// show that the code works, and a contract with skips can be passed by doing nothing.
func noSkips(dir string) error {
	var bad []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".factory") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if skipCall.Match(data) {
			rel, _ := filepath.Rel(dir, path)
			bad = append(bad, rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(bad) > 0 {
		return fmt.Errorf("test files must not skip tests: %s", strings.Join(bad, ", "))
	}
	return nil
}

// minTests and minRed are the architect's red-before-green rule: the tests
// run on the stubs, and most of them must fail.
const (
	minTests = 5
	minRed   = 0.8
)

// reviewSystem builds the reviewer's instructions: the standards, the review
// rules and the ponytail-review skill.
func reviewSystem(goVersion string) (string, error) {
	block, err := standards.Render(goVersion)
	if err != nil {
		return "", err
	}
	skill, err := standards.Review()
	if err != nil {
		return "", err
	}
	return block + "\n" + reviewerRules + skill, nil
}
