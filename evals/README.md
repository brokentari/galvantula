# Evals

Each eval is a one-paragraph spec plus hidden acceptance tests. The evals
measure the factory: after you change a prompt, a model or the graph, run the
evals and compare the pass rate, cost and time with the last run.

```
evals/<name>/
  spec.md          the only file the factory sees
  hidden/          black-box tests: they build ./cmd/<name> and check its output and exit codes
  reference/       a known-good answer that proves the tests right
  assumptions.md   where the spec is unclear, and how the tests treat each case
  provenance.json  which model wrote the tests, at what effort, from which spec, and at what cost
```

## Who writes the tests

A model writes them, not a person, and not a model that builds products.
`go run ./cmd/evalgen evals/<name>` gives `spec.md` to the `eval_author`
model in `guardrails.yaml`. That model writes the hidden tests, the reference
and `assumptions.md`. `guardrails.yaml` refuses an `eval_author` that any
builder role also uses, so the tests do not repeat the builders' mistakes.

evalgen checks the model's work itself. It keeps the result only if:

- every test passes on the reference;
- at least 80% of the tests fail on a program that does nothing, so most tests check real behavior;
- `gofmt`, `go vet` and `go fix -diff` report nothing.

If a check fails, evalgen sends the failure back to the model, up to two times.

The model runs `go test` on code it wrote, on your machine, with no sandbox
until phase 1 adds one. If `ANTHROPIC_API_KEY` is set, that code can read it,
so use a key with a low spend limit.

To change what an eval checks, edit `spec.md` and run evalgen again. Do not
edit `hidden/` or `reference/` by hand. If `assumptions.md` lists a gap that
matters, close it in the spec.

## Run the hidden tests

Against a product the factory built:

```
EVAL_PRODUCT=/path/to/product go test ./evals/wordfreq/hidden
```

Without `EVAL_PRODUCT`, the tests run against `reference/`, so
`go test ./evals/...` checks the tests themselves.

| eval | size | what it tests |
|---|---|---|
| `wordfreq` | one package | flags, stdin or files, Unicode words, sorting with ties, exit codes |
| `todo` | one package, saved state | subcommands, JSON file, ids that are never reused, atomic writes |
| `csvq` | several packages | filter, stable sort, limit, select, CSV and JSON output, error lines |
