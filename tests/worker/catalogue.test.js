import { readFile } from "node:fs/promises";
import { afterAll, afterEach, beforeAll, beforeEach, expect, test, vi } from "vitest";
import { createTestHarness } from "wrangler";

const configurations = { workers: [{ configPath: "./wrangler.jsonc" }] };
const server = createTestHarness(configurations);
const api = server.getWorker();
const departments = ["COMP", "ELEC", "IMBA"];
const captures = Object.fromEntries(await Promise.all(departments.map(async (department) => [
  department,
  await readFile(new URL(`../fixtures/2610-${department.toLowerCase()}.html`, import.meta.url), "utf8"),
])));
const expected = Object.fromEntries(await Promise.all(departments.map(async (department) => [
  department,
  JSON.parse(await readFile(new URL(`../fixtures/2610-${department.toLowerCase()}.expected.json`, import.meta.url), "utf8")).courses,
])));
let semester;
let database;

beforeAll(async () => { await server.listen(); });
beforeEach(async () => {
  await api.applyD1Migrations("DB");
  database = (await api.getEnv()).DB;
  semester = await (await api.fetch("/v1/semesters/current")).json();
  // Every origin request must be explicitly handled; no test can contact HKUST.
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    throw new Error(`Unconfigured upstream request: ${new Request(input).url}`);
  });
});
afterEach(async ({ task }) => {
  if (task.result?.state === "fail") server.debug();
  vi.restoreAllMocks();
  await server.reset();
});
afterAll(async () => { await server.close(); });

function page(department, index = departments) {
  const [start, end] = semester.cohort.split(" - ");
  const label = `${start}-${end.slice(-2)} ${semester.name.split(" ").at(-1)}`;
  return captures[department]
    .replaceAll("2026-27 Fall", label)
    .replaceAll("/2610/", `/${semester.code}/`)
    .replace(/<a class="(?:ug|pg)" href="[^"]+">([^<]+)<\/a>/g,
      (link, code) => index.includes(code) ? link : "");
}

function servePages({ index = departments, transform = (_, html) => html, statuses = {} } = {}) {
  globalThis.fetch.mockImplementation(async (input, init) => {
    const request = new Request(input, init);
    const url = new URL(request.url);
    const department = url.pathname.split("/").at(-1);
    if (url.origin !== "https://w5.ab.ust.hk" || url.pathname !== `/wcq/cgi-bin/${semester.code}/subject/${department}` || !index.includes(department)) {
      throw new Error(`Unexpected origin request: ${request.url}`);
    }
    return new Response(transform(department, page(department, index)), {
      status: statuses[department] ?? 200,
      headers: { "Content-Type": "text/html; charset=utf-8" },
    });
  });
}

async function seed(department, courses, observedAt = 0, term = semester.code) {
  await database.prepare("INSERT OR REPLACE INTO department_snapshots (semester, department, observed_at_ms, courses_json) VALUES (?, ?, ?, ?)")
    .bind(term, department, observedAt, JSON.stringify(courses)).run();
}

async function courses() {
  const response = await api.fetch("/v1/courses");
  expect(response.status).toBe(200);
  return response.json();
}

function deferred() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}

test("HTTP dispatch preserves redirect, liveness, trailing slashes and truthful metadata", async () => {
  const root = await api.fetch("/", { redirect: "manual" });
  expect(root.status).toBe(301);
  expect(root.headers.get("location")).toBe("/v1");
  const health = await api.fetch("/healthz/");
  expect(health.status).toBe(200);
  expect(await health.json()).toEqual({ status: "ok" });
  const metadata = await (await api.fetch("/v1/")).json();
  expect(metadata).toMatchObject({ platform: "cloudflare-workers wasm32", hostname: "courseinfo-local" });
  expect(metadata.runtime).toMatch(/^rustc /);
  expect(Number(metadata.uptime)).toBeGreaterThanOrEqual(0);
  expect(typeof metadata.build_commit).toBe("string");
  expect(typeof metadata.build_date).toBe("string");
  expect((await api.fetch("/v1/courses", { method: "POST" })).status).toBe(405);
  expect((await api.fetch("/unregistered")).status).toBe(404);
});

