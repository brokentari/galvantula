# Assumptions

The spec is one paragraph. This file lists each place where it is unclear, what a reasonable builder might do, and how the hidden tests treat it.

## The JSON file

- **Schema.** The spec says only that the list is kept "in a JSON file". The tests check that the file exists at the expected path and parses as JSON. They never read its fields.
- **Corrupt, empty or unreadable file; missing directory; write errors.** The spec gives no exit status for these. Not tested.
- **Read-only commands.** The spec does not say whether `list`, a rejected command or a repeated `done` writes the file. Not tested.

## Output

- **Line endings.** "One per line" and the fixed messages (`added <id>` and so on) are checked exactly, each line ending in `\n`.
- **Empty list.** `list` and `list --all` print nothing when no item matches. A builder that prints a message such as "no items" fails.
- **Order of `list --all`.** The spec says "in id order" only for `list`. The tests expect `list --all` in id order too.
- **Repeated `done`.** The spec says `done <id>` prints `done <id>` and a repeat is "not an error". The tests expect `done <id>` and exit 0 again. A builder that prints "already done" fails.
- **Special characters.** The tests add text with quotes, a backslash, a tab, `%` verbs and non-ASCII characters, and expect it back unchanged. Text with a newline is not tested, because it breaks "one per line".

## Text for `add`

- **Joining.** "Joins its arguments with single spaces" is read as `strings.Join(args, " ")`. Spaces inside one argument stay. A builder that collapses all whitespace fails.
- **Empty text.** The tests use `add` with no arguments and `add ""`. Text of only spaces (`add " "`) and several empty arguments (`add "" ""`, which joins to one space) are not tested.
- **Text that looks like a flag** (`add -x`, `add --all`). Not tested.

## Errors

- **Exit 1 (unknown id).** The tests check exit 1, empty stdout and a non-empty stderr. The spec does not say that the message names the id, so the tests do not require it. An id counts as unknown when it was removed, was never given out, or the file is missing.
- **Exit 2 (usage).** The spec gives only the status. The tests check the exit code only, not stdout or stderr, so usage text can go to either stream.
- **Exit 2 changes nothing.** The tests expect a rejected command to leave the list as it was. They also expect a rejected `add` to use up no id: the next `add` gets the next id.
- **Cases tested for exit 2:** an unknown subcommand (`frobnicate`), `add` with empty text, `done` and `rm` with no id, and `done` and `rm` with `abc`.
- **Not tested:** no subcommand at all; `help`, `-h` and `--help`; extra arguments (`done 1 2`, `list foo`); ids such as `0`, `-1`, `+1`, `01`, `1.5` or numbers too large for an int. A builder could fairly call `0` unknown (exit 1) or invalid (exit 2).

## The `--file` flag

- **Form.** The tests use only `--file <path>` before the subcommand. `--file=<path>`, `-file` and `--file` after the subcommand are not tested.
- **Relative paths.** A relative path is resolved against the working directory. Tested.
- **Default.** With no flag, the list is `todo.json` in the working directory. The tests check the file there and read it back through `--file <absolute path>`.

## Atomic save

The spec says to write a temporary file in the same directory and rename it over the list. A black-box test cannot crash the program halfway through a write, so the tests check three effects instead:

1. After each `add`, `done` and `rm`, the list path refers to a new file (`os.SameFile` is false). A program that writes the list in place fails.
2. After each save, the directory holds only the list file. The temporary file became the list, so nothing is left over. A program that leaves a temporary, backup or lock file next to the list fails.
3. With `TMPDIR` set to a missing directory, saves still work. A program that creates its temporary file in the system temp directory fails.

Limits: a program that deletes the old file and then writes the new one directly passes all three checks. File mode, `fsync`, symlinked list files and concurrent runs are not tested.
