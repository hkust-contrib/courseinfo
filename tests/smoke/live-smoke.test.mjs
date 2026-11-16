import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFile, mkdtemp, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { runSmoke } from "../../scripts/live-smoke.mjs";

const parserPath = fileURLToPath(new URL("../../target/debug/examples/smoke_parser", import.meta.url));
const scriptPath = fileURLToPath(new URL("../../scripts/live-smoke.mjs", import.meta.url));
const departments = ["COMP", "ELEC", "IMBA"];
const fixtures = Object.fromEntries(await Promise.all(["COMP", "ELEC", "IMBA", "DBAP"].map(async (department) => {
  const name = department === "DBAP" ? "2610-dbap-malformed.html" : `2610-${department.toLowerCase()}.html`;
  return [department, await readFile(new URL(`../fixtures/${name}`, import.meta.url), "utf8")];
})));

// Keep the captured course markup intact; only limit discovery to local fixture departments.
function fixture(department, discovered = departments) {
  const html = fixtures[department];
  const links = discovered.map((code) => {
    const link = html.match(new RegExp(`<a class="(?:ug|pg)" href="/wcq/cgi-bin/2610/subject/${code}">${code}</a>`));
    assert.ok(link, `captured discovery must contain ${code}`);
    return link[0];
  }).join("");
  const reduced = html.replace(/(<div id="subjectItems"\s*>)[\s\S]*?(<\/div>)/, `$1${links}$2`);
  assert.notEqual(reduced, html, "fixture discovery was reduced");
  return reduced;
}

function native(args, input) {
  const result = spawnSync(parserPath, args, { input, encoding: "utf8", timeout: 15_000, maxBuffer: 64 * 1024 });
  assert.ifError(result.error);
  assert.equal(result.signal, null, result.stderr);
  return result;
}

async function outputDirectory(t) {
  const directory = await mkdtemp(join(tmpdir(), "courseinfo-live-smoke-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  return directory;
}

async function sourceServer(t, handler) {
  const requests = [];
  const server = createServer((request, response) => {
    requests.push(request.url);
    handler(request, response);
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  t.after(async () => {
    const closed = new Promise((resolve) => server.close(resolve));
    server.closeAllConnections();
    await closed;
  });
  return { baseUrl: `http://127.0.0.1:${server.address().port}/wcq/cgi-bin`, requests };
}

function serveFixtures(request, response, overrides = {}, discovered = departments) {
  const department = request.url.split("/").at(-1);
  if (!discovered.includes(department)) {
    response.writeHead(404).end();
    return;
  }
  response.setHeader("content-type", "text/html; charset=utf-8");
  response.end(overrides[department] ?? fixture(department, discovered));
}

function options(baseUrl, outputDir, overrides = {}) {
  return { baseUrl, outputDir, parserPath, semester: "2610", ...overrides };
}

async function savedReport(outputDir, report) {
  assert.deepEqual(JSON.parse(await readFile(join(outputDir, "report.json"), "utf8")), report);
  assert.ok(Date.parse(report.started_at) <= Date.parse(report.finished_at));
  return readFile(join(outputDir, "summary.md"), "utf8");
}

async function within(promise, milliseconds) {
  let timer;
  try {
    await Promise.race([promise, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error("HTTP response was not cancelled")), milliseconds);
    })]);
  } finally {
    clearTimeout(timer);
  }
}

test("native CLI parses real department fixtures and reports snapshot storage sizes", () => {
  for (const [department, courses] of [["COMP", 3], ["ELEC", 2], ["IMBA", 1]]) {
    const result = native(["parse", "2610", department], fixture(department));
    assert.equal(result.status, 0, result.stderr);
    const summary = JSON.parse(result.stdout);
    assert.deepEqual(summary.departments, departments);
    assert.equal(summary.courses, courses);
    assert.ok(summary.snapshot_bytes > 0 && summary.snapshot_bytes <= 1_800_000);
    assert.equal(result.stderr, "");
  }
});

test("native CLI resolves the current Hong Kong semester and separates usage from rejection", () => {
  const semesterNow = () => {
    const parts = Object.fromEntries(new Intl.DateTimeFormat("en-US", {
      timeZone: "Asia/Hong_Kong", year: "numeric", month: "numeric",
    }).formatToParts(new Date()).map(({ type, value }) => [type, value]));
    const year = Number(parts.year);
    const month = Number(parts.month);
    return `${String((month <= 8 ? year - 1 : year) - 2000).padStart(2, "0")}${month === 1 ? "20" : month <= 5 ? "30" : month <= 8 ? "40" : "10"}`;
  };
  const before = semesterNow();
  const semester = native(["semester"]);
  assert.equal(semester.status, 0, semester.stderr);
  assert.ok([before, semesterNow()].includes(semester.stdout.trim()));
  const usage = native(["parse", "2610"]);
  assert.equal(usage.status, 2);
  assert.match(usage.stderr, /usage:/);
  const rejected = native(["parse", "2610", "COMP"], "<html>challenge</html>");
  assert.equal(rejected.status, 1);
  assert.match(rejected.stderr, /invalid HKUST department page/);
});

