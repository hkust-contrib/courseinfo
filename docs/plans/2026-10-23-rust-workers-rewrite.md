# Rust/Wasm Workers rewrite — implementation plan

Status: implemented and verified locally; hosted activation pending. Review: implementation findings resolved.
Updated: 2026-10-23.

## Implementation record

The Rust core, Worker API/scheduled export, D1 publication, Terraform, CI, and
operating documentation are implemented. Native tests (17), actual-artifact
Worker/D1/direct-transport tests (30), Wasm-target Clippy, and credential-free
Terraform validation passed. A real-source local full refresh returned 1,518
courses across 100 departments; a process restart retained the catalogue with
the upstream unavailable. See [README](../../README.md) for measurements and
[deployment documentation](../deployment.md) for activation prerequisites.

Runtime proof refined the plan: compatibility date is 2026-09-28 for the pinned
workerd; release stripping preserves target-feature metadata; outbound requests
use supported manual redirects; completion validation preserves HKUST's actual
footer nesting; deadlines include D1 awaits and cancel origin connections.
Finite `FETCH_TIMEOUT_MS`/`REFRESH_TIMEOUT_MS` bindings expose the stated budgets
without allowing them to exceed 30 seconds / 12 minutes. Direct CLI/loopback
tests cover transport cancellation because the harness's Node outbound proxy
does not forward response-stream cancellation.

An independent follow-up review reproduced and fixed four defects: descendant
text-node boundaries introduced spurious spaces; equal-clock acquisitions could
survive pruning or resurrect departments; compact HTML could amplify section
copies before the serialized-size check; and 405 responses omitted `Allow`.
The corrected implementation collapses only real whitespace, fences acquisitions
at or below completed cutoffs, charges a shared 16 MiB parsed-output budget before
copying, and streams JSON into a writer capped at 1,800,000 bytes. The accounting
budget is not an exact heap ceiling. Native, real-D1, and HTTP regression cases
cover the failures; direct HTTP smoke verified unchanged cached data after a
resource rejection. Fresh parser/persistence/security reviewers confirmed the
fixes by inspection; deployment/CI review found no additional confirmed issue.

The source-tree cutover removed obsolete Go code/container files after local
verification. References to Go paths below describe the historical baseline.
Existing deployed Go releases and traffic were not touched. Hosted Cloudflare
egress/CPU/memory checks, real Cron observation, remote Terraform plan/apply,
hostname cutover, and hosted rollback remain pending external authorization,
credentials, and operational decisions; local proof does not satisfy those
activation gates.

### Approved review follow-up (2026-11-16)

PATCH now permits only direct IPv4 clients in `143.89.0.0/16`, using Cloudflare's
client-IP header and rejecting Worker subrequests. GET misses use a Cloudflare
rate limiter before upstream acquisition; cached reads and freshness semantics
are unchanged. Section parsing rejects unclassified direct table rows instead
of publishing missing sections/instructors. Staging must exercise the exact
supported weekly Cron expression, not a temporary alternate schedule.
The deployment runbook now covers network-policy verification, semester rollover
warmup, and freshness monitoring. Hosted acceptance and rollback remain external
requirements, not completed by these implementation changes.

## 1. Deliverable and scope

Replace this repository's standalone Go course catalogue API with Rust compiled
for Cloudflare Workers. Retain the useful existing HTTP surface, add meaningful
parser/runtime regression coverage, and let Terraform own production deployment
using the pattern in `../concierge/deployment/` (repository-root-relative).
This document is a plan, not authorization to create paid resources, switch
production traffic, or change callers without agreement.

The completed deliverable includes all seven routes in `routes.go`, department
scraping, persistent caching, scheduled and manual refresh, runtime metadata,
local development, CI, infrastructure, documentation, and a verified cutover.
The Rust application must not depend on the old Go process after cutover.

Non-goals: meeting-time/venue extraction, a UI, browser automation, enrollment
integration, a native Rust HTTP server, historical catalogue browsing, queues,
Durable Objects, a second deployment owner, or performance claims without data.

Related work: `../concierge/docs/plans/2026-10-22-courseinfo-migration.md` currently
proposes hourly Browser Run schedule snapshots inside concierge's existing D1
course rows. That is a separate, narrower product contract, not a consumer of
this proposed API. Do not import its Browser Run/hourly requirements, modify its
plan, share its database, or treat it as evidence that a standalone Rust API is
already approved for production. Confirm that both products are still wanted
before production activation; implementation of this plan does not implement
the concierge schedule feature.

## 2. Evidence and decision ledger

### Observed baseline

- Production Go totals approximately 650 lines across seven files. Echo supplies
  HTTP routing; Colly supplies fetching/HTML selectors; there is no database.
- `main.go` uses a process-local cache, an uptime-relative weekly ticker, and a
  second listener for Prometheus. The Docker entrypoint enables `-precache`.
- `handlers.go` lists currently cached courses, fetches a whole department on a
  course miss, and refreshes synchronously on PATCH.
