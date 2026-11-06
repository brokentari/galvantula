# Evals

Each eval is a one-paragraph spec and a set of hidden acceptance tests. The
factory gets only `spec.md`. It never sees `hidden/` or `reference/`.

```
evals/<name>/
  spec.md      the input to the factory
  hidden/      black-box tests: they build ./cmd/<name> and check its output and exit codes
  reference/   a known-good answer that exists only to check the tests
```

Run the hidden tests against a product the factory built:

```
EVAL_PRODUCT=/path/to/product go test ./evals/wordfreq/hidden
```

Without `EVAL_PRODUCT`, the tests run against `reference/`. So
`go test ./evals/...` checks the tests themselves. Each test must also fail
against a program that does nothing. The only exceptions are tests that
expect empty output.

| eval | size | what it tests |
|---|---|---|
| `wordfreq` | one package | flags, stdin or files, Unicode words, sorting with ties, exit codes |
| `todo` | one package, saved state | subcommands, JSON file, ids that are never reused, atomic writes |
| `csvq` | several packages | filter, stable sort, limit, select, CSV and JSON output, error lines |

These tests are a first draft. Read them and change them until they say
exactly what you mean, because the eval scores are only as good as the tests.
