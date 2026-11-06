// Command csvq is the reference answer to evals/csvq/spec.md. It exists to
// check the hidden tests; the factory never sees it.
package main

import (
	"cmp"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

type cond struct {
	col, value string
	equal      bool
}

type conds []cond

func (c *conds) String() string { return fmt.Sprint(*c) }

func (c *conds) Set(s string) error {
	if col, v, ok := strings.Cut(s, "!="); ok {
		*c = append(*c, cond{col, v, false})
		return nil
	}
	col, v, ok := strings.Cut(s, "=")
	if !ok {
		return errors.New("want col=value or col!=value")
	}
	*c = append(*c, cond{col, v, true})
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "csvq:", err)
	os.Exit(1)
}

func main() {
	var where conds
	flag.Var(&where, "where", "keep rows where col=value or col!=value (repeatable)")
	sortCol := flag.String("sort", "", "sort by this column")
	desc := flag.Bool("desc", false, "sort descending")
	limit := flag.Int("limit", 0, "keep the first n rows")
	sel := flag.String("select", "", "comma-separated columns to keep")
	format := flag.String("format", "csv", "csv or json")
	flag.Parse()
	limited := false
	flag.Visit(func(f *flag.Flag) { limited = limited || f.Name == "limit" })
	if (*format != "csv" && *format != "json") || *limit < 0 {
		fmt.Fprintln(os.Stderr, "csvq: --format must be csv or json, and --limit must not be negative")
		os.Exit(2)
	}

	r := csv.NewReader(os.Stdin)
	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		fail(errors.New("input has no header row"))
	}
	if err != nil {
		fail(err)
	}
	col := func(name string) int {
		i := slices.Index(header, name)
		if i < 0 {
			fail(fmt.Errorf("no column %q", name))
		}
		return i
	}
	for i := range where {
		col(where[i].col)
	}

	var rows [][]string
	for {
		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			fail(err) // csv.ParseError names the line
		}
		if !slices.ContainsFunc(where, func(c cond) bool { return (row[col(c.col)] == c.value) != c.equal }) {
			rows = append(rows, row)
		}
	}

	if *sortCol != "" {
		i := col(*sortCol)
		numeric := !slices.ContainsFunc(rows, func(row []string) bool {
			_, err := strconv.ParseFloat(row[i], 64)
			return err != nil
		})
		slices.SortStableFunc(rows, func(a, b []string) int {
			c := cmp.Compare(a[i], b[i])
			if numeric {
				x, _ := strconv.ParseFloat(a[i], 64)
				y, _ := strconv.ParseFloat(b[i], 64)
				c = cmp.Compare(x, y)
			}
			if *desc {
				return -c
			}
			return c
		})
	}
	if limited && len(rows) > *limit {
		rows = rows[:*limit]
	}

	cols := header
	if *sel != "" {
		cols = strings.Split(*sel, ",")
	}
	idx := make([]int, len(cols))
	for j, name := range cols {
		idx[j] = col(name)
	}

	if *format == "json" {
		out := make([]map[string]string, 0, len(rows))
		for _, row := range rows {
			obj := make(map[string]string, len(cols))
			for j, name := range cols {
				obj[name] = row[idx[j]]
			}
			out = append(out, obj)
		}
		if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
			fail(err)
		}
		return
	}
	w := csv.NewWriter(os.Stdout)
	w.Write(cols)
	for _, row := range rows {
		picked := make([]string, len(idx))
		for j, i := range idx {
			picked[j] = row[i]
		}
		w.Write(picked)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		fail(err)
	}
}