- During the preceding feasibility investigation, `go test -v -race ./...`
  passed with `TestParseCourse` skipped. A running Go API parsed 76 courses from
  captured live COMP HTML; the source page was 585,543 bytes and listed 100
  distinct departments. These are observations, not full-catalogue benchmarks.
- The current CI builds/publishes a container without a test step.
- Concierge locks Cloudflare provider 5.23.0. Its Worker version resource accepts
  binary modules through `content_file` and computes replacement hashes itself.
- Rust, worker-build, and Terraform were not installed during that investigation.
  No Rust build, workerd run, Terraform plan/apply, or hosted egress was verified.

### Recommended decisions for this plan

| Decision | Recommendation and consequence |
| --- | --- |
| Runtime | One Rust Worker using `workers-rs`, target `wasm32-unknown-unknown`; no WASI server or permanent background task. |
| Storage | One dedicated D1 database, read replication disabled, with department JSON snapshots and a small per-semester publication cutoff. No KV or edge response cache initially. |
| Why D1 rather than the earlier KV suggestion | Preserve immediate database read-after-write and simple atomic department replacement without KV propagation windows or multi-key publication protocols. This is a deliberate refinement of the feasibility recommendation. |
| Refresh | Weekly UTC Cron plus the existing synchronous PATCH route. Both call the same refresh implementation. Publish per department, not an all-catalogue transaction. |
| Capacity | Workers Paid is the baseline. The observed 100 departments already exceed Free's 50 external subrequests; whole-catalogue capacity must still be measured. |
| Clock | Resolve the current academic term in Asia/Hong_Kong, explicitly, not the machine timezone. |
| Access | Keep public GETs; rate-limit upstream acquisitions on misses. PATCH is restricted to direct IPv4 clients in `143.89.0.0/16` under the approved review follow-up. |
| Deployment | Terraform alone creates/configures resources, uploads versions, and activates them. Wrangler supplies local execution, tests, and D1 migration execution. |

This is a proposed implementation contract. Changes identified below are not
claimed as existing behavior or permission to silently break consumers. Resolve
objections to the compatibility choices before replacing the Go deployment.

## 3. HTTP and serialization contract

Keep trailing-slash normalization, `/` redirect status/location, JSON content
types, and the existing snake_case field names. Test through actual dispatch,
not only direct handler calls. Array order across different courses is not
promised today; return courses sorted by code for reproducibility. Preserve
source order within sections and instructor section lists, and do not require
JSON object-key order or byte-for-byte number formatting.

| Route | Planned behavior |
| --- | --- |
| `GET /` | 301 redirect to `/v1`. |
| `GET /healthz` | 200 with `{"status":"ok"}`; liveness only, no upstream fetch or database probe. |
| `GET /v1` | Retain `runtime`, `hostname`, `platform`, `build_commit`, `build_date`, and string `uptime`; use truthful Worker metadata as specified below. |
| `GET /v1/semesters/current` | Existing `code`, `name`, `year`, `cohort` shape, using the explicit Hong Kong clock. |
| `GET /v1/semesters/:semester` | Same successful semester representation; validate canonical four-digit terms with suffix 10/20/30/40. Invalid input returns 400. |
| `GET /v1/courses/:course` | Uppercase supported course codes, read the current-semester department snapshot, and fetch/replace that department on a miss. Return the same Course fields or a structured error. |
| `GET /v1/courses` | Read only current-semester persisted snapshots; do not trigger a catalogue crawl. Preserve `null` when none are cached; otherwise return the cached courses. Cold-start partial population remains possible, as today without `-precache`. |
| `PATCH /v1/courses` | Await a full current-semester refresh. Return 200 and the resulting current-semester list only when all discovered departments succeed. Return an error if any fail; successful departments may already have published. Never substitute 202/background completion without approval. |

The Course representation remains `code`, `title`, numeric `credits`,
`instructors: {name: [section codes]}`, and `sections: [section codes]`. Preserve
`sections: null` for a verified course without sections, and an empty object for
no instructors. Where a section has TA entries, retain the current TA-instead-of-
instructor mapping; do not silently switch to concierge's different model.

Keep the `{status: "error", message: string}` envelope. Error prose is not a
stable machine-readable contract. Distinguish invalid input (400), a missing
course on a verified department page (404), upstream network/status/page/parse
failure (502), storage/internal failure (500), and a lookup superseded by a
newer catalogue publication with no surviving department row (503). Do not map
failed or superseded acquisition to a successful empty snapshot or invented 404.

Explicit changes from legacy behavior:

1. Non-numeric/malformed semester input becomes 400 rather than accidental 500;
   only canonical four-digit semester identifiers are accepted.
2. Upstream failure becomes 502 rather than 404. A full refresh with failures is
   no longer reported as an unconditional 200.
3. Both refresh entrypoints resolve the current semester; replacement removes
   withdrawn courses within a successful department instead of accumulating old
   entries. Last successful same-semester data survives a failed refresh.
4. Clock semantics become Asia/Hong_Kong. Confirm month/academic-year behavior
   around January, February, June, and September; use `2000 + YY` for explicit
   four-digit semester decoding. Do not promise unambiguous dates beyond 2099.
