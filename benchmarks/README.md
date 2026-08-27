# chcrawl benchmarks

A fully local, deterministic benchmark suite for the crawl engine. Every
workload runs against in-process Go HTTP servers bound to `127.0.0.1` only —
no Docker, no external network, no public websites. Nothing here can send
traffic off the machine.

The oracle is derived from the same data as the target, so the two can
never drift apart, and correctness is measured alongside speed. There's no
multi-tool arm system, no Docker lab, no scale ladder — just the workloads
and the oracle.

## Quick start

```bash
go run ./cmd/chcrawl-bench                          # all 10 workloads, report to stdout
go run ./cmd/chcrawl-bench -workload w5-js-discovery # just one
go run ./cmd/chcrawl-bench -report report.md         # write the report to a file

# idiomatic go test -bench form, with custom throughput/memory metrics:
go test ./internal/benchlab/ -bench=. -benchtime=5x -run='^$'

# score chcrawl against katana/hakrawler/gospider on the same local targets:
go run ./cmd/chcrawl-bench -compare -report compare.md
```

## Architecture

```text
internal/benchlab/
  site.go        PageSpec/Site: a declarative synthetic site (pages, links,
                 forms, redirects, JS endpoints) that builds BOTH the local
                 HTTP target AND feeds the oracle — one source of truth
  oracle.go       a pure-Go BFS over the same PageSpec graph, mirroring the
                  real engine's pipeline semantics exactly (enqueue-time
                  dedup, scope-before-depth, redirect-chain resolution,
                  the JS extractor's synthetic path-param probe rule) to
                  predict the expected-correct crawl outcome
  workloads.go    the ten named workloads (w1-w10)
  runner.go       starts a Site's local servers, runs the real engine
                  against it, captures timing/memory, diffs the result
                  against the oracle
  report.go       renders the markdown report
cmd/chcrawl-bench/ the CLI
```

## Workloads

| name | exercises |
|---|---|
| w1-small-static | baseline correctness: links, one form |
| w2-deep-tree | 25-page linear chain — depth handling |
| w3-wide-site | 150-sibling fan-out — wide-crawl throughput |
| w4-redirect-heavy | a 6-hop redirect chain plus a genuine redirect loop (must fail fast, never hang) |
| w5-js-discovery | `<script src>` discovery, JS endpoint mining (incl. the synthetic `.../1` path-param probe), one endpoint that resolves to real content |
| w6-duplicate-hell | heavy fan-in to shared pages plus a cycle back to root — dedup correctness |
| w7-large-responses | 512KB and 4MB bodies — large-response throughput/memory |
| w8-parameter-discovery | query-string-heavy links (incl. an exact duplicate) and multi-field forms |
| w9-multi-host-scope | two hosts; same-origin scope must exclude the second, `same_origin=false` must include it |
| w10-chaos | redirect loop + persistent 500 + malformed content-type + a small delay + a duplicate-heavy fan-out + a 10-deep chain, all combined |

## What "correctness" means here

Every `Run()` call diffs the real crawl's final `SummaryEvent` against the
`Oracle` computed from the exact same `Site` spec: requests made, responses
ok/failed, redirects followed, duplicates rejected, forms/params/JS-files/
JS-routes discovered. A mismatch means either the engine has a bug or the
oracle's model of the engine's semantics has drifted — building this
suite caught three real bugs before anything shipped:

1. The oracle didn't follow `<script src>` as a followable link, so it
   silently never visited the JS file it was supposed to model.
2. The oracle looked up pages by full path *including* the query string;
   real HTTP routing is path-only, so `/search?q=a` and `/search?q=b` both
   correctly route to the same page — the oracle didn't know that.
3. The local HTTP dispatcher used `http.ServeMux`, whose classic `"/"`
   pattern is a catch-all subtree match: any genuinely nonexistent path
   (like a JS-mined endpoint that doesn't really exist) silently fell
   through to the root page's content instead of 404ing, corrupting every
   downstream discovery count.

A fourth, real engine bug also surfaced from just watching w10-chaos's
runtime: the default retry policy treated *every* transport error as
retryable, including "too many redirects" from a genuine redirect loop —
which is a deterministic failure that retrying with exponential backoff
can never fix. That's now excluded from the retry loop.

## A known, deliberate slow point

`w10-chaos` takes ~1.5-2s (vs. low-single-digit-ms for everything else)
because it includes a persistently-failing `500` endpoint, and the default
retry policy correctly retries 5xx responses with exponential backoff (up
to 3 attempts, ~500ms/1s/2s). A crawler can't distinguish "this 500 is
permanent" from "this 500 is transient" without trying again, so this cost
is real and expected, not a bug.

## Comparing against external crawlers

`-compare` runs chcrawl and, if installed, [katana](https://github.com/projectdiscovery/katana),
[hakrawler](https://github.com/hakluke/hakrawler), and
[gospider](https://github.com/jaeles-project/gospider) against the exact same
local synthetic target for each workload — one `httptest` server per run,
never a public site, same as everywhere else in this suite. A tool that
isn't on `PATH` is reported as "not installed", not treated as a failure.

Unlike the oracle-diff mode above, there's no single shared output schema
across four independently-designed tools to diff byte-for-byte. Instead,
each tool is scored against a **ground-truth discoverable-path set**
(`Site.DiscoverablePaths`, in `internal/benchlab/coverage.go`) — the same
kind of graph walk the oracle itself is built from, deduped by path only
since query-string dedup conventions differ across tools:

- **found / total** — how many of the site's genuinely reachable pages the
  tool actually reported.
- **extra** — URLs the tool reported that aren't in the ground truth (often
  a tool's own request-level logging, e.g. katana logging each individual
  redirect hop rather than just the originally-requested URL).
- **duration** — wall-clock time for the whole invocation, capped at 30s
  per tool so one slow/hung process can't stall the suite; a run that hits
  the cap is still scored on whatever it had printed before being killed
  (shown as "(nonzero exit)").

`w9-multi-host-scope` is excluded from `-compare` — same-origin scope is
implemented differently enough across these four tools (registered-domain
matching, subdomain flags, no built-in concept at all) that scoring it
wouldn't be a fair comparison.

A real run on this machine (chcrawl vs. katana/hakrawler/gospider, all four
against the same nine workloads):

- **chcrawl** and **hakrawler** both hit 100% recall on every workload,
  and both finish in single-digit milliseconds per workload (chcrawl
  0/109ms worst case; hakrawler under 65ms worst case).
- **gospider** matches them on most workloads but misses query-string
  links entirely on `w4`/`w8` (its HTML tokenizer doesn't handle the raw
  unescaped `&` this suite's synthetic pages generate in `href="...&..."`
  attributes — the same HTML quirk every tool is equally exposed to,
  since all four crawl the identical generated markup).
- **katana** found real gaps too (it doesn't follow POST-only form
  actions or query-string links by default without extra flags), and
  showed a consistent ~7s-per-invocation floor on this machine regardless
  of page count — except on `w2-deep-tree` (a 25-page linear chain, no
  fan-out), where it took ~29s. The other three tools resolve that same
  chain in under 20ms. Katana's default automatic update check
  (`-duc` disables it) was caught and turned off during benchmark
  development — an early run silently made an outbound network call before
  that fix, which the LOCAL-only invariant for this suite does not allow.

## Extending

Add a new workload by writing a `wN...()` function in `workloads.go` that
returns a `*Site`, and register it in `Workloads()`. The oracle is computed
automatically from whatever `PageSpec` graph you build — there's no
separate expected-count file to keep in sync by hand.
