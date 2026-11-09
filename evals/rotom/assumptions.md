# rotom: assumptions in the hidden tests

The tests start the built `rotom` with `--addr 127.0.0.1:0` and talk to it over TCP. The list below names each place where the spec is unclear, what a reasonable builder might do, and how the tests treat it.

## Not observable from outside the binary

- **Package layout, `README.md`, the store's clock, background deletion of expired keys, no data race.** The tests cannot see these through the binary, so they do not check them. The tests build without `-race`. Many clients write the same key at the same time, and the INCR total must be exact.

## Server

- **Default address.** If `127.0.0.1:6379` is free, the test starts the server with no flags and wants `ready 127.0.0.1:6379`. If another program holds the port, the server cannot listen, so the test wants exit 1, empty stdout and an error on stderr.
- **Listen failure.** The test holds a port and starts the server on it. It wants exit 1, empty stdout and a non-empty stderr. It does not check the error text.
- **Bad flags.** An unknown flag (`--no-such-flag`, `-x 1`) and `--addr` with no value must exit 2. The tests do not check stderr. A bad `--addr` value such as `nonsense` could be a bad flag (2) or a listen failure (1), so the tests do not use one.
- **Ready line.** Stdout must be exactly `ready 127.0.0.1:PORT\n` while the server runs, with a non-zero port. The tests do not check stdout after shutdown.
- **Signals.** After SIGINT or SIGTERM, the server must close an active and an idle connection and exit 0 within 10 s. The tests do not check commands in flight at that time.
- **Concurrency.** 50 clients run at the same time. Also, a client that sent half a command must not block another client.

## Protocol

- **Inline words.** The tests separate words with one space only. Several spaces, tabs, quotes and lines of only spaces are left open.
- **Blank inline lines.** Only empty lines (`\r\n` or `\n`) are used. The tests check that these get no reply.
- **Unterminated last line.** An inline command with no `\r\n` or `\n` before the client closes is a request that ended early. It must get no reply.
- **After an early end.** The client half-closes its side. The server may close the connection or stay silent, but it must not send more bytes.
- **Protocol errors.** The reply must start `-ERR Protocol error: ` and have a non-empty reason, and then the server must close the connection. The tests do not check the reason text. The tested cases:
  - a bad array length (`*x`) or bulk length (`$x`);
  - a bulk length of 512 MiB + 1;
  - bulk data that is not followed by `\r\n`;
  - an array element that is not a bulk string (`:1`);
  - a bad request after a good one in the same write. The good one must still get its reply.

  Redis 7 does not check the two bytes after bulk data. The spec says a missing `\r\n` is malformed, so the tests follow the spec.
- **Left open:** a header line that ends in a bare `\n`; `*0` and `*-1`; `$-1` inside a request; a bulk length of exactly 512 MiB; a top-level bulk string such as `$3\r\nfoo\r\n`; very large array counts; `+5` as a length.
- **Writes and closing.** The test sends each bad request in one write and ends it where the error shows. This lets the server read all input before it closes, so a TCP reset cannot hide the error reply.

## Errors

- **Unknown command.** The reply is exactly `-ERR unknown command 'NAME'`, with NAME as the client wrote it. Names that hold `\r` or `\n` are left open.
- **Wrong number of arguments.** The tests use Redis 7 arity for each listed command. Examples: `PING a b`, `ECHO a b`, `DBSIZE x`, `KEYS a b` and `MSET` with an odd count.
- **Left open, because the spec does not list these options:** extra arguments to `EXPIRE`/`PEXPIRE` (Redis: NX/XX/GT/LT), to `FLUSHALL` (ASYNC/SYNC), to `QUIT` and to `COMMAND` (such as `COMMAND DOCS`).
- **`SET ... EX 0` and negative times.** Redis 7 replies `ERR invalid expire time in 'set' command`. The spec's "out-of-range integer" rule could mean the integer error. The tests accept any `-ERR ` reply and check that the key does not change.
- **Left open:** EX/PX/EXPIRE values that overflow when added to the current time; repeated options (`EX 1 EX 2`, `NX NX`).

## Compatibility with Redis 7

The spec says that each listed command gives the same reply as Redis 7. The tests check these Redis behaviors, which the command list does not state outright:

- SET options are case-insensitive and can come in any order. `SET k v EX` with no number is a syntax error.
- Integers are read as Redis reads them: an optional `-` and digits with no leading zero. Each of `+1`, `01`, `-0`, ` 1`, `1 `, an empty string, `1.0`, `0x1` and `1e3` gets the integer error. This applies to INCR on a stored value, to the INCRBY and EXPIRE arguments, and to SET EX/PX. A builder who uses `strconv.ParseInt` alone fails this test, `TestIntegerSyntaxMatchesRedis`.
- `MSET` removes the expiry, as `SET` does. `APPEND` keeps it.
- `DEL k k` counts 1. `EXISTS k k` counts 2, as the spec says.
- `APPEND k ""` on a missing key creates the key and replies 0.
- `PING ""` and `ECHO ""` reply with an empty bulk string.
- `KEYS` with `*` matches every byte, including `\r`, `\n` and zero.
- **Overflow messages.** Redis 7 words its overflow errors in its own way. The spec fixes the integer error for overflow, so the tests want that error.
- **`DECRBY k -9223372036854775808`.** Redis rejects this for every value. The spec rejects it only when the result overflows. The tests use it only on a missing key (0), where both rules give an error.
- **`DBSIZE` right after an expiry.** Redis can count keys that it has not deleted yet. The spec says that an expired key is missing in every command, so the tests want `DBSIZE` and `KEYS` to skip expired keys at once.

## Timing

- TTL checks after `EX 100` accept 98 to 100 seconds. PTTL checks accept 98000 to 100000 ms.
- **Rounding.** The test sets PX 2400, 2900, 1300 and 1800. TTL must then reply 2, 3, 1 and 2. Rounding to the nearest second gives these replies. Truncation or rounding up gives a different reply in at least one case. Each TTL runs a few ms after its expiry is set, and the margin is 400 ms.
- **Expiry.** Keys get PX 100 or PEXPIRE 100, and the test waits 300 ms. The key must then be missing in every command, including the writes `SET XX`, `SET NX`, `INCR` (on an expired value that is not an integer) and `APPEND`.

## redis-cli

- The test runs `redis-cli` from PATH with `-p PORT`, as the spec does. If redis-cli is missing, the test fails. The test does not check the version.
- Stdout is not a terminal, so redis-cli prints raw replies. The test removes trailing newlines and then compares the output with `OK`, `v`, `PONG`, `1` and `2`.
