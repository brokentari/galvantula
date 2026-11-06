# csvq: assumptions

This file lists each place where the spec is unclear. For each one, it gives what a reasonable builder might do and how the hidden tests treat it.

## Output format

- **CSV bytes.** The spec says "writes CSV with a header row". It does not fix the quoting style (quote only where necessary, or quote every field), the line ending (`\n` or `\r\n`), or whether the last line ends with a newline. The tests parse stdout with `encoding/csv` and compare the records with the expected records. Any valid CSV with the correct records passes. The reference uses `encoding/csv`, which quotes only where necessary and writes `\n`.
- **JSON whitespace and key order.** The spec fixes the keys and the string values but not the whitespace or the key order. A builder that uses a Go map gets keys in alphabetical order. The tests decode stdout and compare the values. They do not check key order or whitespace. They do check that every value is a JSON string, so `"30"` passes and `30` fails. The reference writes the keys in the selected order.
- **The empty JSON array.** The spec says that with no rows the output is `[]`. The tests remove all whitespace from stdout and then require `[]`. Thus `[\n]` passes and `null` fails.

## Errors

- **Stdout on error.** The spec does not say if a failing run can write rows before it finds the error. For example, a program that streams output can find a bad row after it writes the header. The error tests do not check stdout.
- **Error wording.** The tests check only that stderr contains the column name, or the line number as a whole number. For example, for line 4 the tests accept "line 4:" and "4: bad row" but not "14".
- **Line numbers.** The tests use the line number in the input, with the header on line 1. A builder that counts data rows from 1 fails. With a quoted field that contains a newline, "the line number" can mean the first line of the record or the line where the error is. The tests do not use quoted newlines in error cases.
- **No header row.** The spec says "and so does input with no header row". This can mean that the error also names a line number, or only that it prints an error and exits with status 1. The tests give empty stdin and check exit status 1 and stderr that is not empty. They do not check for a line number.
- **Text for status 2.** The spec says only "exits with status 2" for a bad flag value. The tests check only the exit status, not stderr.
- **A limit that is not a number.** The spec gives examples of bad flag values ("such as"). The tests treat `--limit abc` as a bad flag value (status 2). `--limit 0` is not negative, so the tests expect zero rows.
- **A bad row and a filter.** The tests expect an error for a bad row even when `--where` removes that row, because the row is still in the input. They do not combine a bad row with `--limit`. A streaming program can stop before it reads the bad row, and the spec does not say whether it must read the remaining input.
- **More than one error.** The spec does not say which error wins when a run has more than one problem, for example an unknown column and a bad row. The tests have only one problem in each case. The flag tests always give valid stdin.
- **Unknown flags and arguments.** The spec does not give the exit status for an unknown flag or for an argument that is not a flag. The tests do not check these cases. The reference exits with status 2 for both.

## `--where`

- **Exact match.** The tests take "equals the value exactly" to mean string equality. Case must match, a part of a value does not match, and `age=25.0` does not match `25`.
- **Not tested:** a value that contains `=` (`--where expr=a=b`), a column name that ends in `!`, an empty column name (`--where =x`), an empty value (`--where dept=`), and spaces at the start or end of a field. The reference splits at the first `=`. If the character before that `=` is `!`, the reference uses `!=`.

## `--sort`

- **What counts as a number.** The tests use only integers, negative numbers and decimals such as `-4.5`. They do not use `1e3`, `+5`, `NaN`, `Inf`, hexadecimal values, values with spaces, or the empty string. The reference accepts everything that `strconv.ParseFloat` accepts.
- **"Every value in the column".** Filtering happens before sorting, so this can mean all input rows or only the rows that the filter keeps. The tests never filter out the only value that is not a number. The reference checks only the rows that the filter keeps.
- **Values that are equal as numbers but different as text,** such as `1` and `1.0`. Equal values keep their input order, but a builder can also compare the text when the numbers are equal. The tests do not use such values.
- **Text order.** The tests use lowercase ASCII text, so byte order, locale order and case-insensitive order give the same result. The reference uses byte order.
- **Not tested:** `--desc` without `--sort`, and `--sort` given more than one time.

## `--select`, `--limit`, `--format`

- **Not tested:** spaces around names (`--select "a, b"`), the same name two times (`--select a,a`), an empty list (`--select ""`), and a flag other than `--where` given more than one time.
- **Format case.** The tests do not use `--format JSON` or `--format CSV`.

## Input

- **Tested:** quoted fields that contain commas and doubled quotes, and a last line without a newline. These are basic CSV features.
- **Not tested:** `\r\n` line endings, blank lines, a byte order mark, quoted fields that contain newlines, and duplicate column names in the header.

## Flag syntax

The tests use only the `--flag value` form that the spec shows. They do not use `--flag=value` or a single dash.
