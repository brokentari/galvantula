# Evals

Each eval has a one-paragraph spec and hidden acceptance tests. The evals
measure the factory. After you change a prompt, a model or a graph, run the
evals and compare the pass rate, cost and time with the last results.

```
evals/<name>/
  spec.md          the only file the factory sees
  hidden/          black-box tests: they build ./cmd/<name> and check its output and exit codes
  reference/       a known-good answer that proves the tests right
  assumptions.md   where the spec is unclear, and how the tests treat each case
  provenance.json  which model wrote the tests, at what effort, from which spec, and at what cost
```

## Who writes the tests

A model writes them. It is not a person, and it is not a model that builds
products. `go run ./cmd/evalgen evals/<name>` gives `spec.md` to the
`eval_author` model in `guardrails.yaml`. That model writes the hidden tests,
the reference and `assumptions.md`. `guardrails.yaml` refuses an
`eval_author` that a builder role also uses, so the tests do not repeat the
builders' mistakes.

evalgen keeps the model's work only if:

- every test passes on the reference;
- at least 80% of the tests fail on a program that does nothing, so most tests check real behavior;
- `gofmt`, `go vet` and `go fix -diff` report nothing.

If a check fails, evalgen sends the failure back to the model, up to two times.

**evalgen has no sandbox.** The model runs `go test` on code it wrote, on your
machine. If `ANTHROPIC_API_KEY` is set, that code can read it, so use a key
with a low spend limit.

To change what an eval checks, edit `spec.md` and run evalgen again. Do not
edit `hidden/` or `reference/` by hand. If `assumptions.md` lists a gap that
matters, close it in the spec.

## Plant a planner mistake

`factory eval -graph graphs/factory.yaml -planted <name>` tests replanning. It
replaces the planner's first answer with `<eval>/planted/<name>.json`, which
has the same shape as the planner's answer. The file must pass the plan rules,
so its mistake is one the rules cannot see: a task that needs a package that
its deps do not build. The task fails its check, and the run must replan and
still pass the hidden tests. Each result records `planted` and `replans`. Only
the server reads the file. No agent sees it.

| planted plan | the mistake |
|---|---|
| `logq/cli-first` | `cmd/logq` has no deps, so it starts before the packages it calls |

A planted plan must fail every time, or the eval tells you nothing. Retries can
still hide the mistake: if the other packages merge before the last retry, the
task passes and no replan happens. So read `replans` before the pass rate.

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
| `dedupe` | one package | repeats anywhere in the input, `-i`, `-c`, `-d`, `-u`, several files |
| `calc` | one package | a parser: precedence, signs, `^` and `%`, number formatting, error positions |
| `logq` | four packages named in the spec | log line parser, filters, three output formats, exit codes; the spec asks for four packages so that the planner has parallel work |
| `rotom` | four packages named in the spec | a TCP server that speaks RESP2 and RESP3 and stores strings with expiry; redis-cli compatibility |