5. Validate an ASCII alphabetic department, a numeric course component, and an
   optional ASCII alphabetic suffix, including `UROP1000A`. Path fragments,
   punctuation, whitespace, and digit-first codes are rejected before fetching.
6. Preserve internal title parentheses: remove the final recognized credits
   suffix rather than truncating at the first ` (`. This fixes information loss
   and needs a real-derived fixture, not a guessed expected title.
7. A lookup superseded by a newer complete catalogue refresh returns 503 if its
   post-acquisition read has no surviving department row, even if its own write
   succeeded before pruning. Do not hide this race with an unbounded retry or a
   fabricated missing course.

For introspection, use a pinned Rust toolchain/version string, platform
`cloudflare-workers wasm32`, and configured Worker name in `hostname` (a stable
logical name, not a claimed machine hostname). Define string `uptime` as elapsed
seconds since the first handled event in the current Wasm instance, initialized
inside that event using `worker::Date::now()`. It resets on isolate or Wasm
reinitialization and is not process/global uptime or monotonic across requests
routed to different instances. Build commit/date come from the immutable build
manifest.
A separate metrics port and Go runtime counters are retired; identify and
update any real metrics consumer before cutover. Use ordinary Worker logs and
platform visibility, not a new telemetry subsystem.

## 4. Modules, dependencies, and files

Use a Cargo workspace with two crates; avoid an abstract repository framework.

| File/module | Responsibility |
| --- | --- |
| `Cargo.toml`, `Cargo.lock`, `rust-toolchain.toml` | Workspace, reproducible dependency resolution, pinned stable toolchain/target. |
| `crates/courseinfo-core/` | Models, course/semester identifiers, term mapping, department discovery, pure HTML parsing and parse errors. No Worker bindings, I/O, host clock, or native TLS dependencies. |
| `crates/courseinfo-worker/` | Fetch/scheduled entrypoints, routing, platform fetching, D1 operations, refresh orchestration, configuration and metadata. |
| `tests/fixtures/` | Minimal real-derived HTML with source URL/term/capture provenance and independently checked expected data. No live network in the permanent suite. |
| `tests/worker/` | Small JS/TS runtime suite loading production build artifacts in workerd through Wrangler's harness. |
| `db/migrations/` | Initial dedicated D1 schema and future ordered additive migrations. |
| `wrangler.jsonc` | Local artifact entrypoint, local D1, development variables, matching Cron expression. Never the production deployment owner. |
| `package.json`, `bun.lock` | Pinned Wrangler/test tooling only; the application remains Rust. |
| `deployment/{terraform.tf,variables.tf,main.tf,.terraform.lock.hcl}` | Separate courseinfo state/resources, bindings, migration ordering, module upload, activation, Cron, optional domain. |
| `.github/workflows/` and `Makefile` | Native tests, artifact build/runtime tests, Terraform checks, protected deployment workflow, local commands. |
| `README.md` and API/deployment documentation | Replace undocumented runtime assumptions with operating/cutover instructions after implementation smoke proof. |

Candidate baseline: `worker`/`worker-build` 0.8.7 (declared minimum Rust 1.91),
`serde`/`serde_json`, and `scraper` 0.29.0 with unnecessary CLI/default features
disabled. Enable `worker`'s D1 support. Pin the actual validated toolchain and
all tooling in Gate A; no floating `latest` install in CI. Do not pull in Tokio,
a native listener, reqwest's native TLS stack, or browser DOM APIs.

The core parsing interface accepts an HTML string plus the expected term and
department, and returns a complete validated department snapshot or a typed
error. The semester interface accepts a supplied date. These are the native
test seams. The Worker HTTP/scheduled interfaces are the integration seams.
Use real local D1 and mock only upstream I/O in runtime tests; do not recreate a
second implementation in a fake repository or alternate test Worker.

## 5. Acquisition and parsing

Use the existing configured upstream base origin, defaulting to
`https://w5.ab.ust.hk/wcq/cgi-bin`. It is deployment configuration, never a URL
supplied by an HTTP caller. Fixtures use a controlled local upstream override.
Construct term/department path segments only from validated identifiers.

Full refresh begins with the existing COMP discovery page, parses the union of
UG/PG department links, deduplicates it, and reuses the COMP result rather than
fetching it twice. Validate link term/origin/path and match the expected page
identity. Do not follow arbitrary discovered links or recursively fetch the
same department. Start with sequential fetching; bounded concurrency is only a
measured adjustment, not a prerequisite or parallel crawler redesign.

Inspect saved real HTML before finalizing identity, empty-state, and whitespace
rules. Cover lectures/tutorials/labs, repeated names, fractional credits,
entities, missing instructor lists, titles with parentheses, and valid no-section
courses. Do not add schedule-time/venue fields or broaden section traversal to
implement the unrelated concierge feature.

