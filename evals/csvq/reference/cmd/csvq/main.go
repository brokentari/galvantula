// Command csvq filters and reshapes CSV read from standard input.
package main

import (
	"cmp"
	"encoding/csv"
	"encoding/json/jsontext"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

// cond is one --where condition.
type cond struct {
	col, val string
	neg      bool // col!=val
	i        int  // index of col in the header
}

func main() {
	var (
		conds  []cond
		limit  = -1 // no limit
		format = "csv"
	)
	flag.Func("where", "keep rows where `col=value` or col!=value; repeatable", func(s string) error {
		col, val, ok := strings.Cut(s, "=")
		if !ok {
			return errors.New("want col=value or col!=value")
		}
		c := cond{val: val}
		c.col, c.neg = strings.CutSuffix(col, "!")
		conds = append(conds, c)
		return nil
	})
	sortCol := flag.String("sort", "", "sort rows by `column`")
	desc := flag.Bool("desc", false, "sort in descending order")
	flag.Func("limit", "keep the first `n` rows", func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return errors.New("want a whole number, 0 or more")
		}
		limit = n
		return nil
	})
	sel := flag.String("select", "", "keep only these comma-separated `columns`")
	flag.Func("format", "output `format`: csv or json", func(s string) error {
		if s != "csv" && s != "json" {
			return errors.New("want csv or json")
		}
		format = s
		return nil
	})
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "csvq: unexpected argument %q\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	}

	r := csv.NewReader(os.Stdin)
	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		fail("no header row")
	}
	if err != nil {
		fail("%v", err)
	}
	for k := range conds {
		conds[k].i = column(header, conds[k].col)
	}
	sortIdx := -1
	if *sortCol != "" {
		sortIdx = column(header, *sortCol)
	}
	names, cols := header, make([]int, len(header))
	for k := range cols {
		cols[k] = k
	}
	if *sel != "" {
		names, cols = strings.Split(*sel, ","), nil
		for _, n := range names {
			cols = append(cols, column(header, n))
		}
	}

	// Read sets FieldsPerRecord from the header, so ReadAll reports the
	// line of any row with the wrong number of fields.
	rows, err := r.ReadAll()
	if err != nil {
		fail("%v", err)
	}
	rows = slices.DeleteFunc(rows, func(row []string) bool {
		return slices.ContainsFunc(conds, func(c cond) bool { return (row[c.i] == c.val) == c.neg })
	})
	if sortIdx >= 0 {
		sortRows(rows, sortIdx, *desc)
	}
	if limit >= 0 {
		rows = rows[:min(limit, len(rows))]
	}
	out := make([][]string, len(rows))
	for k, row := range rows {
		for _, c := range cols {
			out[k] = append(out[k], row[c])
		}
	}
	if format == "json" {
		err = writeJSON(names, out)
	} else {
		err = csv.NewWriter(os.Stdout).WriteAll(append([][]string{names}, out...))
	}
	if err != nil {
		fail("%v", err)
	}
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "csvq: "+format+"\n", a...)
	os.Exit(1)
}

// column returns the index of name in header, or exits if it is not there.
func column(header []string, name string) int {
	i := slices.Index(header, name)
	if i < 0 {
		fail("unknown column %q", name)
	}
	return i
}

// sortRows sorts rows by column i and keeps equal rows in input order. It
// compares numbers when every value in the column is a number, else text.
func sortRows(rows [][]string, i int, desc bool) {
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
		if desc {
			return -c
		}
		return c
	})
}

// writeJSON writes rows as a JSON array of objects whose keys keep the
// column order and whose values are strings.
func writeJSON(names []string, rows [][]string) error {
	enc := jsontext.NewEncoder(os.Stdout, jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	var err error
	put := func(t jsontext.Token) {
		if err == nil {
			err = enc.WriteToken(t)
		}
	}
	put(jsontext.BeginArray)
	for _, row := range rows {
		put(jsontext.BeginObject)
		for k, v := range row {
			put(jsontext.String(names[k]))
			put(jsontext.String(v))
		}
		put(jsontext.EndObject)
	}
	put(jsontext.EndArray)
	return err
}
