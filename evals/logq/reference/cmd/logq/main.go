// Command logq filters and summarizes log lines.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/brokentari/galvantula/evals/logq/reference/internal/filter"
	"github.com/brokentari/galvantula/evals/logq/reference/internal/parse"
	"github.com/brokentari/galvantula/evals/logq/reference/internal/render"
)

func main() {
	var f filter.Filter
	format := "text"
	flag.Func("level", "keep lines at `level` or above: DEBUG, INFO, WARN or ERROR", func(s string) (err error) {
		f.Level, err = parse.LevelNamed(s)
		return err
	})
	flag.StringVar(&f.Component, "component", "", "keep lines from one `component`")
	flag.Func("where", "keep lines whose field has a value, as `key=value`; repeatable", func(s string) error {
		k, v, ok := strings.Cut(s, "=")
		if !ok || k == "" {
			return errors.New("want key=value")
		}
		f.Where = append(f.Where, filter.Match{Key: k, Value: v})
		return nil
	})
	flag.Func("since", "keep lines at or after `time`, such as 2024-03-05T10:15:30Z", func(s string) (err error) {
		f.Since, err = parse.Time(s)
		return err
	})
	flag.Func("until", "keep lines before `time`", func(s string) (err error) {
		f.Until, err = parse.Time(s)
		return err
	})
	flag.Func("format", "output `format`: text, json or count", func(s string) error {
		if !slices.Contains(render.Formats, s) {
			return fmt.Errorf("unknown format %q", s)
		}
		format = s
		return nil
	})
	flag.Parse() // exits with status 2 on a bad flag value

	bad, err := run(os.Stdout, f, format, flag.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, "logq:", err)
		os.Exit(1)
	}
	if bad > 0 {
		fmt.Fprintf(os.Stderr, "logq: skipped %d bad lines\n", bad)
	}
}

// run writes the kept lines of the named files, or of stdin if there are
// none, to w. It returns the number of bad lines.
func run(w io.Writer, f filter.Filter, format string, names []string) (int, error) {
	bad, kept := 0, 0
	scan := func(name string, r io.Reader) error {
		sc := bufio.NewScanner(r)
		sc.Buffer(nil, 1<<30) // log lines can be longer than the 64 KiB default
		for sc.Scan() {
			e, err := parse.Line(sc.Text())
			if err != nil {
				bad++
				continue
			}
			if !f.Keep(e) {
				continue
			}
			kept++
			if err := render.Entry(w, format, e); err != nil {
				return err
			}
		}
		if err := sc.Err(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}
	if len(names) == 0 {
		if err := scan("stdin", os.Stdin); err != nil {
			return bad, err
		}
	}
	for _, name := range names {
		file, err := os.Open(name)
		if err != nil {
			return bad, err
		}
		err = scan(name, file)
		file.Close()
		if err != nil {
			return bad, err
		}
	}
	if format == "count" {
		return bad, render.Count(w, kept)
	}
	return bad, nil
}