A complete department snapshot requires a verified page identity and validated
course blocks. A malformed course in that department prevents replacement of
that department; logging/skipping it must not erase the previously valid course.
A legitimate empty department must have a fixture-proven empty-state marker;
absence of selector matches alone is a parsing failure. Challenge/login pages,
wrong-term pages, truncated responses, and non-success HTTP statuses are failures.
Because the HTML parser repairs malformed markup, require fixture-established
completion evidence from the original response body, not synthesized DOM closing
tags. Include truncation between complete course blocks and immediately after
a course header: both can otherwise pass page identity while deleting real data.

Use finite per-fetch and total-refresh deadlines; proposed starting budgets are
30 seconds per fetch and 12 minutes wall time per full run, with complete body
consumption inside the fetch deadline. Verify actual cancellation/release in
workerd. Cap body bytes based on measured source sizes before parsing; do not
truncate HTML and then publish it. No automatic retry system in this rewrite.
Discard each HTML body/DOM once its snapshot is validated/persisted.

## 6. Persistence, freshness, and concurrency

### One row is the atomic replacement unit

Create `department_snapshots` with:

- `semester TEXT NOT NULL` and `department TEXT NOT NULL`, composite primary key;
- `observed_at_ms INTEGER NOT NULL`, captured just before the successful fetch;
- `courses_json TEXT NOT NULL`, a JSON object keyed by normalized course code.

Also create `semester_publications`, keyed by semester, with
`cutoff_ms INTEGER NOT NULL` and `run_id TEXT NOT NULL`. The cutoff is the start
time of the newest fully successful catalogue refresh. Its unique run token
distinguishes concurrent full refreshes with equal clock timestamps.

Use one conditional UPSERT for a department: reject any candidate at or before
that semester's cutoff, including insertion into an absent key; otherwise
replace only when `observed_at_ms` is strictly newer than the department's stored
value. Equal department timestamps are a no-op. Atomic publication of one
complete snapshot avoids delete/insert windows and long-lived distributed locks.
A delayed older acquisition must never replace a newer snapshot or resurrect a
row removed by a newer complete refresh. Failures never change the data or its
successful observation timestamp. Timestamps order observations, not an asserted
HKUST last-modified version.

Use parameterized SQL throughout. A course read selects only that course's JSON
from its department row; do not deserialize the entire catalogue per lookup.
A list read obtains course values for one semester in one database query using
SQLite JSON support, then serializes the public list once. Verify this SQL in
real local D1. Keep read replication disabled, as in concierge, so ordinary
reads use the primary and do not require cross-request bookmark management.
Do not add response caching that can conceal a newly published snapshot.

D1's row/string limit is 2,000,000 bytes. Enforce a conservative serialized
snapshot size bound including row overhead, and measure every discovered
department in Gate D. An over-limit department is a visible refresh failure,
never a truncation. If a real department cannot fit with headroom, revise the
schema to course rows before production; do not claim this layout is proven by
one COMP sample. Also measure the complete list response and peak Worker memory.

### Reads and refresh transitions

- Resolve a request's semester once and use it for every read/fetch in that
  request. Never fall back to a different semester under a current-course URL.
- A cached course returns immediately. An absent course invokes one department
  acquisition, attempts publication, then re-reads the winning stored snapshot.
  This prevents a losing concurrent fetch from returning regressed data.
  If that read has no surviving department row, return 503 for a superseded
  lookup regardless of whether its UPSERT succeeded or was rejected. A newer
  refresh may prune between a successful write and the read. Do not return an
  obsolete candidate, fabricate a 404, or automatically restart a refresh.
- If the requested course is absent on a verified successful page, return 404.
  If acquisition fails, return 502; an existing other course remains available.
- Manual and scheduled refresh share the same discovery, parsing, and publication
  logic. Continue to other departments after a per-department failure, but report
  the overall run as failed. Department snapshots may have different ages; an
  all-catalogue transactional snapshot is not promised.
- Only after a fully successful discovery/refresh may obsolete department rows
  be pruned. In one atomic D1 batch, conditionally advance the semester cutoff
  and run token, then prune only if that token belongs to this run. Advance only
  for a strictly newer run start; equal/older runs cannot apply an obsolete
  prune. Delete departments absent from the verified index only when their
  observation is at or before the winning cutoff. Every later snapshot INSERT/UPDATE
  still checks the cutoff, including departments never previously stored.
  Tombstones for existing rows alone would not close that unknown-key race.
  Bind the discovered set as JSON to stay within the 100-parameter limit.
- Resolve Cron's intended term from its scheduled time and reject delayed events
  for a term different from actual current Hong Kong time. Old/new term rows
  are separate; scheduled rollover cannot mix their courses.
- Do not reset the whole cache before fetching. Cold storage is valid: list
  returns null and a lookup can populate one department. Production warmup is
  an explicit rollout step, not an assumed process startup hook.

Duplicate concurrent origin fetches remain possible, as in the Go API. This
plan ensures publication correctness, not cross-isolate single-flight. If abuse
or actual load requires distributed coordination, obtain a separate scope/access
decision rather than hiding it in a generic lock or new Durable Object.

## 7. Runtime and Terraform deployment

