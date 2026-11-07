# dedupe: assumptions

This file lists each place where `spec.md` is unclear or says nothing. For each place, it gives what a reasonable builder might do and how the hidden tests treat it.

## Tested

These readings follow from the spec text. The tests check them.

1. **Format of the `-c` count.** The spec says "the number of times each line appeared, then a tab, before it". A builder who copies `uniq -c` might pad the number with spaces. The tests require a plain decimal number with no padding, then one tab, then the line: `3\tfoo\n`.
2. **Order with `-c`, `-i` and `-u`.** The spec gives first-appearance order for the default mode. `-c` only adds a prefix, `-i` only changes how lines compare, and `-u` lines appear only once. The tests require first-appearance order in all three modes.
3. **`-d` prints each repeated line once.** `uniq -D` prints every copy. The spec says the tool prints each distinct line once, and `-d` prints "only" the repeated ones. The tests require one copy of each repeated line.
4. **Counts with `-d`, `-u` and `-i`.** The count is for the whole group. With `-i`, the count includes every spelling of the line. The tests check `-c` together with `-d`, `-u` and `-i`.
5. **Spaces are part of the line.** The spec says a line with only spaces is a line. The tests also require that `a` and `a ` are different lines, and that ` ` and `  ` are different lines. A builder that trims whitespace fails.
6. **Last line of a file that is not the last file.** The spec says a last line with no newline counts as a line, and that lines from several files form one list. The tests require that a file which ends without a newline still ends its last line there. A builder that joins the files into one stream first (for example with `io.MultiReader`) joins that line to the first line of the next file and fails.
7. **Same file named twice.** The tests count its lines twice, because the files form one list in argument order.
8. **Standard input when files are named.** The spec says to read standard input only when there are no file arguments. The tests give standard input and a file, and require output from the file only.
9. **No output when any file fails.** The spec says to print nothing to standard output when a file cannot be read. The tests put a readable file before the bad one and require empty standard output.
10. **A directory as an argument.** The tests treat a directory as a file that cannot be read: exit 1, the directory name on standard error, no standard output. A builder that opens the directory and ignores the read error fails.
11. **Long lines.** The spec gives no limit on line length. The tests use a 100,000-byte line. A builder that uses `bufio.Scanner` with its default 64 KiB limit fails.
12. **Case folding beyond ASCII.** "Without regard to case" has no ASCII limit in the spec. The tests use `Ä`/`ä` and `É`/`é`, which have one simple case mapping each. An ASCII-only comparison fails.
13. **Flag order.** The tests give flags as separate arguments, before any file names, in both orders (`-i -c` and `-c -i`).

## Not tested

The spec leaves these open. The tests do not depend on them.

1. **Order with `-d`.** The spec states first-appearance order "by default". A builder could print a repeated line when it first sees the repeat. The `-d` tests use inputs where the repeated lines come in the same order by first, second and last appearance.
2. **Carriage returns.** "Lines end at a newline" suggests that a `\r` before the newline is part of the line. But `bufio.Scanner` removes it. No test has a `\r`.
3. **Stderr and stdout for usage errors.** The spec fixes only exit status 2. The tests do not check what the program prints for a bad flag or for `-d` with `-u`.
4. **Error message wording.** The tests only check that standard error contains the file name as given on the command line.
5. **Several unreadable files.** The tests use one bad file per run. They do not check whether the program names the first, the last or all bad files.
6. **Order of checks.** No test combines a usage error with an unreadable file, or `-d -u` with empty input.
7. **Special case foldings.** The tests do not use `ß`/`SS`, the Turkish dotted and dotless `i`, the final sigma or the Kelvin sign. `strings.ToLower`, `strings.ToUpper` and `strings.EqualFold` give different answers for these.
8. **Help flags.** The tests do not use `-h` or `--help`. Go's `flag` package exits 0 for them with `ExitOnError`.
9. **Combined and late flags.** The tests do not use `-ci`, flags after file names, `--` or `-` for standard input.
10. **Binary input.** No test has invalid UTF-8 or NUL bytes.
11. **Read and write errors on the streams.** No test makes standard input fail or closes standard output.
12. **Unreadable files by permission.** A test that uses `chmod 000` passes when the tests run as root. The missing-file and directory tests cover the same rule.
