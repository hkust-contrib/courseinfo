# API contract

Responses use JSON and `Cache-Control: no-store`. Trailing slashes are normalized.
GET routes are public. PATCH requires a direct IPv4 client in `143.89.0.0/16`;
Terraform leaves public exposure disabled until explicitly enabled.

| Method and path | Result |
| --- | --- |
| `GET /` | 301 redirect to `/v1`. |
| `GET /healthz` | `{"status":"ok"}`; liveness, not upstream/storage readiness. |
| `GET /v1` | Runtime/build metadata. |
| `GET /v1/semesters/current` | Current Hong Kong term metadata. |
| `GET /v1/semesters/:semester` | Metadata for a canonical four-digit term. |
| `GET /v1/courses/:course` | Cached current-term course, or one department acquisition on a miss. |
| `GET /v1/courses` | Current-term cached courses sorted by code; JSON `null` if none. No upstream crawl. |
| `PATCH /v1/courses` | Network-restricted synchronous complete-catalogue refresh, then the current-term list. |

Unknown routes return 404; unsupported methods on recognized routes return 405.
Method rejections include `Allow: GET`, or `Allow: GET, PATCH` for `/v1/courses`.

PATCH checks Cloudflare's `CF-Connecting-IP` before accessing D1 or upstream.
Missing, malformed, IPv6 (including IPv4-mapped IPv6), or out-of-range addresses
receive 403. Worker subrequests carrying `CF-Worker` are rejected because
same-zone Workers can override the IP header. `X-Forwarded-For`, `X-Real-IP`, and
`True-Client-IP` do not grant access. This is a network allowlist, not user
authentication; every direct client in the allowed network can request a refresh.
Scheduled refreshes do not pass through this HTTP restriction.

GET course misses share a rate limit of 30 upstream acquisitions per minute per
client IP, across course names and query strings. Cached courses and list reads
do not consume this allowance. The Cloudflare limiter is approximate and local
to each data center, not a strict global limit. Denials return 429 with
`Retry-After: 60` before contacting upstream. Missing/invalid client IPs share
one allowance; Worker subrequests share another. No negative caching or
freshness window is introduced.

## Courses

Codes are normalized to uppercase. Supported codes have an ASCII alphabetic
department, digits, and an optional alphabetic suffix, for example `COMP1021`
or `UROP1000A`. Whitespace, punctuation, encoded path fragments, and digit-first
codes are invalid.

```json
{
  "code": "COMP1021",
  "title": "Introduction to Computer Science",
  "credits": 3.0,
  "instructors": { "LAM, Gibson": ["L1"] },
  "sections": ["L1"]
}
```

The example illustrates the shape, not a complete/live offering. Actual section
order and instructor-to-section associations follow the source. A section with
TA entries uses those names instead of its instructor entries, preserving the
old API's mapping. This is not a meeting schedule or roster endpoint.
No instructors is `{}`; a structurally valid course without section rows is
`"sections": null`. No naturally sectionless course or legitimate empty-department
marker was observed in the captured catalogue; unsupported empty pages fail
closed rather than erasing cached data.

The parser removes only the final credits suffix. Parentheses within the course
title and fractional credits are preserved. Inline element boundaries do not
introduce spaces: only actual source whitespace is collapsed across text nodes.
One malformed course invalidates its department replacement. The update footer
and explicit `</script>`, `</body>`, and `</html>` at the end of the original
response provide completion evidence; the script URL and contents are not
checked. Page identity, term, course anchors, section structure, and discovery
links are also checked before publication. Instructor mappings come from the
desktop section rows, not continuation meetings or mobile duplicates.
Unclassified table rows are rejected rather than silently dropping sections.
A challenge page or truncated 200 response is not empty success.

## Semesters

Current term selection uses UTC+8/Hong Kong time:

- September–December: current academic-year prefix + `10` (Fall).
- January: previous year's prefix + `20` (Winter).
- February–May: previous year's prefix + `30` (Spring).
- June–August: previous year's prefix + `40` (Summer).