Use `#[event(fetch)]` and a small direct `wasm_bindgen` export named `scheduled`.
Do not use `#[event(scheduled)]` from worker 0.8.7: its wrapper discards the
Rust handler's returned error, so `return Err(...)` alone reports success.

The explicit scheduled export accepts public `worker::worker_sys::ScheduledEvent`,
`worker::Env`, and `worker::worker_sys::ScheduleContext`, and synchronously returns
`Result<worker::js_sys::Promise, worker::wasm_bindgen::JsValue>`. Convert the event
to the high-level event, wrap the shared refresh future with
`worker::js_sys::futures::future_to_promise` (using `AssertUnwindSafe` as required
by the pinned interface), and resolve with `JsValue::UNDEFINED` or reject with
the actual refresh error. Register that same promise with raw
`ctx.wait_until(promise.clone())?` and return it. Execute refresh only once.
The generated worker-build dispatcher recognizes the named export and returns
its promise; no authored JavaScript application shim is necessary.

This path is supported by the inspected public interfaces, not yet compiled.
Gate A must prove both successful and failed scheduled outcomes in workerd.
The high-level `ScheduleContext::wait_until(Future<Output = ()>)` is not an
equivalent error-propagating escape hatch.

Await the complete relevant operation; do not start an untracked weekly timer
or move a full refresh into HTTP `waitUntil`, whose post-response lifetime is
not a cron replacement.

Set the weekly schedule to `0 19 * * SUN` (Monday 03:00 Hong Kong time).
This intentionally replaces an uptime-relative seven-day interval with a
calendar schedule. Keep its value identical in Wrangler and Terraform, and
ensure unrelated/local schedules cannot trigger accidental workloads.

Mirror concierge's resource ownership, not its unrelated resource inventory:

1. `cloudflare_d1_database` with replication disabled and a distinct name/state.
2. `cloudflare_worker` settings, including intended development/domain exposure.
3. `terraform_data` invoking pinned Wrangler D1 migrations with explicit generated
   configuration, ordered before creation of a version that reads the schema.
   Hash migration files and include database identity in replacement triggers.
4. `cloudflare_worker_version` with D1/config/build metadata bindings and generated
   JS/Wasm modules. No R2, KV, Durable Objects, browser binding, or nodejs_compat
   flag unless the actually built artifact proves one is required.
5. `cloudflare_workers_deployment` activating the selected version at 100%.
6. `cloudflare_workers_cron_trigger`, dependent on successful activation; optional
   custom domain once warmup and smoke gates pass.

Run pinned worker-build from `crates/courseinfo-worker/` with
`worker-build --release --out-dir ../../build`. Its default output directory is
relative to the package root, not the workspace; do not invoke it against the
virtual workspace manifest and assume repository-root output.

This deliberately produces repository-root `build/index.js` and
`build/index_bg.wasm`. Upload them as module names `index.js` and `index_bg.wasm`
with MIME types `application/javascript+module` and `application/wasm`,
respectively; Terraform paths are `${path.module}/../build/...`.
Set `main_module = "index.js"`. Inspect all imports in the pinned output and
include additional required modules instead of assuming this layout survives
tool upgrades.
Use `content_file`; never Terraform `file()` for binary Wasm. Provider 5.23.0
computes module `content_sha256` and replacement automatically; do not set the
computed field or add redundant annotation/hash triggers.

Use a root Wrangler configuration that loads those files unchanged:
`main: "build/index.js"`, `base_dir: "build"`, `no_bundle: true`, and
`find_additional_modules: true`. Add narrow explicit rules for `ESModule`
(`index.js`) and `CompiledWasm` (`index_bg.wasm`), with `fallthrough: false`.
Omit `build.command` entirely; Make/CI invokes worker-build explicitly once.
The pinned harness respects this no-bundle path but would still execute a
configured custom build. Use matching compatibility date/flags in Terraform
and Wrangler. Gate A verifies the actual loaded module set and file checksums;
future additional modules must be included in both configurations.

Set `cloudflare_worker_version.limits.cpu_ms = 300000` for the paid HTTP
manual-refresh path, matching the local configuration. This is a five-minute CPU
ceiling, not a promise that a request should consume it. Weekly Cron has a
different CPU budget; keep the proposed 12-minute wall deadline below its
15-minute maximum. Passing a Cron refresh does not prove synchronous PATCH can
finish under HTTP limits and real caller timeouts. Gate D covers both paths;
do not change PATCH to async to make a failing measurement disappear.

Build files must exist before `terraform plan`; `depends_on` cannot create
plan-time local files. Saved plans and applies must see the exact same artifact
bytes at the same paths. Keep state and credentials separate from concierge;
reuse its CI authentication/setup mechanism only after confirming authorization
for this repository, resource names, D1 creation/migrations, and state backend.
Do not assume the existing `cloudflare:worker-create` scope covers everything.

## 8. Ordered implementation gates

Each gate produces working behavior, not an empty scaffold. Implement one
consumer-visible test/behavior slice at a time; do not write a speculative full
suite first. No production traffic moves until all acceptance gates pass.

