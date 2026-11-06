# wordfreq: assumptions

This file lists each place where the spec is unclear. For each one, it gives what a reasonable builder might do and how the hidden tests treat it.

## Tested readings

- **Several files.** The spec says the tool reads "the files named as arguments" and prints one list. A builder could print one list for each file, but one combined list is the plain reading. The tests require one combined list, and the order of the files must not change it.
- **Standard input when files are given.** The tool reads standard input only "when there are none". The tests send text on stdin together with a file argument, and require that the stdin text is not counted.
- **Line ends.** "One line per word" means that every line ends in `\n`, the last line too. The tests compare stdout exactly. No header, padding or extra text is permitted.
- **Exit status on success.** The spec gives status 0 only for empty input. The tests require status 0 for every run that succeeds, because that is the convention.
- **Input with no words.** Input that holds only separators (spaces, punctuation, dashes) has no words, so it gives no lines. The tests require no output and status 0, the same as empty input.
- **Separators.** "Any other character separates words." The tests check a wide set: space, tab, `\n`, `\r\n`, `-`, `_`, `'`, `’`, `—`, `«`, the no-break space, an emoji, and ASCII punctuation. A builder who uses the regexp `\w+` keeps `_` in words. That build fails.
- **Digits in words.** Runs of ASCII digits are words (`2024`), and letters and digits in one run make one word (`abc123`). `3.14` gives the words `3` and `14`.
- **Unicode letters and lower case.** The tests use precomposed Latin letters (`ï`, `é`), Cyrillic and CJK. For these letters, simple lower-casing and case folding give the same result. Upper-case forms must count with lower-case forms and print in lower case.
- **Ties use the lower-case word.** `Cherry banana Apple` must print `apple`, `banana`, `cherry`. A build that sorts the original spelling fails.
- **Counts sort as numbers.** The test uses the counts 10, 9 and 2, which sort in a different order as text.
- **`-n` larger than the number of words.** The tool prints all the words and nothing more.
- **`-n` cuts inside a tie.** The tool applies the alphabetical tie-break before it cuts the list.
- **Bad flag.** An unknown flag, `-n` with no value, and `-n abc` are all bad flags, so each must exit with status 2. Go's `flag` package does this by default. A builder who parses flags by hand could give a different status for `-n abc`. The tests require 2.
- **Negative `-n`.** `-n -1` and `-n -5 file` must exit with status 2.
- **Flag errors and output.** The spec does not say what a flag error prints. The tests check only the exit status. They do not check stderr or stdout.
- **File that cannot be read.** The tests use three cases: a missing file, a file with mode 000, and a directory named as a file. Each must exit with status 1, and stderr must contain the name as it was given on the command line. When the tests run as root, mode 000 does not stop reads, so that test is skipped. A directory cannot be read as text, so the tests treat it as a file that cannot be read. A build that opens the directory and ignores the read error fails.
- **Stdout when the only file cannot be read.** In this case the tool read no words, so the tests require empty stdout.
- **Long input.** The spec sets no limit on line length. The tests send about 2 MB on one line. A build with `bufio.Scanner` and its default 64 KB line limit fails. A build that raises the limit to 1 MB also fails.

## Left open (not tested)

- **Unreadable file among readable files.** The tests require status 1 and the bad file's name on stderr. They do not check stdout. They also do not check whether the tool stops at the first bad file or reports each bad file.
- **A word across a file boundary.** If one file ends in a letter and the next file starts with one, the tool can join them or not. In the tests, each file that is read with another file ends with a newline.
- **Alphabetical order outside ASCII.** Byte order and locale collation give different orders for words such as `é` and `z`. The tests break ties only between ASCII lower-case words. Shorter prefixes come first in both orders.
- **Ties between digits and letters.** The tests have no ties between words that start with a digit and words that start with a letter.
- **Special case rules.** Turkish dotted and dotless I, Greek final sigma, `ß` and title-case letters are not tested.
- **Other Unicode numbers.** The tests do not use non-ASCII decimal digits (for example `٣`) or other number characters (`²`, `½`, `Ⅻ`). The spec does not say if "digits" means only ASCII digits, all decimal digits, or all numbers.
- **Combining marks.** A decomposed `é` (`e` plus U+0301) can stay one word or become two. The tests use only precomposed letters.
- **Invalid UTF-8.** Not tested.
- **Flag forms.** The tests use `-n N` as two arguments before any file names. `-n=N`, `--n` and flags after file names are not tested.
- **`-` as a file name.** Not tested.
- **The same file named twice.** Not tested.
- **Both a negative `-n` and a missing file.** Status 1 or 2 can both be correct. Not tested.
- **Read error on standard input.** Not tested.
- **Stderr on success.** Not checked.
- **Error message text.** The tests check only that stderr contains the file name. The rest of the message, and an absolute path in place of the given name, are both acceptable.
