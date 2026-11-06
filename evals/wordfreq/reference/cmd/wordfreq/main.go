// Command wordfreq prints the most common words in its input with their counts.
package main

import (
	"bufio"
	"cmp"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"unicode"
)

func main() {
	n := flag.Int("n", 10, "number of lines to print; 0 prints all")
	flag.Parse()
	if *n < 0 {
		fmt.Fprintln(os.Stderr, "wordfreq: -n must not be negative")
		os.Exit(2)
	}

	counts := map[string]int{}
	add := func(data []byte) {
		notWord := func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }
		for _, w := range strings.FieldsFunc(string(data), notWord) {
			counts[strings.ToLower(w)]++
		}
	}
	if flag.NArg() == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "wordfreq: stdin:", err)
			os.Exit(1)
		}
		add(data)
	}
	for _, name := range flag.Args() {
		data, err := os.ReadFile(name)
		if err != nil {
			// The *PathError from os.ReadFile names the file.
			fmt.Fprintln(os.Stderr, "wordfreq:", err)
			os.Exit(1)
		}
		add(data)
	}

	words := slices.SortedFunc(maps.Keys(counts), func(a, b string) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b))
	})
	if *n > 0 && *n < len(words) {
		words = words[:*n]
	}
	out := bufio.NewWriter(os.Stdout)
	for _, w := range words {
		fmt.Fprintln(out, counts[w], w)
	}
	if err := out.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, "wordfreq:", err)
		os.Exit(1)
	}
}
