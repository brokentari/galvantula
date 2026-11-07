package executor

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/brokentari/galvantula/internal/graph"
	"github.com/brokentari/galvantula/internal/plan"
	"github.com/brokentari/galvantula/internal/workspace"
)

// notesFile is where a scoped node writes its notes. Nodes that run in
// parallel must not all write NOTES.md, because their merges would conflict.
func notesFile(node string) string { return ".factory/notes/" + node + ".md" }

// outsideScope lists the files a node wrote, new or changed, that are not in
// its scope. A file is in scope if it sits directly in a scope directory, or if
// it is the node's notes file. It returns nothing for a node with no scope.
func outsideScope(n graph.Node, files []string) []string {
	if len(n.Scope) == 0 {
		return nil
	}
	return slices.DeleteFunc(slices.Clone(files), func(f string) bool {
		return f == notesFile(n.ID) || slices.Contains(n.Scope, path.Dir(f))
	})
}

// testTargets says which packages a node's check runs the tests of, and how
// many tests must run. A node with no scope runs the whole module. A scoped
// node runs only its own packages, because the others may still be stubs
// whose tests fail. It must run at least one test if its packages have any.
func testTargets(wt string, n graph.Node, fallbackMin int) (pkgs []string, min int) {
	if len(n.Scope) == 0 {
		return []string{"./..."}, fallbackMin
	}
	for _, dir := range n.Scope {
		pkgs = append(pkgs, "./"+dir)
		files, _ := filepath.Glob(filepath.Join(wt, filepath.FromSlash(dir), "*_test.go"))
		if len(files) > 0 {
			min = 1
		}
	}
	return pkgs, min
}

// packagesOf lists the directories that hold Go files on main: the packages
// of the contract. The planner must give each one an owner.
func packagesOf(repo workspace.Repo) ([]string, error) {
	files, err := repo.Tracked(repo.Dir)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, f := range files {
		if strings.HasSuffix(f, ".go") && !strings.HasPrefix(f, ".factory/") {
			if d := path.Dir(f); !slices.Contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
	}
	slices.Sort(dirs)
	return dirs, nil
}

const plannerBrief = `Plan how to build the program in this directory in parallel. spec.md says what it must do. The architect has written the contract: CONTRACT.md, stubs that compile, and acceptance tests. Builders will fill in the stubs. Each builder owns some packages and may write files only in them.

Split the work into tasks, one per builder. Rules:
- Each task has an id (lowercase letters, digits and hyphens), a short title, a scope, and deps.
- The scope lists package directories, such as "internal/store". Every package below must be in the scope of exactly one task. Do not add a package that is not in the list.
- deps lists the ids of tasks that must finish first. A builder starts only after its deps are merged. Give a task a dep only if its tests need the other package to work for real, or if it must call code that the other task writes. Packages with no such need get no dep, so that builders run at the same time. A task may depend on tasks only, never on itself.
- At most %d tasks. At most %d tasks in the longest chain of deps. Keep the plan small: one task for a package that is small, and do not split a package.
- Do not use the ids standards, architect, plan, tasks, review, fix or verify, or an id that starts with approve-. Other nodes have them.

Answer with a JSON object and nothing else, in this shape:

%s

The packages of the contract:

%s
`

// plannerPrompt lists the contract's packages for the planner.
func plannerPrompt(repo workspace.Repo, _ Task) (string, error) {
	pkgs, err := packagesOf(repo)
	if err != nil {
		return "", err
	}
	if len(pkgs) == 0 {
		return "", fmt.Errorf("the contract has no Go package to plan for")
	}
	var list []string
	for _, p := range pkgs {
		list = append(list, "- "+p)
	}
	return fmt.Sprintf(plannerBrief, plan.MaxTasks, plan.MaxDepth, plan.Schema, strings.Join(list, "\n")), nil
}

// planReport parses and validates the planner's answer and returns the nodes
// that fill the task-graph slot. It logs the tasks, for the person who
// approves them at the gate, with a ceiling on what one attempt of each costs.
func planReport(c Claude, repo workspace.Repo, text string, log func(string)) ([]graph.Node, error) {
	out, err := plan.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("plan rejected: %w", err)
	}
	pkgs, err := packagesOf(repo)
	if err != nil {
		return nil, &InfraError{Err: err}
	}
	if err := plan.Validate(out, pkgs); err != nil {
		return nil, fmt.Errorf("plan rejected:\n%w", err)
	}
	for _, t := range out.Tasks {
		after := "starts at once"
		if len(t.Deps) > 0 {
			after = "after " + strings.Join(t.Deps, ", ")
		}
		log(fmt.Sprintf("task %s (%s): %s", t.ID, after, strings.Join(t.Scope, ", ")))
	}
	log(fmt.Sprintf("%d tasks. One attempt of each costs at most $%.2f, and a task may retry %d times.",
		len(out.Tasks), float64(len(out.Tasks))*c.Rails.Budget.NodeUSD, plan.Retries))
	return plan.Nodes(out), nil
}

// implementerNotes tells an implementer where to write its notes and, if it
// has a scope, which files it may write.
func implementerNotes(t Task) string {
	if len(t.Node.Scope) == 0 {
		return "- Write NOTES.md: in a few lines, what you built and each assumption you made.\n"
	}
	return fmt.Sprintf(`- You own only these package directories: %s. Write files directly in them and nowhere else, except your notes. Other builders own the other packages and work at the same time. You may read their code, but anything you write there is refused, and it would conflict with theirs. Do not make new directories.
- Write your notes to %s (make the .factory/notes folder if needed), not to NOTES.md: in a few lines, what you built and each assumption you made.
- Your check runs the tests of your packages only.
`, strings.Join(t.Node.Scope, ", "), notesFile(t.Node.ID))
}