test("native CLI rejects non-UTF-8, over-limit input, and snapshots exceeding production storage limits", () => {
  const invalidUtf8 = native(["parse", "2610", "COMP"], Buffer.from([0xff]));
  assert.equal(invalidUtf8.status, 1);
  assert.match(invalidUtf8.stderr, /utf-8/i);
  const oversized = native(["parse", "2610", "COMP"], Buffer.alloc(4 * 1024 * 1024 + 1, 32));
  assert.equal(oversized.status, 1);
  assert.match(oversized.stderr, /4 MiB/);
  const largeSnapshot = fixture("IMBA").replace("Corporate Innovation", '"'.repeat(910_000));
  const snapshot = native(["parse", "2610", "IMBA"], largeSnapshot);
  assert.equal(snapshot.status, 1);
  assert.match(snapshot.stderr, /snapshot exceeds the storage size limit/);
});

test("live runner checks every discovered department once and saves complete reports", async (t) => {
  const outputDir = await outputDirectory(t);
  const { baseUrl, requests } = await sourceServer(t, (request, response) => serveFixtures(request, response));
  const report = await runSmoke(options(baseUrl, outputDir));
  assert.equal(report.status, "passed");
  assert.equal(report.semester, "2610");
  assert.equal(report.discovered_departments, 3);
  assert.equal(report.checked_departments, 3);
  assert.equal(report.course_count, 6);
  assert.deepEqual(report.failures, []);
  assert.deepEqual(report.pages.map(({ department, courses }) => [department, courses]), [["COMP", 3], ["ELEC", 2], ["IMBA", 1]]);
  assert.deepEqual(requests, departments.map((department) => `/wcq/cgi-bin/2610/subject/${department}`));
  for (const page of report.pages) {
    assert.equal(page.url, `${baseUrl}/2610/subject/${page.department}`);
    assert.ok(page.snapshot_bytes > 0 && page.snapshot_bytes <= 1_800_000);
  }
  const summary = await savedReport(outputDir, report);
  assert.match(summary, /smoke: passed/);
  assert.match(summary, /Departments checked: 3\/3/);
  assert.match(summary, /production parsing and snapshot serialization/);
});

test("malformed real courses and changed section rows fail without stopping later departments", async (t) => {
  const discovered = ["COMP", "DBAP", "ELEC", "IMBA"];
  const rejected = {
    DBAP: fixture("DBAP", discovered),
    ELEC: fixture("ELEC", discovered).replace("newsect secteven mainRow", "newsect secteven"),
  };
  const outputDir = await outputDirectory(t);
  const { baseUrl, requests } = await sourceServer(t, (request, response) => serveFixtures(request, response, rejected, discovered));
  const report = await runSmoke(options(baseUrl, outputDir));
  assert.equal(report.status, "failed");
  assert.equal(report.discovered_departments, 4);
  assert.equal(report.checked_departments, 4);
  assert.equal(report.course_count, 4);
  assert.deepEqual(report.pages.map(({ department }) => department), ["COMP", "IMBA"]);
  assert.deepEqual(report.failures.map(({ department, stage }) => [department, stage]), [["DBAP", "parse"], ["ELEC", "parse"]]);
  assert.deepEqual(requests, discovered.map((department) => `/wcq/cgi-bin/2610/subject/${department}`));
  for (const department of ["DBAP", "ELEC"]) {
    assert.equal(await readFile(join(outputDir, `${department}.html`), "utf8"), rejected[department]);
  }
  const summary = await savedReport(outputDir, report);
  assert.match(summary, /Parser rejection \(DBAP\)/);
  assert.match(summary, /may indicate changed upstream HTML/);
  assert.doesNotMatch(summary, /<html>/i);
});

test("a truncated discovery response fails closed and saves the original rejected bytes", async (t) => {
  const truncated = fixture("COMP").slice(0, fixture("COMP").indexOf('<script src="/wcq/cgi-bin/msg.php'));
  const outputDir = await outputDirectory(t);
  const { baseUrl, requests } = await sourceServer(t, (request, response) => serveFixtures(request, response, { COMP: truncated }));
  const report = await runSmoke(options(baseUrl, outputDir));
  assert.equal(report.status, "failed");
  assert.equal(report.discovered_departments, 0);
  assert.equal(report.checked_departments, 1);
  assert.deepEqual(report.pages, []);
  assert.equal(report.failures[0].stage, "parse");
  assert.match(report.failures[0].message, /terminal/);
  assert.deepEqual(requests, ["/wcq/cgi-bin/2610/subject/COMP"]);
  assert.equal(await readFile(join(outputDir, "COMP.html"), "utf8"), truncated);
  await savedReport(outputDir, report);
});

