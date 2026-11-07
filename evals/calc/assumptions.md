# calc: assumptions

The spec leaves the cases below open. For each case, this file gives what a reasonable builder might do and how the hidden tests treat it.

## Tested readings

- **Arguments and stdin.** The spec says calc reads stdin "with no arguments". The tests assume calc ignores stdin when it has arguments (`TestArgumentsIgnoreStdin`).
- **Last line without a newline.** The tests treat a final line with no `\n` as a line, and calc must evaluate it.
- **Error message format.** The spec says the message "starts with the expression and the zero-based position". The tests require a stderr line that starts with the exact expression text. The text must not be quoted and must not have a prefix such as `calc: `. The first run of digits after the expression must be the position. The tests accept any separator and any text after the position (for example `1+*2: 2: unexpected '*'` or `1+*2 at 2`).
- **"First bad character".** The tests check positions only where a real character is bad. Examples: an operator where an operand must be (`1+*2` → 2), an extra `)` (`1+2)` → 3), and a character that is not allowed (`2$3` → 1). Positions count from the start of the original text, spaces included (`1 + * 2` → 4).
- **Signs are separate tokens.** `2 * - 3` gives `-6`, because "spaces between tokens do not matter" and `-` is a sign token.
- **Whole numbers.** "No decimal point" applies to any magnitude. The tests check `2^20` → `1048576` and `123456*1000` → `123456000`. Shortest `%g` would print `1.048576e+06`, and that output has a decimal point.
- **Negative zero.** The tests make `-0` through `-0`, `-(1-1)`, `0*-1`, `0/-5`, `2*-0` and `(-5)%5`. All must print `0`.
- **Bad options.** The tests check only exit status 2, for `--no-such-option`, `-no-such-option`, and `--no-such-option 1+1`. They do not check stdout or stderr.

## Not tested

- **Arguments that start with `-`**, such as `calc -3`. The Go `flag` package reads them as unknown flags and exits 2. A builder can also read them as expressions. The tests send expressions with a leading sign on stdin.
- **Position at the end of the expression** (`2+`, `(1+2`, `(`). There is no bad character, so the position could be the length of the text or the last token. The tests require an error, exit 1, and a stderr line that starts with the expression and then a number. They do not check the number's value.
- **Byte or character positions** for text that is not ASCII. Not tested.
- **The first error when a later character is also invalid**, such as `1+*$`. A lexer that runs before the parser can report the `$`. Not tested.
- **A sign after `^`** (`2^-2`). The spec says a sign binds less tightly than `^`, so the right operand of `^` may not take a sign. Some builders accept it, and others reject it. Not tested.
- **Repeated signs** at the start (`--3`, `+-3`). Not tested. Signs after a binary operator (`2--3`, `2*-3`) are tested.
- **Number forms that the spec does not show:** `5.` (trailing dot), exponents (`1e3`), `1.2.3`, and leading zeros. Not tested.
- **Whitespace other than spaces between tokens:** tabs, leading or trailing spaces, lines that contain only spaces, `\r\n` line endings, and an empty argument (`calc ""`). Not tested.
- **Two numbers with only a space between them** (`3 4`). A builder that removes all spaces reads `34`. Not tested. (Removing spaces does fail the position tests, because positions then change.)
- **Implicit multiplication** (`2(3)`, `(1)(2)`). Not tested.
- **Formatting at extreme magnitudes.** Examples: `0.0000001` (fixed form or `1e-07`) and `10^21`. The tests use non-whole results only with a magnitude from 0.1 to 4, where shortest fixed and shortest `%g` give the same digits.
- **Infinity and NaN** from overflow or a negative base (`2^10000`, `(0-1)^0.5`). Not tested.
- **Division by zero message.** The spec does not say what the message names. The tests require only exit status 1, no stdout for that expression, and some text on stderr.
- **A syntax error and a division by zero in the same expression** (`1/0+`). Either error can come first. Not tested.
- **Stderr when every expression works**, and the order of stdout and stderr lines. Not tested.
- **`-h`, `--help` and `--`.** Not tested.
