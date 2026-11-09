# rotom: assumptions in the hidden tests

The tests run the built `rotom` binary only. They start it with `--addr 127.0.0.1:0`, read the ready line, connect over TCP and compare raw RESP bytes. Each item below names a place where the spec is unclear, what a reasonable builder might do, and how the tests treat it.

## Server and flags

- **Default address.** The spec gives `127.0.0.1:6379` as the default. Port 6379 can be in use on the test machine, so the tests do not check the default.
- **Flag form.** The tests pass `--addr` and the address as two arguments. The Go `flag` package accepts this form, and so does `--addr=VALUE`.
- **Ready line.** The tests require the first stdout line to be exactly `ready 127.0.0.1:PORT`, with a port that is not 0, and they connect to that address. The tests do not check stdout after the ready line.
- **Listen failure.** The tests take a port with their own listener and start the server on it. They require exit status 1, empty stdout and a stderr that is not empty. The spec does not give the message, so the tests do not check the words.
- **Bad flag.** The tests use `--bogus` and a bare `--addr` with no value. They require exit status 2 and empty stdout. They do not check stderr.
- **Shutdown.** For SIGINT and SIGTERM, the tests connect one client, send `PING`, and then send the signal. They require exit status 0 within 10 seconds and the client connection closed. A server that waits for idle clients to leave fails. The tests do not check "stops accepting" separately: after the process exits, another test's server can get the same port, and the check would be unreliable.
- **Data races.** A black-box test cannot see a data race, and evalkit builds without `-race`. The tests run 10 clients that each send `INCR` 300 times on one key and then check the total, which catches lost updates.
- **Package layout, README and clock.** The four packages, the README and the clock that the store receives are not visible through the binary. The tests do not check them.
- **Background deletion of expired keys.** Memory use cannot be measured in a portable way, so the tests do not check it. The tests only check that expired keys act as missing.

## Protocol

- **Protocol error text.** The tests require a reply that starts with `-ERR Protocol error: ` and has a reason after it. Any reason text passes. The server must then close the connection without sending anything more.
- **Malformed input that the tests use.** The tests use only cases that the spec names: a bad array length (`*x`), a bad bulk length (`$x`), a bulk length of 536870913 (one byte above 512 MiB), bulk data that `\r\n` does not follow, and a malformed request after a good pipelined `PING`. In the last case, the `PONG` must come before the error.
- **Malformed input that the tests do not use.** These cases are open: an array header that ends in a bare `\n`, negative lengths such as `*-1` or `$-1`, `*0`, array items that are not bulk strings, and inline lines that start with `$`.
- **Exactly 512 MiB.** The spec allows a bulk string of exactly 512 MiB. Sending one is too slow for a test, so the tests do not check this limit from below.
- **Request that ends early.** The tests send part of a request and then close their write side (TCP half-close). They require no reply bytes and a closed connection. A later `GET` on another connection must show that the partial `SET` had no effect. An inline line with no `\n` at the end also counts as a request that ends early. A builder who uses `bufio.Scanner` runs the last unended line and fails this test. The tests treat that as a spec violation, because the spec says that an inline command ends with `\r\n` or `\n`.
- **Inline word splitting.** The tests use one space between words. Runs of spaces, tabs and quotes are open. A line of only spaces is open too: the tests use only empty lines as blank lines.
- **Errors that are not protocol errors.** The tests expect the connection to stay open after an unknown-command, wrong-number, integer or syntax error, because the spec closes the connection only for a malformed request.
- **Exact error text.** The spec gives the text of the unknown-command, wrong-number, integer and syntax errors, so the tests compare the whole reply. Real Redis appends `, with args beginning with: ...` to the unknown-command error. That suffix fails the tests.

## Commands