test("non-200 responses are retrieval failures even when their HTML would parse", async (t) => {
  for (const status of [201, 206, 503]) {
    await t.test(`HTTP ${status}`, async (t) => {
      const outputDir = await outputDirectory(t);
      const { baseUrl, requests } = await sourceServer(t, (request, response) => {
        if (request.url.endsWith("/ELEC")) response.writeHead(status).end(fixture("ELEC"));
        else serveFixtures(request, response);
      });
      const report = await runSmoke(options(baseUrl, outputDir));
      assert.equal(report.status, "failed");
      assert.equal(report.checked_departments, 3);
      assert.deepEqual(report.pages.map(({ department }) => department), ["COMP", "IMBA"]);
      assert.deepEqual(report.failures.map(({ department, stage }) => [department, stage]), [["ELEC", "fetch"]]);
      assert.match(report.failures[0].message, new RegExp(`HTTP ${status}`));
      assert.equal(requests.length, 3);
      await assert.rejects(readFile(join(outputDir, "ELEC.html")), { code: "ENOENT" });
      assert.match(await savedReport(outputDir, report), /Source retrieval \(ELEC\)/);
    });
  }
});

test("redirects are rejected without following or retrying them", async (t) => {
  const outputDir = await outputDirectory(t);
  const { baseUrl, requests } = await sourceServer(t, (request, response) => {
    if (request.url.endsWith("/ELEC")) response.writeHead(302, { location: "/wcq/cgi-bin/2610/subject/IMBA" }).end();
    else serveFixtures(request, response);
  });
  const report = await runSmoke(options(baseUrl, outputDir));
  assert.equal(report.status, "failed");
  assert.equal(report.checked_departments, 3);
  assert.equal(report.failures[0].stage, "fetch");
  assert.deepEqual(requests, departments.map((department) => `/wcq/cgi-bin/2610/subject/${department}`));
  await savedReport(outputDir, report);
});

test("declared and streamed responses over 4 MiB are bounded fetch failures", async (t) => {
  for (const mode of ["declared", "streamed"]) {
    await t.test(mode, async (t) => {
      const outputDir = await outputDirectory(t);
      const { baseUrl } = await sourceServer(t, (request, response) => {
        if (request.url.endsWith("/ELEC")) {
          if (mode === "declared") response.writeHead(200, { "content-length": 4 * 1024 * 1024 + 1 }).end("oversized");
          else {
            response.write(Buffer.alloc(2 * 1024 * 1024, 32));
            response.end(Buffer.alloc(2 * 1024 * 1024 + 1, 32));
          }
        } else serveFixtures(request, response);
      });
      const report = await runSmoke(options(baseUrl, outputDir));
      assert.equal(report.status, "failed");
      assert.equal(report.checked_departments, 3);
      assert.equal(report.failures[0].stage, "fetch");
      assert.match(report.failures[0].message, /4 MiB/);
      assert.ok(report.pages.some(({ department }) => department === "IMBA"));
      await assert.rejects(readFile(join(outputDir, "ELEC.html")), { code: "ENOENT" });
      await savedReport(outputDir, report);
    });
  }
});

test("fetch timeouts bound both response headers and the entire body and release HTTP sockets", async (t) => {
  for (const mode of ["headers", "body"]) {
    await t.test(mode, { timeout: 10_000 }, async (t) => {
      let cancelled;
      const closed = new Promise((resolve) => { cancelled = resolve; });
      const outputDir = await outputDirectory(t);
      const { baseUrl, requests } = await sourceServer(t, (request, response) => {
        if (request.url.endsWith("/ELEC")) {
          response.on("close", cancelled);
          if (mode === "body") {
            response.writeHead(200, { "content-type": "text/html" });
            response.write(fixture("ELEC").slice(0, 100));
          }
        } else serveFixtures(request, response);
      });
      const start = Date.now();
      const report = await runSmoke(options(baseUrl, outputDir, { fetchTimeoutMs: 150, totalTimeoutMs: 5000 }));
      assert.ok(Date.now() - start < 4000, "per-fetch timeout must not wait for the run deadline");
      assert.equal(report.status, "failed");
      assert.equal(report.checked_departments, 3);
      assert.deepEqual(report.failures.map(({ stage }) => stage), ["fetch"]);
      assert.match(report.failures[0].message, /including response body/);
      assert.ok(requests.at(-1).endsWith("/IMBA"));
      await within(closed, 1000);
      await savedReport(outputDir, report);
    });
  }
});

