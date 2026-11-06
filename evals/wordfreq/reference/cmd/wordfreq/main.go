// Command wordfreq is the reference answer to evals/wordfreq/spec.md. It
// exists to check the hidden tests; the factory never sees it.
package main

import (
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
	n := flag.Int("n", 10, "lines to print; 0 prints all")
	flag.Parse()
	if *n < 0 {
		fmt.Fprintln(os.Stderr, "wordfreq: -n must not be negative")
		os.Exit(2)
	}

	var text strings.Builder
	if flag.NArg() == 0 {
		if _, err := io.Copy(&text, os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, "wordfreq:", err)
			os.Exit(1)
		}
	}
	for _, name := range flag.Args() {
		data, err := os.ReadFile(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, "wordfreq:", err)
			os.Exit(1)
		}
		text.Write(data)
		text.WriteByte('\n')
	}

	counts := map[string]int{}
	for w := range strings.FieldsFuncSeq(text.String(), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		counts[strings.ToLower(w)]++
	}
	words := slices.SortedFunc(maps.Keys(counts), func(a, b string) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b))
	})
	if *n > 0 && len(words) > *n {
		words = words[:*n]
	}
	for _, w := range words {
		fmt.Println(counts[w], w)
	}
}
