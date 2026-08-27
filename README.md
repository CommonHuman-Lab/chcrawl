# chcrawl

A high-performance, correctness-first web crawler and recon engine, native
Go. Part of the CommonHuman-Lab toolkit.

## Install / build

```bash
go build -o chcrawl ./cmd/chcrawl
```

## Usage

```bash
./chcrawl https://target.example
```

Output is streamed JSONL to stdout: a `page` record per fetched page, an
`error` record per failure, and a final `summary` record with aggregate
stats (unique/in-scope URLs, endpoints, params, forms, JS files/routes,
requests, redirects, duplicates, throughput, peak memory). `page`/`error`
records and the summary also carry retry telemetry (`retry_attempts`,
`retry_delay_ms`/`retry_backoff_ms`, and the summary's `active_wall_ms` —
wall-clock duration minus cumulative retry backoff, NOT CPU time) so a slow
crawl against a flaky target can be told apart from a slow crawl engine.

```bash
./chcrawl -h                                  # full flag reference
./chcrawl -max-depth 5 -max-pages 500 https://target.example
./chcrawl -respect-robots-txt -insecure https://target.example
./chcrawl -header "Authorization: Bearer tok" https://target.example
./chcrawl -discover-openapi -recover-source-maps https://target.example
./chcrawl -output result.jsonl https://target.example
```

Key flags: `-concurrency`, `-per-host-concurrency`, `-max-pages`,
`-max-depth`, `-max-duration`, `-same-origin`, `-exclude`, `-proxy`,
`-cookies`, `-header` (repeatable), `-delay`, `-insecure`,
`-respect-robots-txt`, `-discover-openapi`, `-recover-source-maps`,
`-legacy-mode` (switches to simpler normalization/budget/retry behavior).

## Discovery capabilities

- HTML link extraction: `<a>`, `<link>`, `<script src>`, `<img>`,
  `<iframe>`, `<button formaction>`, `data-href`/`data-url`/`data-link`/
  `data-action`, meta-refresh, `<base href>` — plus a `<code>`-block
  API-path heuristic.
- Form extraction with required-field defaulting and `<select>`
  selected/first-option fallback.
- JS endpoint mining (method+template-literal calls, static quoted paths,
  indirect variable-concatenation, webpack chunk discovery).
- WebSocket URL discovery.
- Redirect chain tracking with `<base href>`-aware relative link resolution.
- Pluggable scope policies (exact-origin, subdomain, allow/deny list,
  regex), retry/backoff policies, and an opt-in robots.txt gate.
- Auth helpers: form login (with CSRF/hidden-field replay), OAuth2
  client-credentials bearer login, HTTP Basic.
- OpenAPI/Swagger discovery (`-discover-openapi`): probes canonical spec
  locations (and follows the embedded spec URL from a Swagger UI/Redoc
  page), parses v2 and v3 documents (JSON or YAML, with `$ref` resolution),
  and reports the full endpoint/parameter list as a standalone `openapi`
  JSONL record — a one-shot pass against the target origin, not wired into
  the per-page crawl loop.
- `.js.map` source-map recovery (`-recover-source-maps`): for every JS file
  crawled, recovers original pre-minification source (noise-filtered:
  `node_modules`, `webpack/runtime`, `.spec`/`.test` files, `/vendor/`
  excluded) and reports what was recovered as a `source_map` discovery.