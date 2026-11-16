import assert from "node:assert/strict";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { reportSmoke } from "../../scripts/report-smoke.mjs";

const marker = "<!-- courseinfo-live-parser-smoke -->";
const context = {
  repo: { owner: "example", repo: "courseinfo" },
  serverUrl: "https://github.com",
  runId: 12345,
  sha: "0123456789abcdef",
};

function report(overrides = {}) {
  return {
    schema_version: 1,
    status: "failed",
    semester: "2610",
    started_at: "2026-11-16T00:00:00.000Z",
    finished_at: "2026-11-16T00:00:10.000Z",
    discovered_departments: 3,
    checked_departments: 3,
    course_count: 42,
    pages: [{ department: "COMP", url: "https://source.test/2610/COMP", courses: 42, snapshot_bytes: 1024 }],
    failures: [{ department: "ELEC", url: "https://source.test/2610/ELEC", stage: "parse", message: "Required course row is missing" }],
    ...overrides,
  };
}

async function reportFile(t, value = report()) {
  const directory = await mkdtemp(join(tmpdir(), "courseinfo-report-smoke-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const path = join(directory, "report.json");
  if (value !== null) await writeFile(path, typeof value === "string" ? value : JSON.stringify(value));
  return path;
}

function githubApi(issues = []) {
  const calls = { lists: [], creates: [], comments: [] };
  const stored = [...issues];
  const github = {
    rest: {
      issues: {
        async listForRepo(parameters) {
          calls.lists.push(parameters);
          const offset = (parameters.page - 1) * parameters.per_page;
          return { data: stored.slice(offset, offset + parameters.per_page) };
        },
        async create(parameters) {
          calls.creates.push(parameters);
          const issue = { number: stored.length + 1, body: parameters.body };
          stored.push(issue);
          return { data: issue };
        },
        async createComment(parameters) {
          calls.comments.push(parameters);
          return { data: { id: calls.comments.length } };
        },
      },
    },
  };
  return { github, calls };
}

function invoke(github, reportPath, options = {}) {
  return reportSmoke({ github, context, reportPath, buildOutcome: "success", smokeOutcome: "failure", ...options });
}

test("a source parser failure creates an actionable tracking issue", async (t) => {
  const path = await reportFile(t);
  const { github, calls } = githubApi();
  await invoke(github, path);

  assert.equal(calls.creates.length, 1);
  assert.equal(calls.comments.length, 0);
  const issue = calls.creates[0];
  assert.equal(issue.owner, "example");
  assert.equal(issue.repo, "courseinfo");
  assert.equal(issue.title, "Weekly catalogue source smoke test failed");
  assert.ok(issue.body.includes(marker));
  assert.ok(issue.body.includes("https://github.com/example/courseinfo/actions/runs/12345"));
  assert.ok(issue.body.includes(context.sha));
  assert.match(issue.body, /Semester:.*2610/);
  assert.match(issue.body, /3 checked.*3 discovered.*42 courses/);
  assert.ok(issue.body.includes("ELEC"));
  assert.ok(issue.body.includes("Required course row is missing"));
  assert.match(issue.body, /Parser rejection/);
  assert.match(issue.body, /possible.*not confirmed/i);
  assert.ok(issue.body.includes("#artifacts"));
  assert.ok(issue.body.includes("report.json"));
  assert.ok(issue.body.includes("bun run smoke:live"));
});

test("repeated failures comment on the same issue rather than creating duplicates", async (t) => {
  const path = await reportFile(t);
  const { github, calls } = githubApi();
  await invoke(github, path);
  await invoke(github, path);

  assert.equal(calls.creates.length, 1);
  assert.equal(calls.comments.length, 1);
  assert.equal(calls.comments[0].issue_number, 1);
  assert.ok(calls.comments[0].body.includes(marker));
});

test("tracking issue discovery paginates open issues and excludes pull requests", async (t) => {
  const path = await reportFile(t);
  const issues = [
    { number: 1, body: marker, pull_request: { url: "https://api.github.com/pulls/1" } },
    ...Array.from({ length: 99 }, (_, index) => ({ number: index + 2, body: null })),
    { number: 101, body: `Existing report\n${marker}` },
  ];
  const { github, calls } = githubApi(issues);
  await invoke(github, path);

  assert.equal(calls.creates.length, 0);
  assert.equal(calls.comments.length, 1);
  assert.equal(calls.comments[0].issue_number, 101);
  assert.deepEqual(calls.lists.map(({ page }) => page), [1, 2]);
  for (const request of calls.lists) {
    assert.equal(request.state, "open");
    assert.equal(request.per_page, 100);
    assert.equal(request.owner, "example");
    assert.equal(request.repo, "courseinfo");
  }
});

test("a matching pull request alone does not prevent issue creation", async (t) => {
  const path = await reportFile(t);
  const { github, calls } = githubApi([{ number: 7, body: marker, pull_request: {} }]);
  await invoke(github, path);
  assert.equal(calls.creates.length, 1);
  assert.equal(calls.comments.length, 0);
});

test("successful build and smoke neither create nor modify or close issues", async (t) => {
  const path = await reportFile(t, report({ status: "passed", failures: [] }));
  // No API surface is provided: any GitHub call, including a close, fails.
  await invoke({}, path, { smokeOutcome: "success" });
});

test("a failed report still raises an issue if the smoke step incorrectly reports success", async (t) => {
  const path = await reportFile(t);
  const { github, calls } = githubApi();
  await invoke(github, path, { smokeOutcome: "success" });
  assert.equal(calls.creates.length, 1);
  assert.match(calls.creates[0].body, /Parser rejection/);
});

test("missing reports identify runner setup failure even when steps claim success", async (t) => {
  const path = await reportFile(t, null);
  const { github, calls } = githubApi();
  await invoke(github, path, { smokeOutcome: "success" });
  assert.equal(calls.creates.length, 1);
  assert.match(calls.creates[0].body, /Runner\/setup failure/);
  assert.match(calls.creates[0].body, /report is missing/);
  assert.match(calls.creates[0].body, /does not establish upstream HTML drift/);
  assert.match(calls.creates[0].body, /Coverage: unavailable/);
});

test("build failure with a skipped smoke step is reported as setup failure", async (t) => {
  const path = await reportFile(t, null);
  const { github, calls } = githubApi();
  await invoke(github, path, { buildOutcome: "failure", smokeOutcome: "skipped" });
  assert.equal(calls.creates.length, 1);
  assert.match(calls.creates[0].body, /native parser build did not succeed/);
  assert.match(calls.creates[0].body, /Inspect the build step logs/);
  assert.match(calls.creates[0].body, /report is missing/);
});

test("a failed build is not hidden by an otherwise passing report", async (t) => {
  const path = await reportFile(t, report({ status: "passed", failures: [] }));
  const { github, calls } = githubApi();
  await invoke(github, path, { buildOutcome: "failure", smokeOutcome: "success" });
  assert.equal(calls.creates.length, 1);
  assert.match(calls.creates[0].body, /native parser build did not succeed/);
});

test("malformed and unsupported reports are setup failures, never raw diagnostics", async (t) => {
  const cases = [
    '{"error":"<script>@everyone ::warning::\\n```","status":',
    "null",
    "[]",
    report({ schema_version: 2 }),
    report({ failures: [{ department: "COMP", url: null, stage: "unknown", message: "bad" }] }),
    report({ failures: [{ department: "COMP", url: null, stage: "parse", message: { html: "<script>@everyone" } }] }),
    report({ checked_departments: -1 }),
    report({ pages: [{ department: "COMP" }] }),
    report({ status: "passed" }),
  ];
  for (const value of cases) {
    const path = await reportFile(t, value);
    const { github, calls } = githubApi();
    await invoke(github, path);
    assert.equal(calls.creates.length, 1);
    const body = calls.creates[0].body;
    assert.match(body, /Runner\/setup failure/);
    assert.match(body, /not valid UTF-8 JSON|invalid or unsupported schema/);
    assert.ok(!body.includes("@everyone"));
    assert.ok(!body.includes("<script>"));
    assert.ok(!body.includes("::warning::"));
  }
});

test("unreadable, oversized, and non-UTF-8 reports fail safely", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "courseinfo-report-unreadable-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const oversized = await reportFile(t, "x".repeat(1024 * 1024 + 1));
  const invalidUtf8 = join(directory, "invalid-utf8.json");
  await writeFile(invalidUtf8, Buffer.from([0x7b, 0xff, 0x7d]));
  for (const [path, expected] of [
    [directory, /could not be read/],
    [oversized, /exceeds the 1 MiB safety limit/],
    [invalidUtf8, /not valid UTF-8 JSON/],
  ]) {
    const { github, calls } = githubApi();
    await invoke(github, path);
    assert.equal(calls.creates.length, 1);
    assert.match(calls.creates[0].body, expected);
    assert.match(calls.creates[0].body, /Runner\/setup failure/);
  }
});

test("retrieval, parser, and runner failures are clearly distinguished", async (t) => {
  const path = await reportFile(t, report({
    failures: [
      { department: "ELEC", url: "https://source.test/ELEC", stage: "fetch", message: "HTTP 503" },
      { department: "COMP", url: "https://source.test/COMP", stage: "parse", message: "Missing course row" },
      { department: null, url: null, stage: "runner", message: "Native parser timed out" },
    ],
  }));
  const { github, calls } = githubApi();
  await invoke(github, path);
  const body = calls.creates[0].body;
  assert.match(body, /Source retrieval failure/);
  assert.match(body, /Parser rejection/);
  assert.match(body, /Runner\/setup failure/);
  assert.ok(body.includes("HTTP 503"));
  assert.ok(body.includes("Missing course row"));
  assert.ok(body.includes("Native parser timed out"));
  assert.match(body, /department <code>unknown<\/code>/);
});

test("untrusted diagnostics cannot inject mentions, markup, or workflow commands", async (t) => {
  const payload = '@everyone <script>alert("source")</script> <!-- injected -->\n```sh\n::warning:: dangerous\n[x](https://evil.test) ![image](https://evil.test) # heading';
  const path = await reportFile(t, report({
    semester: payload,
    failures: [{ department: payload, url: payload, stage: "parse", message: payload }],
  }));
  const { github, calls } = githubApi();
  await invoke(github, path);
  const body = calls.creates[0].body;
  for (const unsafe of ["@everyone", "<script>", "<!-- injected -->", "::warning::", "[x](", "![image](", "# heading"]) {
    assert.ok(!body.includes(unsafe), unsafe);
  }
  assert.ok(body.includes("&#64;everyone"));
  assert.ok(body.includes("&#60;script&#62;"));
  assert.equal((body.match(/```/g) ?? []).length, 2, "only the trusted reproduction command uses fences");
  assert.equal((body.match(/<!--/g) ?? []).length, 1, "only the trusted issue marker is an HTML comment");
  assert.ok(body.includes("bun run smoke:live"));
});

test("large failure lists and fields stay within GitHub's body limit", async (t) => {
  const large = "<@`:&".repeat(500);
  const path = await reportFile(t, report({
    failures: Array.from({ length: 30 }, (_, index) => ({
      department: `DEPT${index} ${large}`,
      url: `https://source.test/${large}`,
      stage: "parse",
      message: `diagnostic${index} ${large}`,
    })),
  }));
  const { github, calls } = githubApi();
  await invoke(github, path);
  const body = calls.creates[0].body;
  assert.ok(body.length < 48000, `issue body has ${body.length} characters`);
  assert.ok(body.includes("diagnostic0 "));
  assert.ok(body.includes("diagnostic9 "));
  assert.ok(!body.includes("diagnostic10 "));
  assert.match(body, /20 additional failures omitted/);
  assert.ok(body.includes("…"));
  assert.ok(body.includes("report.json"));
  assert.ok(body.includes("bun run smoke:live"));
});

test("GitHub API failures propagate rather than pretending the report was delivered", async (t) => {
  const path = await reportFile(t);
  const github = { rest: { issues: { listForRepo: async () => { throw new Error("API unavailable"); } } } };
  await assert.rejects(invoke(github, path), /API unavailable/);
});
