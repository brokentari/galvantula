// Command todo keeps a to-do list in a JSON file.
package main

import (
	"encoding/json/v2"
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

// list keeps the last id it gave out, so rm never frees an id for reuse.
type list struct {
	LastID int    `json:"last_id"`
	Items  []item `json:"items"`
}

func main() { os.Exit(run(os.Args[1:])) }

func usage() {
	fmt.Fprintln(os.Stderr, "usage: todo [--file path] add <text...> | list [--all] | done <id> | rm <id>")
}

func run(args []string) int {
	flags := flag.NewFlagSet("todo", flag.ContinueOnError)
	flags.Usage = usage
	path := flags.String("file", "todo.json", "path of the list file")
	if flags.Parse(args) != nil {
		return 2
	}
	if flags.NArg() == 0 {
		usage()
		return 2
	}
	l, err := load(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "todo:", err)
		return 1
	}
	cmd, rest := flags.Arg(0), flags.Args()[1:]
	switch cmd {
	case "add":
		text := strings.Join(rest, " ")
		if text == "" {
			fmt.Fprintln(os.Stderr, "todo: add needs text")
			return 2
		}
		l.LastID++
		l.Items = append(l.Items, item{ID: l.LastID, Text: text})
		return commit(*path, l, fmt.Sprint("added ", l.LastID))
	case "list":
		lf := flag.NewFlagSet("list", flag.ContinueOnError)
		lf.Usage = usage
		all := lf.Bool("all", false, "show done items too")
		if lf.Parse(rest) != nil {
			return 2
		}
		// Items stay in id order: add appends the next id and rm keeps the order.
		for _, it := range l.Items {
			switch {
			case *all && it.Done:
				fmt.Printf("%d [x] %s\n", it.ID, it.Text)
			case *all:
				fmt.Printf("%d [ ] %s\n", it.ID, it.Text)
			case !it.Done:
				fmt.Printf("%d %s\n", it.ID, it.Text)
			}
		}
		return 0
	case "done", "rm":
		if len(rest) != 1 {
			usage()
			return 2
		}
		id, err := strconv.Atoi(rest[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "todo: id %q is not a number\n", rest[0])
			return 2
		}
		i := slices.IndexFunc(l.Items, func(it item) bool { return it.ID == id })
		if i < 0 {
			fmt.Fprintf(os.Stderr, "todo: no item with id %d\n", id)
			return 1
		}
		if cmd == "rm" {
			l.Items = slices.Delete(l.Items, i, i+1)
			return commit(*path, l, fmt.Sprint("removed ", id))
		}
		l.Items[i].Done = true
		return commit(*path, l, fmt.Sprint("done ", id))
	}
	fmt.Fprintf(os.Stderr, "todo: unknown subcommand %q\n", cmd)
	usage()
	return 2
}

// load reads the list at path. A missing file is an empty list.
func load(path string) (list, error) {
	var l list
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &l)
	}
	return l, err
}

// commit saves l to path, prints msg and returns the exit status.
func commit(path string, l list, msg string) int {
	if err := save(path, l); err != nil {
		fmt.Fprintln(os.Stderr, "todo:", err)
		return 1
	}
	fmt.Println(msg)
	return 0
}

// save writes a temporary file next to path and renames it over path, so a
// crash leaves the old list or the new one, never half of one.
func save(path string, l list) error {
	data, err := json.Marshal(l)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".todo-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // fails harmlessly after a successful rename
	_, err = f.Write(data)
	if err = errors.Join(err, f.Sync(), f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
