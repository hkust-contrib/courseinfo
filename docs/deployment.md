# Deployment and rollback

Terraform is the sole production deployment owner. Wrangler handles local
execution/testing and D1 migration execution; do not run `wrangler deploy` to
activate production code alongside Terraform.

## Toolchain and artifacts

- Rust 1.99.1 and `wasm32-unknown-unknown`, pinned in `rust-toolchain.toml`.
- worker/worker-build 0.8.7; Cargo dependencies locked.
- Node 24, Bun 1.4.3, Wrangler 4.137.0, Vitest 4.1.12; JavaScript dependencies locked.
- Terraform 1.9.8 and Cloudflare provider 5.23.0.
- Compatibility date **2026-09-28**, matching the maximum supported by the pinned
  local workerd. Terraform and Wrangler must match; a newer calendar date is not
  automatically a valid tested compatibility date.

`bun run build` runs worker-build from `crates/courseinfo-worker/` with
`--release --locked --out-dir ../../build`. The deployable modules are:

| Module name | Local file | Content type |
| --- | --- | --- |
| `index.js` | `build/index.js` | `application/javascript+module` |
| `index_bg.wasm` | `build/index_bg.wasm` | `application/wasm` |

Wrangler uses explicit no-bundle rules and no custom build command. Runtime tests
consume these same files. Terraform uses `content_file`, never `file()` for
Wasm. Provider 5.23.0 computes content hashes and creates a replacement version
when bytes change at the same path. Build before planning; preserve the same
bytes and paths through apply.

The scheduled export is an explicit Rust/wasm-bindgen promise bridge because
worker 0.8.7's scheduled macro discards handler errors. It returns and registers
the same promise, so a failed refresh becomes a failed Cron outcome. Both
outcomes are exercised in the production-artifact suite.

## Resources and configuration

The dedicated state owns D1, Worker settings, additive migrations, uploaded
versions, active deployment, a switchable Cron trigger, and an optional custom
domain. It owns no concierge resources, KV, R2, browser binding, or Durable
Objects. D1 read replication is disabled and destruction is protected.

| Terraform input | Default / requirement |
| --- | --- |
| `cloudflare_account_id` | Required real account ID. |
| `worker_name` | `courseinfo`. |
| `database_name` | `courseinfo-catalogue`. |
| `base_url` | `https://w5.ab.ust.hk/wcq/cgi-bin`; trusted deployment configuration only. |
| `fetch_timeout_ms` | `30000`; integer 1–30000. |
| `refresh_timeout_ms` | `720000`; integer 1–720000. |
| `workers_dev_enabled` | `false`; explicit public exposure approval. |
| `cron_enabled` | `false`; enable after warmup/acceptance. |
| `custom_domain` | `null`; explicit authorized public hostname. |
| `deployment_version_id` | `null` selects the tested upload; otherwise a retained compatible version UUID. |
| `course_miss_rate_limit_namespace_id` | `14389001`; reserve an account-unique ID per environment. |

Bindings are `DB`, `BASE_URL`, `WORKER_NAME`, `FETCH_TIMEOUT_MS`,
`REFRESH_TIMEOUT_MS`, and `COURSE_MISS_LIMITER`. The limiter permits 30 upstream
GET-miss acquisitions per minute per client IP in each Cloudflare location.
It is approximate, not a strict global quota. Local defaults live in
`wrangler.jsonc`; `.dev.vars` can supply local variable overrides. HTTP CPU
ceiling is 300,000 ms. Weekly Cron has its own platform CPU/wall-time class,
so manual and scheduled refreshes require separate hosted verification.

The schedule `0 19 * * SUN` means Monday 03:00 Hong Kong time. It replaces an
uptime-relative ticker. Unknown schedules and delayed events from a different
Hong Kong term are rejected. Production has no automatic startup warmup.