### Gate A — pinned build and actual runtime proof

- Pin Rust/wasm target and worker-build; use Wrangler 4.137.0 and Node 24 as the
  reviewed harness baseline, with locked Bun/Vitest dependencies and Terraform.
- Create the workspace and implement the real health route plus one real-derived
  department parsing path. Build release JS/Wasm, load those exact files in local
  workerd, and obtain a correctly parsed course through HTTP.
- Include D1 binding/JSON query proof using the production Worker entrypoint and
  the initial schema. Verify the selected scraper features compile and execute
  without unsupported entropy/thread/OS dependencies.
- Prove scheduled success and failure propagate to the actual runtime outcome,
  not only logs. The pinned scheduled macro discards a Rust handler's return
  value; implement the explicit rejection path described in section 7.
- Record module list, uncompressed bundle size (the enforced limit), optional
  compressed diagnostic size, initialization behavior, and parser memory/CPU
  observations. Native compilation alone is not this gate.

Acceptance: a real fixture travels through actual Worker dispatch, Wasm parsing,
D1 persistence, and a second HTTP request. Unsupported dependencies are resolved
here, not excused by later mocks. No hosted resources are necessary yet.

### Gate B — deterministic core and complete route contract

- Capture and minimize source-derived fixtures with provenance. Independently
  inspect expected course values; do not generate goldens from the Rust parser.
- Implement the remaining model, identifier, semester, discovery, and parsing
  behavior in vertical slices with native regression tests.
- Implement every route, normalization, introspection, and error outcome in
  section 3 through the production fetch entrypoint.
- Use the old Go API as a temporary differential oracle for unchanged success
  cases. Explicitly label the approved differences; never repin parser bugs or
  exact Go error prose as the new specification.

Acceptance: all preserved fields/routes and proposed changes are accounted for;
malformed HTML cannot yield a successful empty replacement. Tests use fixed
clocks/fixtures, not a live university endpoint.

### Gate C — persistent reads and refresh transitions

- Implement conditional snapshot publication, the atomic semester cutoff/prune
  batch, reads, complete department replacement, and failure preservation.
- Connect cold misses, synchronous PATCH, and scheduled execution to the same
  real acquisition/publication path.
- Add workerd/D1 integration coverage for overlapping acquisitions, term rollover,
  storage failure, partial full-refresh failure, and a fresh runtime reading
  previously persisted data. Mock outbound I/O only, not the D1 adapter.
- Make logs identify operation/semester/department/outcome without logging raw
  HTML, secrets, or full instructor/catalogue payloads. Report Cron failure as
  failure, not merely a logged exception with successful completion.

Acceptance: no partial department publication, timestamp regression, removed
course accumulation, cross-term fallback, or loss of last successful data.
No test-only handler or in-memory cache may be necessary for correctness.

### Gate D — infrastructure and measured staging acceptance

- Add Terraform resources, local/deployment configuration, ordered migrations,
  and immutable build metadata. Run Terraform formatting/validation and inspect
  a plan against a dedicated authorized staging environment.
- Deploy the exact artifact that passed runtime tests. Verify that a Wasm-only
  byte change is planned as a new version and that unchanged artifacts do not
  trigger code replacement; never use an invalid edited binary for apply.
- Demonstrate real HKUST acquisition from the hosted Worker, not the workstation.
  Run one complete catalogue refresh through PATCH and one through a real
  scheduled invocation; wait for real Cron propagation rather than inventing a
  remote scheduled-dispatch API. A temporary staging trigger must preserve the
  same >=1-hour CPU-budget class and be restored after observation.
- Measure every department's HTML/JSON size, whole-catalogue duration, HTTP/Cron
  CPU separately, peak memory, D1 query count, response size, upstream status
  behavior, and missing/malformed course outcomes. Verify all 100+ discovered
  departments, not only COMP. The exact count is source-derived, not hardcoded.
- Prove publication and subsequent HTTP reads after cold runtime/redeployment,
  no state loss on version activation, and failure preservation with controlled
  faults in staging/local runtime (do not deliberately overload HKUST).

Acceptance: whole-workload completion inside both execution budgets with
headroom, all rows below D1's size limit, no resource-limit failures, and real
Cloudflare egress acceptance. If a gate fails, change and re-review the design;
do not silently add Browser Run, queues, truncate data, or accept partial-only
refresh as the completed feature.

### Gate E — release automation, cutover, and rollback proof

- Replace container-publishing CI with native tests, one pinned release build,
  actual-artifact Worker integration tests, and protected Terraform deployment.
  Pass an immutable artifact plus checksums between jobs; do not rebuild
  different bytes after testing. Pull requests must not apply infrastructure.
- Serialize deployment/migration jobs per environment and retain state locking.
  Match artifact checksums at plan/apply and keep secrets out of logs/artifacts.
- Inventory the actual old hostname, clients, PATCH consumers, and metrics
  scrapers from reachable deployment configuration. These are not established
  by this repository. Resolve missing external ownership before traffic switch.
