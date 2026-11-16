# Courseinfo

HKUST course catalogue API implemented in Rust and deployed as a Cloudflare
Worker. It scrapes department pages, validates complete snapshots, persists them
in D1, and serves the existing `/v1` course/semester interface.

## Local development

Requirements: Rust/rustup, Node 24, Bun 1.4.3, and Make. `rust-toolchain.toml`
pins Rust 1.99.1 and `wasm32-unknown-unknown`. On Linux, installing worker-build
also needs `pkg-config`, OpenSSL development headers, and a C compiler.
Terraform 1.9.8 is needed for infrastructure checks, not local API execution.
Ensure `$HOME/.cargo/bin` is on `PATH` (normally by sourcing `$HOME/.cargo/env`).

```sh
make setup
make test
bun run db:migrate:local
make dev
```

Wrangler serves the API locally, normally at `http://localhost:8787`:

```sh
curl http://localhost:8787/healthz
curl http://localhost:8787/v1/semesters/current
curl http://localhost:8787/v1/courses/COMP1021
curl -X PATCH -H 'CF-Connecting-IP: 143.89.1.1' http://localhost:8787/v1/courses
```

A course miss fetches its department. Listing courses does not fetch upstream;
a cold database returns JSON `null`. PATCH awaits a full refresh and can take
about a minute for the current catalogue. It is not a background-job endpoint.
Local D1 data lives under `.wrangler/state/`; migrations are explicit.

The PATCH example simulates Cloudflare's client-IP header for local development
only. Production Cloudflare supplies this header: PATCH requires a direct IPv4
client in `143.89.0.0/16`; otherwise it returns 403. Do not expose local Wrangler
as a public server. GET misses are limited to 30 upstream acquisitions per minute
per client IP per Cloudflare location; cached reads remain unrestricted.

The weekly schedule is Sunday 19:00 UTC, Monday 03:00 Hong Kong time. Wrangler
never runs scheduled events automatically in local development. Its local
scheduled-event endpoint can exercise the handler with the configured expression:

```sh
curl 'http://localhost:8787/cdn-cgi/local/scheduled?cron=0%2019%20*%20*%20SUN'
```

See [API semantics](docs/api.md) and [deployment/rollback](docs/deployment.md).

## Modules and verification

- `crates/courseinfo-core`: pure identifiers, Hong Kong term selection, HTML
  parsing, and bounded snapshot encoding. No network, platform binding, or ambient clock.
- `crates/courseinfo-worker`: HTTP/scheduled handling, bounded fetching, D1
  publication, and refresh orchestration.
- `db/migrations`: dedicated catalogue schema. `db/queries`: publication queries
  compiled into Rust and also exercised against real local D1.
- `tests/fixtures`: minimized real captures, independently extracted expected
  course values, and provenance. The original footer hierarchy is preserved.
- `tests/worker`: actual-artifact HTTP/scheduled/D1 tests and CLI/loopback tests
  that observe cancellation of real upstream connections.

```sh
bun run test:native     # Rust core regression suite
bun run build           # Creates build/index.js and build/index_bg.wasm
bun run test:worker     # Consumes those exact files; no rebuild/rebundle
bun run check           # Rust format and Wasm-target Clippy checks
make check-infra        # Credential-free Terraform initialization/validation
```

No permanent test contacts HKUST or requires Cloudflare credentials. The
transport tests start local HTTP/Worker processes with isolated D1 state and
development registries, then remove both. `make test` builds once before running
the runtime suite.

Do not change release `strip` to `true`: removing `target_features` before
wasm-bindgen breaks externref exception-wrapper generation. The configured
`strip = "debuginfo"` preserves required transformation metadata. The generated
JavaScript loader and Wasm are both required deployment modules.

## Observed implementation verification

Recorded on 2026-10-23. Checks and artifact sizes include the independent review;
live catalogue measurements are from the initial implementation smoke.

| Check | Observed result |
| --- | --- |
| Native core tests | 17 passed |
| Actual-artifact Worker/D1/transport tests | 30 passed |
| Rust formatting and Wasm-target Clippy | Passed |
| Terraform 1.9.8 / provider 5.23.0 validation | Passed, backend disabled |
| Live `2610` full refresh | HTTP 200; 1,518 courses across 100 departments |
| Local full-refresh wall time | 60.78 seconds |
| Largest persisted department JSON | 17,604 bytes |
| Total persisted department JSON | 269,462 bytes |
| Compiled JS + Wasm, uncompressed | 1,253,029 bytes |

A separate process restart retained all 1,518 courses. Cached reads succeeded
with the upstream deliberately unreachable; a cold miss returned 502.
These are local observations, not hosted Cloudflare CPU/memory/egress guarantees.

The independent review fixed inline-text corruption, same-millisecond catalogue
resurrection, pre-limit parser/JSON allocation amplification, and missing HTTP
`Allow` headers. New regressions failed before the fixes; the complete check
workflow and a real-HTTP inline-markup/amplification smoke passed afterward.

## Deployment status

The Go implementation, container build, and Go-only tests have been removed from
this source tree. Existing deployed Go releases and production traffic were not
changed. Terraform uploads and activates immutable tested artifacts, but public
exposure and Cron are disabled by default.

No remote deployment, production traffic switch, or hosted rollback rehearsal
has been performed. Activation requires an authorized account/token, dedicated
remote state, target hostname/exposure approval, and hosted workload checks.
PATCH is restricted to `143.89.0.0/16`, and GET misses use Cloudflare rate limiting.
These controls do not replace hosted ingress verification, workload measurements,
or rollback rehearsal. This standalone API does not implement concierge's
separate hourly schedule-snapshot feature.
