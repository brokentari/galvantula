// Command dedupe prints the lines of its input without repeats. Each
// distinct line is printed once, at the place where it first appeared.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// group is one distinct line: its first spelling and how often it appeared.
type group struct {
	line string
	n    int
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dedupe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fold := fs.Bool("i", false, "compare lines without regard to case")
	count := fs.Bool("c", false, "print the count of each line and a tab before it")
	dups := fs.Bool("d", false, "print only lines that appeared more than once")
	once := fs.Bool("u", false, "print only lines that appeared exactly once")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dups && *once {
		fmt.Fprintln(stderr, "dedupe: -d and -u cannot be used together")
		return 2
	}

	// Read every input before printing, so an unreadable file leaves stdout empty.
	var inputs [][]byte
	if fs.NArg() == 0 {
		data, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "dedupe: standard input: %v\n", err)
			return 1
		}
		inputs = append(inputs, data)
	}
	for _, name := range fs.Args() {
		data, err := os.ReadFile(name)
		if err != nil {
			fmt.Fprintf(stderr, "dedupe: %v\n", err) // *PathError names the file
			return 1
		}
		inputs = append(inputs, data)
	}

	var order []*group
	seen := map[string]*group{}
	for _, data := range inputs {
		for line := range strings.Lines(string(data)) {
			line = strings.TrimSuffix(line, "\n")
			key := line
			if *fold {
				// shortcut: ToLower misses special foldings such as ß and SS, use a full case fold if those matter
				key = strings.ToLower(line)
			}
			g := seen[key]
			if g == nil {
				g = &group{line: line}
				seen[key] = g
				order = append(order, g)
			}
			g.n++
		}
	}

	w := bufio.NewWriter(stdout)
	for _, g := range order {
		if *dups && g.n < 2 || *once && g.n != 1 {
			continue
		}
		if *count {
			fmt.Fprintf(w, "%d\t", g.n)
		}
		w.WriteString(g.line)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintf(stderr, "dedupe: %v\n", err)
		return 1
	}
	return 0
}