test("method rejections advertise the supported resource methods", async () => {
  for (const [path, allowed] of [
    ["/", ["GET"]],
    ["/healthz/", ["GET"]],
    ["/v1", ["GET"]],
    ["/v1/semesters/2630", ["GET"]],
    ["/v1/courses/COMP1021", ["GET"]],
    ["/v1/courses/", ["GET", "PATCH"]],
  ]) {
    const response = await api.fetch(path, { method: "POST", redirect: "manual" });
    expect(response.status).toBe(405);
    expect(new Set(response.headers.get("allow")?.split(",").map((value) => value.trim())))
      .toEqual(new Set(allowed));
  }
});

test("PATCH only accepts direct IPv4 clients within 143.89.0.0/16", async () => {
  servePages({ index: ["COMP"] });
  for (const ip of ["143.89.0.0", "143.89.0.1", "143.89.255.254", "143.89.255.255"]) {
    const response = await api.fetch("/v1/courses/", {
      method: "PATCH",
      headers: { "CF-Connecting-IP": ip, "X-Forwarded-For": "192.0.2.1" },
    });
    expect(response.status, ip).toBe(200);
    expect(await response.json()).toEqual(Object.values(expected.COMP));
  }
});

test("denied PATCH requests cannot fetch upstream or change cached courses", async () => {
  await seed("COMP", expected.COMP);
  servePages();
  const denied = [
    {},
    ...["", "143.88.255.255", "143.90.0.0", "127.0.0.1", "::1", "::ffff:143.89.1.1",
      "143.89.1.256", "143.89.1.1:443", "143.89.1.1, 192.0.2.1", "143.89.example.com",
      "143.89.01.1"].map((ip) => ({ "CF-Connecting-IP": ip })),
    { "X-Forwarded-For": "143.89.1.1", "X-Real-IP": "143.89.1.1", "True-Client-IP": "143.89.1.1" },
    { "CF-Connecting-IP": "192.0.2.1", "X-Forwarded-For": "143.89.1.1" },
    { "CF-Connecting-IP": "143.89.1.1", "CF-Worker": "example.com" },
  ];
  for (const headers of denied) {
    for (const path of ["/v1/courses", "/v1/courses/"]) {
      const response = await api.fetch(path, { method: "PATCH", headers });
      expect(response.status, JSON.stringify(headers)).toBe(403);
      expect(await response.json()).toMatchObject({ status: "error" });
      expect(response.headers.get("cache-control")).toBe("no-store");
    }
  }
  expect(globalThis.fetch).not.toHaveBeenCalled();
  expect(await courses()).toEqual(Object.values(expected.COMP));
  expect(await (await api.fetch("/v1/courses/COMP1021", {
    headers: { "CF-Connecting-IP": "192.0.2.1" },
  })).json()).toEqual(expected.COMP.COMP1021);
});

test("unclassified section rows cannot erase cached sections or instructors", async () => {
  await seed("IMBA", expected.IMBA, 7);
  servePages({ transform: (department, html) => department === "IMBA"
    ? html.replace('class="newsect secteven mainRow"', 'class="secteven"')
    : html });
  expect((await api.fetch("/v1/courses/IMBA9999")).status).toBe(502);
  expect(await (await api.fetch("/v1/courses/IMBA5620")).json()).toEqual(expected.IMBA.IMBA5620);
  expect(await courses()).toEqual(Object.values(expected.IMBA));
});

test("GET miss rate limits span course URLs without blocking cached reads or other clients", async () => {
  await seed("COMP", expected.COMP);
  servePages();
  const headers = { "CF-Connecting-IP": "192.0.2.123" };
  let blocked = false;
  // Allow for one rate-window boundary; do not assert an exact global request count.
  for (let i = 0; i < 61; i++) {
    const before = globalThis.fetch.mock.calls.length;
    const response = await api.fetch(`/v1/courses/COMP9${String(i).padStart(3, "0")}?attempt=${i}`, { headers });
    if (response.status === 429) {
      expect(globalThis.fetch.mock.calls.length).toBe(before);
      expect(response.headers.get("retry-after")).toBe("60");
      expect(await response.json()).toMatchObject({ status: "error" });
      blocked = true;
      break;
    }
    expect(response.status).toBe(404);
    await response.json();
  }
  expect(blocked).toBe(true);
  const upstreamCalls = globalThis.fetch.mock.calls.length;
  expect(await (await api.fetch("/v1/courses/COMP1021", { headers })).json()).toEqual(expected.COMP.COMP1021);
  expect(await courses()).toEqual(Object.values(expected.COMP));
  expect(globalThis.fetch.mock.calls.length).toBe(upstreamCalls);
  expect((await api.fetch("/v1/courses/COMP9999", {
    headers: { "CF-Connecting-IP": "192.0.2.124" },
  })).status).toBe(404);
});

