# rotom

Build a Go TCP server named `rotom`, with its main package at `./cmd/rotom`, that speaks RESP2, the Redis wire protocol, and stores strings in memory. Build it as four packages: the protocol reader and writer in `./internal/resp`, the key store with expiry in `./internal/store`, the command table in `./internal/command`, and the server in `./cmd/rotom`, which wires them together and owns the connections. Add a `README.md` that says how to build and run the server and lists the commands it supports.

## Server

The `--addr` flag sets the listen address and defaults to `127.0.0.1:6379`. Port `0` picks a free port. When the server is ready to accept connections, it prints one line to standard output, `ready HOST:PORT`, with the real address. It serves any number of clients at the same time. Each client may send commands one after another or many at once (pipelining), and the server answers each command in order. On SIGINT or SIGTERM, the server stops accepting, closes all connections and exits with status 0. If it cannot listen, it prints an error to standard error and exits with status 1. A bad flag exits with status 2. All clients see the same data, and the server must have no data race.

## Protocol

A client sends a command as a RESP array of bulk strings, such as `*2\r\n$3\r\nGET\r\n$1\r\nk\r\n`. A client may also send an inline command: one line of words separated by spaces and ended by `\r\n` or `\n`. A blank inline line is ignored. Bulk strings are binary safe: they may hold `\r\n` and zero bytes. Command names are not case sensitive. Keys and values are case sensitive.

The server writes these reply types: a simple string (`+OK\r\n`), an error (`-ERR message\r\n`), an integer (`:1\r\n`), a bulk string (`$3\r\nfoo\r\n`), a null bulk string (`$-1\r\n`) and an array (`*2\r\n...`). A malformed request, such as a bad length or a missing `\r\n`, gets the error `ERR Protocol error: ` and a short reason. The server then closes that connection. A bulk length above 512 MiB is a protocol error. Other clients are not affected. A request that ends early, because the client closed the connection, is dropped without a reply.

An unknown command gets `ERR unknown command 'NAME'`, where NAME is the command name as the client wrote it. A command with the wrong number of arguments gets `ERR wrong number of arguments for 'name' command`, with the name in lower case. An unreadable or out-of-range integer argument gets `ERR value is not an integer or out of range`. A bad option gets `ERR syntax error`.

## Commands

- `PING` replies `PONG`. `PING msg` replies with `msg` as a bulk string.
- `ECHO msg` replies with `msg` as a bulk string.
- `QUIT` replies `OK` and closes the connection.
- `COMMAND` replies with an empty array. This lets `redis-cli` connect.
- `SET key value [EX seconds | PX milliseconds] [NX | XX]` stores the value and replies `OK`. `NX` sets only if the key is missing, and `XX` only if it exists. If the condition fails, it replies with a null bulk string and changes nothing. A `SET` without `EX` or `PX` removes any expiry on the key. `EX` and `PX` need a positive integer. Giving both `EX` and `PX`, or both `NX` and `XX`, is a syntax error.
- `GET key` replies with the value, or a null bulk string if the key is missing or expired.
- `MSET key value [key value ...]` replies `OK`. With an odd number of arguments, it gets the wrong-number error. `MGET key [key ...]` replies with an array that holds the value, or a null bulk string, for each key.
- `DEL key [key ...]` replies with the number of keys it removed. `EXISTS key [key ...]` replies with how many of the keys exist; a key named twice counts twice.
- `INCR key`, `DECR key`, `INCRBY key n` and `DECRBY key n` treat the value as a 64-bit signed integer, treat a missing key as 0, and reply with the new value as an integer. A value that is not an integer, or a result that would overflow, gets the integer error and changes nothing. These commands keep the expiry on the key.
- `APPEND key value` adds to the end of the value (it creates the key if missing) and replies with the new length. `STRLEN key` replies with the length in bytes, or 0 for a missing key.
- `EXPIRE key seconds` and `PEXPIRE key milliseconds` set an expiry and reply `1`, or `0` if the key is missing. A zero or negative time deletes the key at once. `TTL key` and `PTTL key` reply with the time left in seconds or milliseconds, `-1` if the key has no expiry and `-2` if it is missing. `TTL` rounds to the nearest second. `PERSIST key` removes the expiry and replies `1`, or `0` if the key is missing or has no expiry.
- `KEYS pattern` replies with an array of the matching keys, in any order. The pattern supports `*`, `?`, `[abc]`, `[a-c]`, `[^a]` and `\` to escape a character.
- `TYPE key` replies `string` or `none`, as a simple string.
- `DBSIZE` replies with the number of keys. `FLUSHALL` removes all keys and replies `OK`.

## Expiry

An expired key behaves as if it were missing in every command, as soon as its time has passed. The store also deletes expired keys in the background, so that memory does not grow without end. Time comes from a clock the store receives, so that tests of the store do not wait.

## Compatibility

`redis-cli` from Redis 7 works with the server. For each command and option above, the server gives the same reply as Redis 7 gives for the same arguments. The exceptions are the error messages and the `COMMAND` reply that this spec fixes, and any option that this spec does not list. For example, `redis-cli -p PORT SET k v` prints `OK`, and `redis-cli -p PORT GET k` then prints `v`.
