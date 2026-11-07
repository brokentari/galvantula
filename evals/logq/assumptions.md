# logq: assumptions

This file lists each place where the spec is unclear or leaves a choice open. For each one, it says what a reasonable builder might do and how the hidden tests treat it.

## Not tested

These behaviors are open. The tests avoid them.

- **Package layout.** The spec names four packages: `internal/parse`, `internal/filter`, `internal/render` and `cmd/logq`. The tests use only the built command, so they cannot see the layout. The reference uses the four packages.
- **Timestamp details.** The spec says "a UTC timestamp" and gives `2024-03-05T10:15:30Z`. A builder can accept or reject fractional seconds, a `+00:00` offset, other offsets, or a lowercase `z`. The tests use only the form `YYYY-MM-DDTHH:MM:SSZ`, in log lines and in `--since` and `--until`.
- **Letter case of levels.** A builder can accept `warn` or `Warn`, in log lines and in `--level`. The tests use uppercase levels. The unknown level in the tests is `TRACE`, which every builder rejects.
- **Component names.** The spec does not say which characters a component can have. The tests use lowercase letters only.
- **Spacing and line endings.** The tests use one space between parts, LF line endings, and a newline at the end of each file. These cases stay open: more than one space, tabs, leading or trailing spaces, CRLF endings, a last line with no newline, and blank lines. A blank line does not fit the line form, but a builder can also skip it without counting it as bad.
- **Field syntax edge cases.** These cases stay open: an empty value (`k=`), an empty key, a duplicate key, an `=` in an unquoted value, a quote in an unquoted value, backslash escapes other than `\"`, text directly after a closing quote, and a quoted value with no spaces. The tests use plain unquoted values, quoted values with spaces, and `\"` in a quoted value.
- **`--where` edge cases.** These cases stay open: an empty key, an empty value, a value that contains `=`, and the same key given twice.
- **Flag syntax.** The tests give each flag before the file names, as two separate arguments (`--level WARN`). These cases stay open: the `--level=WARN` form, flags after file names, unknown flags, a flag with no value, a repeated `--level`, `--component` or `--format`, and `-` as a name for stdin.
- **Messages for bad flag values.** The spec gives only the exit status, 2. The tests check exit 2 and an empty stdout. They do not check stderr.
- **More than one error.** The spec does not say what happens when a run has both a bad flag value and a missing file. The tests do not combine errors.
- **Output before a file error.** When the first file is good and the second file is missing, a builder can print the lines of the first file before it stops, or print nothing. A builder can also continue to later files. The test checks only exit 1 and that stderr names the missing file. It does not check stdout.
- **Order of stdout and stderr.** The spec puts the bad-line note "after the output". The tests read the two streams separately and cannot see the order.
- **Exactly one bad line.** The spec gives the note as `skipped N bad lines`. A builder can print `skipped 1 bad line` for one line. The tests always use two or more bad lines.
- **JSON layout.** The spec does not fix key order, spacing, or whether each object is on its own line. The tests decode stdout as a series of JSON values. They accept any layout.
- **JSON with no fields.** A Go builder can print `"fields": {}` or `"fields": null`. The test accepts both.
- **Empty input.** The tests do not run with empty stdin.

## Tested, with a judgment call

The spec does not say these things word for word. The tests treat them as follows.

- **`--where` compares the field's value without its quotes.** The value of `msg="bad password"` is `bad password`, and the value of `msg="she said \"hi\" twice"` is `she said "hi" twice`. The spec says the field must have "exactly that value", and the JSON format shows field values as strings. So `--where 'msg=bad password'` matches. A builder that compares the raw text, quotes included, fails two subtests of `TestWhere`.
- **A line without the field fails a `--where` condition.** The spec keeps lines "whose field has exactly that value". A line with no such field does not have that value.
- **`--where` with no `=` is a bad flag value.** The flag takes `key=value`, so `--where user` exits with status 2.
- **The JSON `time` value is the timestamp string as written.** For the line at `2024-03-05T10:15:30Z`, the test expects the string `2024-03-05T10:15:30Z`.
- **A JSON object has only the four keys.** The spec lists `time`, `level`, `component` and `fields`. The test fails on any other key. The test also fails if a field value is not a JSON string. For example, `ms` must be `"120"` and not `120`.
- **Count output ends in a newline.** The test expects `3 lines\n` exactly. The spec says "the word `lines`", so one kept line gives `1 lines\n`. A builder that prints `1 line` fails one subtest of `TestCount`.
- **With no kept lines, count prints `0 lines`.** The spec says that text and json print nothing when no lines are kept. It does not include count, so count still prints its total.
- **Bad lines do not change the exit status.** The spec gives status 1 only for a file that cannot be read, and status 2 only for a bad flag value. A run with bad lines exits 0.
- **Lines that a filter drops are not bad.** Only lines that do not fit the line form count as bad. The tests fail if a run that only filters lines prints a note that has the word `skipped`.
- **The note gives one total for all input.** With bad lines in two files, the test expects one note with the total, such as `skipped 3 bad lines`. The note can have a prefix, such as `logq: `.
- **The note appears when no lines are kept.** The spec prints the note "if any lines were bad", whatever the output.
- **A directory is a file that cannot be read.** `logq old.log`, where `old.log` is a directory, exits with status 1 and names `old.log` on stderr. A builder that does not check read errors prints nothing and exits 0, so it fails this test.
- **Stdin is ignored when there are file names.** The file tests also send a line on stdin. That line must not appear in the output.
- **Bad lines.** Each of these lines must count as bad: text with no timestamp, a timestamp with a space instead of `T`, an unknown level (`TRACE`), a line with no component, a word with no `=` after the fields start, and a quoted value with no closing quote.