- Warm the new database by the existing synchronous PATCH route, verify expected
  catalogue counts/sample courses, and then switch the configured public
  hostname only after successful runtime/deployment checks.
- Keep the old deployed Go release available operationally for the rollback
  window. Reverting traffic is distinct from reverting a Rust Worker version.
  A Rust version rollback uses `cloudflare_workers_deployment.versions[].version_id`
  to select a recorded retained version. Add an explicit optional deployment
  version-ID input rather than always referencing only the latest version
  resource. Keep migrations additive and verify the selected version still
  exists and understands the schema before applying.
- Rehearse traffic/version rollback and Cron suspension before signoff. Trigger
  changes, including deletion, may take up to 15 minutes to propagate; wait for
  suspension or keep the old compatible scheduled handler during that window.
  Do not destroy D1 or run down-migrations as application rollback.
- Once acceptance and rollback readiness are established, remove the obsolete
  Go source/tests/module files, container image workflow, Dockerfile, and Go-only
  Makefile targets from the final code tree. Keep no compatibility daemon or
  duplicate production implementation. Historical releases remain in version
  control/artifact storage, not as a supported second runtime in the repository.
- After smoke proof, document API differences, D1 persistence/retention, clocks,
  weekly cadence, local commands, environment inputs, migrations, access policy,
  metrics retirement, measured limits, deployment, and rollback. Remove any
  throwaway comparison/benchmark scaffolding not needed for regression coverage.

Acceptance: a fresh checkout can run documented checks/builds; a protected
release deploys the tested artifact; all intended consumers use the new runtime;
rollback is exercised; obsolete implementation paths are removed.

## 9. Required regression coverage

| Seam | Consumer-visible scenarios |
| --- | --- |
| Core parser | Real course fields and associations; lectures/tutorials/labs; title parentheses; fractional credits; entities/whitespace; repeated instructors; no-section representation; malformed block rejects replacement; valid empty vs failed selection; truncated success bodies between complete blocks and after a course header. |
| Core term/identifier logic | All seasons, academic-year strings, Hong Kong midnight boundaries, explicit code validation, lowercase course normalization and suffixes, rejection of unsafe path input. |
| HTTP dispatch in workerd | Every route/status/schema, redirect/trailing slash, null cold list, successful cache miss, no upstream access for cached course/list, real missing course vs failed fetch, explicit error changes. |
| Persistent catalogue | Independent requests/runtimes see committed data; withdrawn courses disappear on replacement; failed refresh retains data/age; old-term rows never answer a current request; JSON/schema corruption is a server error. |
| Concurrent publication | Older fetch cannot win; equal timestamps are idempotent; post-acquisition read missing a row returns 503 even after an accepted publication followed by pruning; deleted/previously unknown departments cannot be resurrected below cutoff; older/equal-time completed refresh cannot prune using an obsolete index; newer legitimate acquisition can repopulate. |
| Scheduled/manual refresh | Deduplicated discovery produces the complete catalogue; COMP reused; shared failure semantics; partial successful departments survive another failure; failed Rust refresh yields a failed scheduled runtime outcome; delayed previous-term Cron rejected; manual path synchronous. |
| Operational acceptance | Actual generated module imports, D1 binding/migration, hosted acquisition, complete-workload limits, immutable artifact deployment, changed-Wasm replacement, and rollback. |

Use native `cargo test -p courseinfo-core` for pure behavior. Wrangler 4.137.0
exports `createTestHarness()`; its worker interface includes
`applyD1Migrations(bindingName)` and `scheduled(...)`. Use it under Node 24 with
Vitest against the production artifacts and real local D1. Configure
`migrations_dir = "db/migrations"` explicitly. Reapply migrations after every
test storage reset; `server.reset()` recreates storage and cannot prove
cross-runtime persistence. Use the storage-preserving reload path
`server.update(...)` for that scenario, confirming its behavior in Gate A.
`wasm-bindgen-test` alone does not prove Workers bindings. Keep SQL
correctness/atomicity tests in the actual local runtime, not only native SQLite.

Do not preserve incidental tests for default strings, formatted banners, source
text, helper forwarding, or JSON key order. Test semantic outcomes and unchanged
state, not only call counts or "did not throw". No permanent suite requires
Cloudflare credentials, paid resources, live HKUST, or wall-clock sleeps.

## 10. Activation prerequisites and review record

Before production activation, obtain or verify:

- target account/paid plan, separate state backend, D1 permissions, and domain
  ownership; preserve the old traffic target for rollback;
- acceptance of section 3's listed compatibility changes and logical metadata;
- explicit decision on the existing unauthenticated PATCH exposure, including
  any authenticated caller migration if protection is approved;
- whether the standalone API remains wanted alongside concierge's separate
  schedule snapshot plan;
- the Gate D measurements and evidence of both manual and real scheduled runs.

These are deployment/contract prerequisites, not claims that credentials or
caller decisions have already been supplied. They do not justify skipping the
reachable local implementation and test work.