- **Arity of PING and DBSIZE.** The spec lists only `PING`, `PING msg` and a bare `DBSIZE`. The tests expect the wrong-number error for `PING a b` and `DBSIZE a`.
- **Arguments that are open.** The tests do not send arguments to `QUIT`, `COMMAND` (for example `COMMAND DOCS`) or `FLUSHALL` (for example `ASYNC`), and they do not send extra arguments to `EXPIRE` or `PEXPIRE`.
- **Errors change nothing.** The tests expect a command that gets any error reply to leave the data as it was. For example, `MSET a 1 b` creates no key, `INCRBY none x` creates no key, and a `SET` with a syntax error keeps the old value and its expiry.
- **SET option order and case.** The spec grammar puts `EX`/`PX` before `NX`/`XX`. The tests use only that order and only upper-case options. Real Redis accepts any order and any case, but these are open.
- **SET option repeated.** `EX 10 EX 20` is open. The tests check only `EX` with `PX`, `NX` with `XX`, an unknown option, and `EX` with no number. They treat all four as syntax errors, because `EX` with no number is a bad option.
- **SET with a zero or negative EX/PX.** The spec says that `EX` and `PX` need a positive integer, but it does not name the error. The reply can be the integer error, or the Redis text `invalid expire time in 'set' command`. The tests accept any error reply and check that the key is unchanged. A value that is not a number (`EX ten`, `PX 1.5`) must get the exact integer error.
- **EXPIRE with zero or a negative time on a key that exists.** The tests expect the reply `1` and the key gone. The key was not missing, so `1` follows from the spec.
- **Very large expiry times.** Overflow in `EX`, `PX`, `EXPIRE` and `PEXPIRE` is open.
- **Expiry after other writes.** The spec does not say if `MSET` clears an expiry or if `APPEND` keeps one. The tests do not check either case. The tests check that `SET` without `EX`/`PX` clears the expiry, that a failed `SET NX` keeps it, and that `INCR`, `DECR`, `INCRBY` and `DECRBY` keep it.
- **DEL with the same key twice.** The spec says that `EXISTS` counts a repeated key twice and that `DEL` counts the keys it removed. The tests expect `DEL c c` to reply `1`.
- **Integer parsing.** The tests use only clear cases. These values must get the integer error: `abc`, `1.5`, `12abc`, the empty string and `9223372036854775808`. Forms such as `+5`, `007` and ` 5` are open, because Go's `strconv.ParseInt` and Redis do not agree on them.
- **DECRBY with the smallest int64.** `DECRBY zero -9223372036854775808` on a missing key would give 2^63, which overflows. The tests expect the integer error and no key created. A builder who turns `DECRBY n` into `INCRBY -n` fails this test, because `-(-2^63)` wraps to itself in Go.
- **TTL rounding.** The tests set `PX 2800` and expect `TTL` to reply 3, set `PX 2200` and expect 2, and then `PEXPIRE 1800` and expect 2. Truncation gives 2 for the first case, and ceiling gives 3 for the second, so both fail. Each case allows 300 ms of delay. A half-second remainder is open: the delay makes that case unreliable to test.
- **Other TTL and PTTL values.** Elsewhere, the tests accept a range, for example 99 to 100 for `EX 100`, so that a slow machine does not fail them.
- **Expired keys in DBSIZE and KEYS.** The spec says that an expired key acts as missing "in every command, as soon as its time has passed". The tests expect `DBSIZE` and `KEYS *` to leave out expired keys at once, before any background sweep. Real Redis counts such keys in `DBSIZE` until it removes them. That behavior fails the test.
- **Expired keys in write commands.** After a key expires, `SET NX` must succeed, `SET XX` must fail, and `INCR` and `APPEND` must start from an empty key. The new keys have no expiry (`TTL` replies -1).
- **KEYS patterns.** The tests check `*`, `?`, `[ae]`, `[^e]`, `[a-e]`, `[w-z]`, `\*` and `\?` on ASCII keys, and compare the keys in any order. These cases are open: an unclosed `[`, a reversed range, an escape inside a class, and multi-byte characters with `?`.
- **Command names with control bytes.** An unknown command name that holds `\r\n` would break the error line. The tests do not send one.
- **Empty strings.** The tests expect `ECHO ""` to reply with an empty bulk string, `APPEND k ""` to reply with the current length, and an empty value to fail `INCR`.