test("total deadline stops unfinished coverage, records a runner error, and still writes reports", { timeout: 10_000 }, async (t) => {
  let cancelled;
  const closed = new Promise((resolve) => { cancelled = resolve; });
  const outputDir = await outputDirectory(t);
  const { baseUrl, requests } = await sourceServer(t, (request, response) => {
    if (request.url.endsWith("/ELEC")) {
      response.on("close", cancelled);
      response.writeHead(200).write("<html>");
    } else serveFixtures(request, response);
  });
  const start = Date.now();
  const report = await runSmoke(options(baseUrl, outputDir, { fetchTimeoutMs: 5000, totalTimeoutMs: 2000 }));
  assert.ok(Date.now() - start < 5000);
  assert.equal(report.status, "failed");
  assert.equal(report.discovered_departments, 3);
  assert.equal(report.checked_departments, 2);
  assert.deepEqual(report.pages.map(({ department }) => department), ["COMP"]);
  assert.ok(report.failures.every(({ stage }) => stage === "runner"));
  assert.ok(report.failures.some(({ message }) => /deadline exceeded/i.test(message)));
  assert.ok(report.failures.some(({ message }) => /Incomplete run/.test(message)));
  assert.equal(requests.length, 2);
  await within(closed, 1000);
  assert.match(await savedReport(outputDir, report), /Runner\/setup/);
});

test("invalid UTF-8 source is a parser rejection and its exact bounded bytes are retained", async (t) => {
  const rejected = Buffer.from([0xff, 0xfe]);
  const outputDir = await outputDirectory(t);
  const { baseUrl } = await sourceServer(t, (request, response) => serveFixtures(request, response, { ELEC: rejected }));
  const report = await runSmoke(options(baseUrl, outputDir));
  assert.equal(report.status, "failed");
  assert.equal(report.checked_departments, 3);
  assert.deepEqual(report.failures.map(({ department, stage }) => [department, stage]), [["ELEC", "parse"]]);
  assert.deepEqual(await readFile(join(outputDir, "ELEC.html")), rejected);
  await savedReport(outputDir, report);
});

test("missing native executable is a runner failure rather than a source rejection", async (t) => {
  const outputDir = await outputDirectory(t);
  const { baseUrl } = await sourceServer(t, (request, response) => serveFixtures(request, response));
  const hostileName = `@team <b>oops</b> ::error::${"x".repeat(2000)}`;
  const report = await runSmoke(options(baseUrl, outputDir, { parserPath: join(outputDir, hostileName) }));
  assert.equal(report.status, "failed");
  assert.equal(report.discovered_departments, 0);
  assert.equal(report.checked_departments, 1);
  assert.deepEqual(report.failures.map(({ stage }) => stage), ["runner"]);
  assert.match(report.failures[0].message, /Cannot run native parser/);
  await assert.rejects(readFile(join(outputDir, "COMP.html")), { code: "ENOENT" });
  const summary = await savedReport(outputDir, report);
  assert.ok(report.failures[0].message.length <= 1000);
  assert.match(summary, /&#64;team/);
  assert.doesNotMatch(summary, /@team|<b>/);
});

test("invalid runner configuration reports failure without issuing an HTTP request", async (t) => {
  const outputDir = await outputDirectory(t);
  const { baseUrl, requests } = await sourceServer(t, (request, response) => serveFixtures(request, response));
  const report = await runSmoke(options(baseUrl, outputDir, { semester: "../../bad" }));
  assert.equal(report.status, "failed");
  assert.equal(report.semester, null);
  assert.equal(report.checked_departments, 0);
  assert.deepEqual(report.failures.map(({ stage }) => stage), ["runner"]);
  assert.deepEqual(requests, []);
  await savedReport(outputDir, report);
});

test("direct Node CLI exits unsuccessfully and writes runner reports when setup is missing", async (t) => {
  const cwd = await outputDirectory(t);
  const result = spawnSync(process.execPath, [scriptPath], { cwd, encoding: "utf8", timeout: 10_000 });
  assert.ifError(result.error);
  assert.equal(result.status, 1);
  assert.match(result.stdout, /smoke failed: 0\/0 departments checked/);
  assert.doesNotMatch(result.stdout + result.stderr, /::error|::warning|<html>/);
  const report = JSON.parse(await readFile(join(cwd, "smoke-results", "report.json"), "utf8"));
  assert.equal(report.status, "failed");
  assert.equal(report.semester, null);
  assert.deepEqual(report.failures.map(({ stage }) => stage), ["runner"]);
  await savedReport(join(cwd, "smoke-results"), report);
});