For example, `/v1/semesters/2630` returns:

```json
{
  "code": "2630",
  "name": "2026 - 2027 Spring",
  "year": "2027",
  "cohort": "2026 - 2027"
}
```

The legacy `year` field uses academic-year start for Fall/Winter and start + 1
for Spring/Summer; it is not always the calendar year of a class meeting.
Explicit prefixes represent academic years 2000–2099. The current-clock resolver
rejects unsupported years rather than ambiguously wrapping the century.

## Persistence and refresh

D1 holds a complete JSON snapshot per `(semester, department)`. Reads use the
primary database; there is no isolate-memory catalogue or edge response cache.
Different departments can have different observation ages. Old-term rows remain
stored but cannot answer current-course requests; there is no historical API or
automatic purge policy.

A successful replacement removes withdrawn courses from that department.
Older/equal-timestamp acquisitions cannot overwrite a newer snapshot. A fully
successful refresh publishes a per-semester cutoff and prunes obsolete
departments atomically. Every insert must be strictly newer than that cutoff,
including previously unknown departments. Excluded departments observed at or
before the cutoff are pruned. Same-millisecond acquisitions are conservatively
superseded: lookups reread the winning snapshot, or return 503 if the department
was removed, instead of resurrecting its obsolete data.

Acquisition/parsing failures preserve the old department data and timestamp.
A partial full refresh may publish other successful departments but returns an
error and does not finalize the catalogue. D1 cannot cancel an already-issued
atomic statement/batch: it may commit after an operation timeout. A timeout is
not a transaction-wide rollback guarantee.

Defaults: 30-second complete-body acquisition deadline, 12-minute total refresh
deadline including storage awaits, 4 MiB upstream HTML limit, and 1,800,000-byte
serialized department limit. A separate 16 MiB parsed-output accounting budget
charges retained strings/container entries before association copies, across the
whole department. This is not an exact heap ceiling: DOM storage, temporary
strings, allocator overhead, and spare container capacity are separate.
JSON encoding stops before exceeding its byte limit, including escape expansion;
neither limit truncates or partially publishes data. Timeouts abort in-flight
upstream requests. No automatic retry or distributed single-flight mechanism is
provided.

## Errors

```json
{ "status": "error", "message": "course COMP9999 not found" }
```

| Status | Meaning |
| --- | --- |
| 400 | Invalid course or semester identifier. |
| 403 | PATCH client is outside the permitted IPv4 network, lacks a valid client-IP header, or is a Worker subrequest. |
| 404 | Unknown route, or a course absent from the winning verified department snapshot. |
| 405 | Unsupported method on a recognized route. |
| 429 | GET-miss acquisition allowance exceeded; wait before retrying. |
| 500 | Storage, stored-data validation, or configuration failure. |
| 502 | Upstream status/network/body/parse failure, size limit, or operation deadline. |
| 503 | A newer refresh removed the department before a lookup's post-acquisition read. |

Error prose is diagnostic, not a stable machine-readable enum. A source failure
never becomes an invented course 404. The 503 case may follow either a rejected
old write or a successful write that was subsequently pruned.

## Introspection and migration differences

`GET /v1` retains string fields `runtime`, `hostname`, `platform`, `build_commit`,
`build_date`, and `uptime`. `hostname` is the configured logical Worker name;
`platform` is `cloudflare-workers wasm32`. `uptime` measures seconds since the
first event handled by the current Wasm instance. It can reset on isolate or
Wasm reinitialization and is not global/process uptime. Local builds identify
unprovided commit/date as `development`; CI embeds immutable source metadata.

Compared with Go: malformed semesters now return 400, upstream failures return
502 rather than 404, unsuccessful full refreshes no longer report unconditional
200, current-term selection is explicitly Hong Kong time, and successful
replacement does not accumulate withdrawn courses. PATCH now requires
`143.89.0.0/16`, and GET misses can return 429. The separate Prometheus port,
Go runtime counters, startup `-precache`, and process-relative refresh ticker
are retired. Initial population is an explicit authorized PATCH or scheduled run.