test("semester routes return full metadata and reject malformed input as client errors", async () => {
  const response = await api.fetch("/v1/semesters/2630");
  expect(response.status).toBe(200);
  expect(await response.json()).toEqual({ code: "2630", name: "2026 - 2027 Spring", year: "2027", cohort: "2026 - 2027" });
  for (const invalid of ["XX10", "2650", "610", "02610"]) {
    const bad = await api.fetch(`/v1/semesters/${invalid}`);
    expect(bad.status).toBe(400);
    expect(await bad.json()).toMatchObject({ status: "error" });
  }
});

test("cold lookup parses and persists a department; subsequent reads need no upstream", async () => {
  expect(await courses()).toBeNull();
  servePages();
  const response = await api.fetch("/v1/courses/comp1021/");
  expect(response.status).toBe(200);
  expect(await response.json()).toEqual(expected.COMP.COMP1021);
  globalThis.fetch.mockImplementation(async () => { throw new Error("origin is unavailable"); });
  expect(await (await api.fetch("/v1/courses/COMP4900")).json()).toEqual(expected.COMP.COMP4900);
  expect(await courses()).toEqual(Object.values(expected.COMP));
  expect(response.headers.get("cache-control")).toBe("no-store");
});

test("invalid codes, genuine absence and origin failures have different statuses", async () => {
  for (const code of ["COMP", "1234", "COMP%201021", "COMP1021%3Fsecret"]) {
    expect((await api.fetch(`/v1/courses/${code}`)).status).toBe(400);
  }
  servePages();
  const missing = await api.fetch("/v1/courses/COMP9999");
  expect(missing.status).toBe(404);
  expect(await missing.json()).toMatchObject({ status: "error" });
  servePages({ statuses: { COMP: 503 } });
  expect((await api.fetch("/v1/courses/COMP9999")).status).toBe(502);
  expect(await courses()).toEqual(Object.values(expected.COMP));
});

test("complete refresh replaces withdrawn courses, discovers each department and prunes obsolete departments", async () => {
  const withdrawn = { ...expected.COMP.COMP1021, code: "COMP9998" };
  const obsolete = { ...expected.COMP.COMP1021, code: "MATH1012" };
  await seed("COMP", { COMP9998: withdrawn });
  await seed("MATH", { MATH1012: obsolete });
  await seed("COMP", { COMP9998: withdrawn }, 0, "0030");
  servePages();
  const response = await api.fetch("/v1/courses", { method: "PATCH", headers: { "CF-Connecting-IP": "143.89.1.1" } });
  expect(response.status).toBe(200);
  expect(await response.json()).toEqual(departments.flatMap((department) => Object.values(expected[department])));
  expect(await courses()).toEqual(departments.flatMap((department) => Object.values(expected[department])));
  // Alongside full catalogue equality, this catches duplicate COMP discovery work.
  expect(globalThis.fetch.mock.calls.map(([input]) => new Request(input).url)).toEqual(
    departments.map((department) => `https://w5.ab.ust.hk/wcq/cgi-bin/${semester.code}/subject/${department}`),
  );
});

test("partial refresh preserves failed department data and does not prune the old catalogue", async () => {
  const previous = { ...expected.ELEC.ELEC2991, title: "Previous successful offering" };
  const retained = { ...expected.COMP.COMP1021, code: "MATH1012" };
  await seed("ELEC", { ELEC2991: previous });
  await seed("MATH", { MATH1012: retained });
  servePages({ statuses: { ELEC: 503 } });
  const response = await api.fetch("/v1/courses", { method: "PATCH", headers: { "CF-Connecting-IP": "143.89.1.1" } });
  expect(response.status).toBe(502);
  const catalogue = await courses();
  expect(catalogue).toContainEqual(previous);
  expect(catalogue).toContainEqual(retained);
  expect(catalogue).toContainEqual(expected.COMP.COMP1021);
  expect(catalogue).toContainEqual(expected.IMBA.IMBA5620);
});