**Access policy:** PATCH is restricted in the Worker to direct IPv4 clients in
`143.89.0.0/16`, on both custom domains and workers.dev. It uses Cloudflare's
`CF-Connecting-IP`; missing, malformed, IPv6, and out-of-range addresses return
403 before any database or upstream operation. Worker subrequests (`CF-Worker`)
are also denied because same-zone Workers can override the client IP.
Do not put an untrusted proxy or service binding in front of this check.
See [Cloudflare's header trust semantics](https://developers.cloudflare.com/fundamentals/reference/http-headers/#cf-connecting-ip-in-worker-subrequests).
Local Wrangler accepts simulated headers and is not a secure public ingress.

GET remains public. Cache hits bypass the miss limiter; all course URLs share a
client's acquisition allowance. Missing/invalid client IPs share one bucket,
and Worker subrequests share another rather than trusting their supplied IP.
See [Cloudflare rate limiting](https://developers.cloudflare.com/workers/runtime-apis/bindings/rate-limit/).
This does not prevent distributed abuse or concurrent refreshes from permitted
PATCH clients. Public exposure still requires explicit approval.

## Local infrastructure checks

```sh
make check-infra
```

This initializes providers with the backend disabled and validates configuration;
it does not create resources or prove Cloudflare API permissions. Remote work
requires a separately authorized HTTP state backend and Cloudflare token:

- `CLOUDFLARE_API_TOKEN`, `TF_VAR_cloudflare_account_id`.
- `TF_HTTP_ADDRESS`, `TF_HTTP_LOCK_ADDRESS`, `TF_HTTP_UNLOCK_ADDRESS`.
- `TF_HTTP_USERNAME`, `TF_HTTP_PASSWORD`.
- Lock/unlock methods matching the backend; CI uses POST/DELETE.

Do not read/copy concierge state or assume its token scope authorizes courseinfo.
Keep credentials in environment secrets, never committed tfvars or artifacts.

Terraform runs pinned Wrangler migrations with a generated temporary config
before creating a version that reads the schema. Migration triggers include
ordered SQL contents, database identity, and the locked migration runner. Keep
migrations additive while retained application versions may be rolled back.

## CI release workflow

`.github/workflows/build.yml` runs native tests, builds once with source commit
metadata, records checksums, runs actual-artifact tests, verifies the checksums
again, and uploads the tested modules. Separate credential-free Terraform checks
must pass. Pull requests cannot deploy.

Production/staging dispatch is opt-in:

1. Set repository variable `COURSEINFO_DEPLOYMENT_ENABLED=true`.
2. Create a protected GitHub environment with appropriate reviewers and ownership.
3. Configure its secrets:
   - `COURSEINFO_CLOUDFLARE_API_TOKEN`.
   - `COURSEINFO_TF_HTTP_ADDRESS`, `COURSEINFO_TF_HTTP_LOCK_ADDRESS`,
     `COURSEINFO_TF_HTTP_UNLOCK_ADDRESS`, `COURSEINFO_TF_HTTP_USERNAME`,
     `COURSEINFO_TF_HTTP_PASSWORD`.
4. Set environment variable `COURSEINFO_CLOUDFLARE_ACCOUNT_ID`. Optional overrides:
   `COURSEINFO_WORKER_NAME`, `COURSEINFO_DATABASE_NAME`, `COURSEINFO_BASE_URL`,
   `COURSEINFO_FETCH_TIMEOUT_MS`, `COURSEINFO_REFRESH_TIMEOUT_MS`,
   `COURSEINFO_WORKERS_DEV_ENABLED`, `COURSEINFO_CRON_ENABLED`,
   `COURSEINFO_CUSTOM_DOMAIN`, `COURSEINFO_MISS_RATE_LIMIT_NAMESPACE_ID`.
5. Dispatch on main with the protected environment and `plan` or `apply` intent.
   Optional `deployment_version_id` selects a retained version for rollback.

The deployment job downloads the tested artifact by artifact ID, checks it
against the same source checkout, plans with state locking, checks the bytes
again, and applies the saved plan. It never rebuilds. Deployments/migrations are
serialized per environment. A successful local suite is not authorization to
turn on the deployment gate or public exposure.

## Weekly live-source monitoring

The separate `source-smoke.yml` workflow runs on the default branch at
`17 18 * * SUN`, or by manual dispatch. It compiles a native executable against
the same Rust parsing and serialization code used by the Worker, then reads the
current-term catalogue directly from HKUST. It needs no Cloudflare credentials,
does not mutate D1, and does not use the network-restricted PATCH endpoint.

The job has `contents: read` and `issues: write` permissions. On failure it
creates an issue marked `<!-- courseinfo-live-parser-smoke -->`, or comments on
the existing open marked issue. Workflow concurrency serializes scheduled and
manual runs to avoid duplicate issue creation. A failed check stays red even
after successful issue reporting. Recovery leaves the issue open for review.

Triage the report's failure stage before changing selectors. Fetch failures
include HTTP errors, deadlines, and oversized responses; parser rejections may
indicate source markup or content changes; build/runner failures do not establish
source drift. Download the run artifact for the JSON report and bounded rejected
HTML. Reproduce against the production parser, minimize a new fixture, and add a
regression before changing accepted structure. No fixed live course counts or
HTML hashes are used, so normal catalogue updates are not treated as breakage.

Enable GitHub Issues and Actions, retain `GITHUB_TOKEN` issue-writing permission,
and ensure scheduled workflows remain active. A GitHub-runner check cannot prove
Cloudflare egress, memory/CPU capacity, actual Worker Cron delivery, or rollback;
the hosted acceptance gates below still apply. See the README for local commands.

## Staging and traffic cutover

Local implementation verification is recorded in the root README. Before moving
production traffic:

1. Verify account plan, D1/Worker/migration permissions, separate state locking,
   domain ownership, and PATCH access from `143.89.0.0/16`. Workers Paid is the
   baseline: the observed catalogue has 100 departments, beyond Free's 50
   external-subrequest limit.
2. Deploy the tested modules to authorized staging; apply the actual D1 migration.
3. Run a real HKUST refresh from Cloudflare and a real scheduled invocation.
   Local workstation egress does not prove that HKUST accepts hosted requests.
   Keep the exact supported expression `0 19 * * SUN` and observe its next
   scheduled execution in staging. Alternate temporary expressions are rejected
   by the handler; they cannot validate refresh. The local scheduled endpoint
   exercises code but is not evidence of hosted Cron delivery or resource limits.
4. Measure both paths' CPU, memory, total duration, D1 operations, response sizes,
   and largest department snapshot. Do not substitute a single COMP page for
   the complete workload. The 4 MiB HTML, 16 MiB parsed-output accounting, and
   1.8 MB serialized snapshot bounds are enforced, not truncation targets. The
   accounting budget is not a heap ceiling; measure actual hosted memory and
   revise/re-review if real data cannot fit.
5. Warm the new database through PATCH from an allowed IPv4 network, check the
   full result and representative courses, inventory real clients/metrics
   scrapers, and only then switch the authorized hostname. The repository no
   longer builds a Go container; keep the previously deployed Go release
   available operationally during rollback.
6. Enable the intended weekly trigger. Trigger updates/deletion can take up to
   15 minutes to propagate. Rehearse traffic and version rollback before signoff.

Before signoff, verify PATCH succeeds from an allowed client and returns 403
from outside the range, including an attempt to supply a forged
`CF-Connecting-IP`. Check the GET-miss limiter returns 429 without blocking
cached reads. Local tests simulate edge headers; only hosted checks verify
Cloudflare's ingress header handling. Keep a unique limiter namespace per
environment to avoid sharing allowances with another deployment.

Assign an operator for semester rollover at 00:00 Hong Kong time on January 1,
February 1, June 1, and September 1. Run PATCH from the allowed network after the
boundary and verify the current semester, complete catalogue, and representative
courses. Reads never fall back to the old term: without this warmup, the list can
remain empty or partial until the next successful weekly refresh.
Monitor scheduled failures and the age of `semester_publications.cutoff_ms`,
as well as current-term catalogue completeness; `/healthz` is liveness only.
Do not mark deployment accepted until these hosted checks and rollback have
actually been observed.

A failed source page retains its old snapshot and makes the full run fail rather
than silently dropping courses. D1 cannot cancel an already-issued atomic write;
operation timeout is not a promise that every write was rolled back.

## Rollback

Record `uploaded_version_id` and `deployed_version_id` outputs for each release.
To roll back Rust code, select a retained version that still exists and supports
the additive schema using `deployment_version_id`, then plan/apply through the
same protected workflow. Verify the active version and cached API behavior.
Do not use a console/Wrangler activation that leaves Terraform unaware.

Suspend Cron if reverting to code without the same handler contract, allowing
for propagation while keeping a compatible handler active. Reverting traffic
to the old Go deployment is a separate operation owned by the actual hostname's
operator. Never destroy D1 or run destructive down-migrations as an application
rollback. Old-term snapshots remain stored; retention/purging is a separate
operational decision, not an implicit startup action.

Hosted deployment, real Cron execution, production hostname migration, and
hosted rollback remain unperformed until the external prerequisites above are
provided and authorized.