Review completed with separate runtime/data-contract and deployment/testing
reviews, followed by author revisions and focused source/API follow-up. The
following findings were incorporated:

| Finding | Resolution |
| --- | --- |
| An older fetch can resurrect a physically pruned row, including a previously unknown department. | Per-semester cutoff checked by every publication; cutoff advancement/pruning is one atomic batch, gated by a unique run token. |
| A lookup's accepted write can be pruned before its subsequent read. | Any missing post-acquisition department row returns explicit 503; no stale response, invented 404, or automatic retry. |
| Fixed `uptime: \"0.00\"` is not useful runtime metadata. | Define truthful elapsed time since this isolate's first handled event, with explicit reset semantics. |
| Tolerant HTML parsing can hide truncated bodies. | Require original-body completion evidence and between-block/after-header truncation regressions. |
| Unix-style Sunday zero is outside Cloudflare's documented weekday numbering. | Use the unambiguous `SUN` Cron token. |
| worker-build defaults resolve relative to the package, not the workspace. | Run from the Worker crate with `--out-dir ../../build`; align artifact names/paths everywhere. |
| The pinned scheduled macro discards Rust errors. | Use a public direct scheduled export returning/registering one rejecting promise; require actual failed-Cron proof in Gate A. |
| Tests could rebuild/rebundle bytes different from Terraform's input. | Pin Wrangler 4.137.0, use explicit no-bundle module rules, omit custom build commands, and transfer immutable tested artifacts. |
| Storage-reset tests cannot prove persistence across runtime replacement. | Reapply migrations after isolation resets; use a storage-preserving reload for the persistence scenario. |
| CPU configuration and rollback selection were underspecified. | Name `cloudflare_worker_version.limits.cpu_ms`, require both HTTP/Cron measurements, expose retained-version selection, and account for Cron propagation on rollback. |

The reviews establish a concrete plan, not runtime acceptance. No Rust build,
workerd execution, Terraform plan/apply, full-catalogue benchmark, or hosted
egress was performed while writing this document. Those remain explicit
implementation gates; source-backed bridge/harness behavior must be exercised
against the exact pinned artifacts.

## 11. Primary references

- [Workers Rust support](https://developers.cloudflare.com/workers/languages/rust/)
- [worker-build 0.8.7 output implementation](https://docs.rs/crate/worker-build/0.8.7/source/src/main.rs)
- [worker 0.8.7 D1 binding](https://docs.rs/worker/0.8.7/worker/d1/struct.D1Database.html)
- [scraper 0.29.0 manifest](https://docs.rs/crate/scraper/0.29.0/source/Cargo.toml)
- [Workers resource limits](https://developers.cloudflare.com/workers/platform/limits/)
- [D1 limits, including rows and bound parameters](https://developers.cloudflare.com/d1/platform/limits/)
- [D1 binding and atomic batch semantics](https://developers.cloudflare.com/d1/worker-api/d1-database/)
- [Wrangler runtime test harness](https://developers.cloudflare.com/workers/testing/test-harness/get-started/)
- [Harness state, storage, and fetch mocking](https://developers.cloudflare.com/workers/testing/test-harness/prepare-test-state/)
- [Cron configuration and propagation](https://developers.cloudflare.com/workers/configuration/cron-triggers/)
- [Provider 5.23.0 module upload](https://github.com/cloudflare/terraform-provider-cloudflare/blob/v5.23.0/internal/services/worker_version/resource.go)
- [Provider 5.23.0 module hashes/schema](https://github.com/cloudflare/terraform-provider-cloudflare/blob/v5.23.0/internal/services/worker_version/schema.go)
- [worker-build output-directory resolution](https://docs.rs/crate/worker-build/0.8.7/source/src/build/mod.rs)
- [Pinned scheduled event macro](https://docs.rs/crate/worker-macros/0.8.7/source/src/event.rs)
- [Scheduled outcomes and rejected promises](https://developers.cloudflare.com/workers/runtime-apis/handlers/scheduled/)
- [Wrangler 4.137.0 harness interfaces](https://unpkg.com/wrangler@4.137.0/wrangler-dist/cli.d.ts)
- [D1 primary/read-replica consistency](https://developers.cloudflare.com/d1/best-practices/read-replication/)
- [Worker clock semantics](https://developers.cloudflare.com/workers/runtime-apis/performance/)
- [Raw scheduled context promise binding](https://docs.rs/worker-sys/0.8.7/src/worker_sys/types/schedule.rs.html)
- [Rust future-to-promise rejection contract](https://docs.rs/js-sys/0.3.106/js_sys/futures/fn.future_to_promise.html)
- [wasm-bindgen Result return contract](https://wasm-bindgen.github.io/wasm-bindgen/reference/types/result.html)
- [Pinned harness implementation](https://github.com/cloudflare/workers-sdk/blob/wrangler%404.137.0/packages/wrangler/src/api/test-harness.ts)
- [Pinned no-bundle loading implementation](https://github.com/cloudflare/workers-sdk/blob/wrangler%404.137.0/packages/wrangler/src/dev/use-esbuild.ts)
