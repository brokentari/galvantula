// Command todo is the reference answer to evals/todo/spec.md. It exists to
// check the hidden tests; the factory never sees it.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type item struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type list struct {
	NextID int    `json:"next_id"`
	Items  []item `json:"items"`
}

func usage(msg string) {
	fmt.Fprintln(os.Stderr, "todo:", msg)
	fmt.Fprintln(os.Stderr, "usage: todo [--file path] add <text...> | list [--all] | done <id> | rm <id>")
	os.Exit(2)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "todo:", err)
	os.Exit(1)
}

func load(path string) list {
	l := list{NextID: 1}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return l
	}
	if err != nil {
		fail(err)
	}
	if err := json.Unmarshal(data, &l); err != nil {
		fail(fmt.Errorf("%s: %w", path, err))
	}
	return l
}

func save(path string, l list) {
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		fail(err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".todo-*")
	if err != nil {
		fail(err)
	}
	_, err = tmp.Write(data)
	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
		fail(err)
	}
}

func id(args []string) int {
	if len(args) != 1 {
		usage("need exactly one id")
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		usage("id must be a number: " + args[0])
	}
	return n
}

func find(l list, n int) int {
	i := slices.IndexFunc(l.Items, func(it item) bool { return it.ID == n })
	if i < 0 {
		fail(fmt.Errorf("no item %d", n))
	}
	return i
}

func main() {
	path := flag.String("file", "todo.json", "list file")
	flag.Parse()
	if flag.NArg() == 0 {
		usage("missing subcommand")
	}
	cmd, args := flag.Arg(0), flag.Args()[1:]

	switch cmd {
	case "add":
		text := strings.Join(args, " ")
		if text == "" {
			usage("add needs text")
		}
		l := load(*path)
		l.Items = append(l.Items, item{ID: l.NextID, Text: text})
		l.NextID++
		save(*path, l)
		fmt.Println("added", l.NextID-1)
	case "list":
		fl := flag.NewFlagSet("list", flag.ContinueOnError)
		all := fl.Bool("all", false, "show done items too")
		if err := fl.Parse(args); err != nil || fl.NArg() > 0 {
			usage("list takes only --all")
		}
		for _, it := range load(*path).Items {
			switch {
			case !*all && !it.Done:
				fmt.Println(it.ID, it.Text)
			case *all:
				mark := map[bool]string{false: "[ ]", true: "[x]"}[it.Done]
				fmt.Println(it.ID, mark, it.Text)
			}
		}
	case "done":
		n := id(args)
		l := load(*path)
		l.Items[find(l, n)].Done = true
		save(*path, l)
		fmt.Println("done", n)
	case "rm":
		n := id(args)
		l := load(*path)
		i := find(l, n)
		l.Items = slices.Delete(l.Items, i, i+1)
		save(*path, l)
		fmt.Println("removed", n)
	default:
		usage("unknown subcommand " + cmd)
	}
}
