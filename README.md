# chcrawl

A high-performance, correctness-first web crawler and recon engine, native
Go. Part of the CommonHuman-Lab toolkit.

## Installation

Requires Go 1.27+.

```bash
go install github.com/commonhuman-lab/chcrawl/cmd/chcrawl@latest
```

Or build from source:

```bash
git clone https://github.com/commonhuman-lab/chcrawl.git
cd chcrawl
go build -o chcrawl ./cmd/chcrawl
```

## Quick start

```bash
./chcrawl https://target.example

./chcrawl -h                                  # full flag reference
./chcrawl -max-depth 5 -max-pages 0 https://target.example
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

## Legal & Ethical Use

Only run chcrawl against applications you own or have explicit written authorization to test. Authorized use includes penetration testing engagements, bug bounty programs within defined scope, and CTF competitions.

The authors accept no liability for unauthorized or illegal use.

---

## License

Licensed under the [AGPLv3](LICENSE). You are free to use, modify, and distribute this software. If you run it as a service or distribute it, the source must remain open.

For commercial licensing, contact the author.
