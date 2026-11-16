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
bun run test:smoke       # Offline smoke-runner and issue-reporting regressions
bun run check           # Rust format and Wasm-target Clippy checks
make check-infra        # Credential-free Terraform initialization/validation
```

The default test suite never contacts HKUST or requires Cloudflare credentials.
Transport and smoke-runner tests use loopback HTTP and isolated temporary state.
`make test` builds the Worker once before running its runtime suite. Only the
explicit live smoke command and weekly workflow below contact HKUST.

Do not change release `strip` to `true`: removing `target_features` before
wasm-bindgen breaks externref exception-wrapper generation. The configured
`strip = "debuginfo"` preserves required transformation metadata. The generated
JavaScript loader and Wasm are both required deployment modules.

## Weekly live parser smoke

`.github/workflows/source-smoke.yml` runs every Sunday at **18:17 UTC**
(Monday **02:17 Hong Kong time**) and can be dispatched manually on the default
branch. GitHub's schedule is best-effort, not an exact execution-time guarantee.

The check fetches the current term's COMP page, discovers all departments, and
checks each page sequentially using the production Rust parser and snapshot
serializer. It does not call PATCH, write D1, or deploy anything. Fetches have
30-second deadlines and a 4 MiB body limit; the full run is bounded to 12 minutes.
Once the total timeout fires, it cancels active work and prevents later department
checks, even if timer rounding fires the callback just before the clock deadline.
Incomplete coverage is recorded as a failed run, not a partial success.
Parser failures retain rejected HTML alongside the JSON report and run summary
in a 14-day Actions artifact.

Failures open **Weekly catalogue source smoke test failed**, or add a comment
to the existing open tracking issue. Reports distinguish fetch failures, parser
rejections, and runner/build failures: an outage is not proof of HTML drift.
Successful runs do not create issues or automatically close existing ones.
The repository must have Issues and Actions enabled and permit the workflow's
`GITHUB_TOKEN` to write issues; no personal access token is needed.

Run the live check locally:

```sh
bun run smoke:live
```

Results are written to ignored `smoke-results/`; local runs do not create GitHub
issues. The offline regression command is `bun run test:smoke`.
This checks the existing validation rules against live pages, not every HTML
byte change. Harmless layout/content changes that remain valid do not alert;
silently wrong data that satisfies all current rules can still escape detection.
GitHub-runner success does not replace hosted Cloudflare egress/resource checks.

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