test("a truncated successful response cannot overwrite the last successful department", async () => {
  await seed("COMP", expected.COMP, 7);
  servePages({ transform: (_, html) => html.slice(0, html.indexOf('<div class="diffMsg"')) });
  expect((await api.fetch("/v1/courses/COMP9999")).status).toBe(502);
  expect(await courses()).toEqual(Object.values(expected.COMP));
  const persisted = await database.prepare("SELECT observed_at_ms FROM department_snapshots WHERE semester=? AND department='COMP'")
    .bind(semester.code).first();
  expect(persisted.observed_at_ms).toBe(7);
});

test("malformed stored courses produce server errors, not empty data or origin requests", async () => {
  await seed("COMP", { COMP1021: { ...expected.COMP.COMP1021, credits: "invalid" } });
  expect((await api.fetch("/v1/courses/COMP1021")).status).toBe(500);
  expect((await api.fetch("/v1/courses")).status).toBe(500);
});

test("a newer published snapshot wins over an older acquisition that completes last", async () => {
  const entered = deferred();
  const release = deferred();
  globalThis.fetch.mockImplementation(async () => {
    entered.resolve();
    await release.promise;
    return new Response(page("COMP"));
  });
  const request = api.fetch("/v1/courses/COMP1021");
  await entered.promise;
  const newer = { ...expected.COMP.COMP1021, title: "Newer completed acquisition" };
  await seed("COMP", { COMP1021: newer }, Date.now() + 60_000);
  release.resolve();
  const response = await request;
  expect(response.status).toBe(200);
  expect(await response.json()).toEqual(newer);
  expect(await courses()).toEqual([newer]);
});

test("a semester cutoff blocks insertion of an unknown department from an older acquisition", async () => {
  await database.prepare("INSERT INTO semester_publications VALUES (?, ?, ?)")
    .bind(semester.code, Date.now() + 60_000, "newer-complete-refresh").run();
  servePages();
  const response = await api.fetch("/v1/courses/ELEC2991");
  expect(response.status).toBe(503);
  expect(await courses()).toBeNull();
});

test("post-publication disappearance returns 503 even when the lookup write succeeded", async () => {
  // Deterministically model another writer removing the row before the follow-up read.
  await database.prepare("CREATE TRIGGER concurrent_prune AFTER INSERT ON department_snapshots BEGIN DELETE FROM department_snapshots WHERE semester=NEW.semester AND department=NEW.department; END").run();
  servePages();
  expect((await api.fetch("/v1/courses/COMP1021")).status).toBe(503);
  expect(await courses()).toBeNull();
});

test("an older full refresh cannot prune a newer publication's departments", async () => {
  const retained = { ...expected.COMP.COMP1021, code: "MATH1012" };
  await seed("MATH", { MATH1012: retained });
  await database.prepare("INSERT INTO semester_publications VALUES (?, ?, ?)")
    .bind(semester.code, Date.now() + 60_000, "newer-complete-refresh").run();
  servePages();
  const response = await api.fetch("/v1/courses", { method: "PATCH", headers: { "CF-Connecting-IP": "143.89.1.1" } });
  expect(response.status).toBe(200);
  expect(await response.json()).toEqual([retained]);
});

test("persisted courses survive replacement of the Worker runtime", async () => {
  servePages();
  expect((await api.fetch("/v1/courses/COMP1021")).status).toBe(200);
  globalThis.fetch.mockImplementation(async () => { throw new Error("origin unavailable during deployment"); });
  await server.update({ workers: [{ configPath: "./wrangler.jsonc", vars: { WORKER_NAME: "courseinfo-reloaded" } }] });
  expect((await (await api.fetch("/v1")).json()).hostname).toBe("courseinfo-reloaded");
  expect(await (await api.fetch("/v1/courses/COMP1021")).json()).toEqual(expected.COMP.COMP1021);
});

test("scheduled refresh uses the production export and persists the full catalogue", async () => {
  servePages();
  const result = await api.scheduled({ cron: "0 19 * * SUN", scheduledTime: new Date() });
  expect(result.outcome).toBe("ok");
  expect(await courses()).toEqual(departments.flatMap((department) => Object.values(expected[department])));
});

test("scheduled upstream failure rejects the runtime outcome and preserves cached data", async () => {
  await seed("COMP", expected.COMP);
  servePages({ statuses: { COMP: 503 } });
  const result = await api.scheduled({ cron: "0 19 * * SUN", scheduledTime: new Date() });
  expect(result.outcome).not.toBe("ok");
  expect(await courses()).toEqual(Object.values(expected.COMP));
});

test("unknown cron and delayed prior-term events cannot refresh current data", async () => {
  await seed("COMP", expected.COMP);
  const wrongCron = await api.scheduled({ cron: "* * * * *", scheduledTime: new Date() });
  expect(wrongCron.outcome).not.toBe("ok");
  const delayed = await api.scheduled({ cron: "0 19 * * SUN", scheduledTime: new Date(Date.now() - 370 * 86400_000) });
  expect(delayed.outcome).not.toBe("ok");
  expect(await courses()).toEqual(Object.values(expected.COMP));
});

test("redirects and oversized HTML cannot replace a cached department", async () => {
  await seed("COMP", expected.COMP);
  globalThis.fetch.mockImplementation(async () => new Response(null, {
    status: 302, headers: { Location: "https://untrusted.example/" },
  }));
  expect((await api.fetch("/v1/courses/COMP9999")).status).toBe(502);
  servePages({ transform: (_, html) => html + " ".repeat(4 * 1024 * 1024) });
  expect((await api.fetch("/v1/courses/COMP9999")).status).toBe(502);
  expect(await courses()).toEqual(Object.values(expected.COMP));
});

test("an oversized serialized snapshot fails without replacing the old row", async () => {
  await seed("COMP", expected.COMP);
  servePages({ transform: (_, html) => html.replace(">LAM, Gibson<", `>${"A".repeat(1_800_001)}<`) });
  expect((await api.fetch("/v1/courses/COMP9999")).status).toBe(502);
  expect(await courses()).toEqual(Object.values(expected.COMP));
});

test("compact association amplification fails without replacing the cached department", async () => {
  await seed("COMP", expected.COMP);
  const section = "L".repeat(65_536);
  const anchors = `>I</a>${"<a>I</a>".repeat(511)}`;
  servePages({ transform: (_, html) => html
    .replace("L1 (2225)", `${section} (2225)`)
    .replace(">LAM, Gibson</a>", anchors) });
  expect((await api.fetch("/v1/courses/COMP9999")).status).toBe(502);
  expect(await courses()).toEqual(Object.values(expected.COMP));
});

test("JSON escaping cannot bypass the serialized snapshot byte limit", async () => {
  await seed("COMP", expected.COMP);
  servePages({ transform: (_, html) => html.replace(">LAM, Gibson<", `>${"\\".repeat(1_000_000)}<`) });
  expect((await api.fetch("/v1/courses/COMP9999")).status).toBe(502);
  expect(await courses()).toEqual(Object.values(expected.COMP));
});


test("storage latency cannot turn an expired full refresh into successful pruning", async () => {
  const retained = { ...expected.COMP.COMP1021, code: "MATH1012" };
  await seed("MATH", { MATH1012: retained });
  await server.update({ workers: [{ configPath: "./wrangler.jsonc", vars: { REFRESH_TIMEOUT_MS: "500" } }] });
  database = (await api.getEnv()).DB;
  // Real D1 work, not a mocked database or a wall-clock sleep. The late atomic
  // department write may commit, but a timed-out run must never finalize/prune.
  await database.prepare(`CREATE TRIGGER slow_snapshot BEFORE INSERT ON department_snapshots BEGIN
    SELECT sum(n) FROM (
      WITH RECURSIVE numbers(n) AS (VALUES(0) UNION ALL SELECT n+1 FROM numbers WHERE n<10000000)
      SELECT n FROM numbers
    );
  END`).run();
  servePages({ index: ["COMP"] });
  expect((await api.fetch("/v1/courses", { method: "PATCH", headers: { "CF-Connecting-IP": "143.89.1.1" } })).status).toBe(502);
  const catalogue = await courses();
  expect(catalogue).toContainEqual(retained);
  expect(catalogue).toContainEqual(expected.COMP.COMP1021);
});
