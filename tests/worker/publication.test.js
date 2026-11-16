import { readFile } from "node:fs/promises";
import { afterAll, afterEach, beforeAll, beforeEach, expect, test } from "vitest";
import { createTestHarness } from "wrangler";

// Execute the exact SQL compiled into the Rust persistence adapter, in real D1.
const sql = Object.fromEntries(await Promise.all(["publish", "advance", "prune"].map(async (name) => [
  name, await readFile(new URL(`../../db/queries/${name}.sql`, import.meta.url), "utf8"),
])));
const fixture = JSON.parse(await readFile(new URL("../fixtures/2610-comp.expected.json", import.meta.url), "utf8")).courses.COMP1021;
const server = createTestHarness({ workers: [{ configPath: "./wrangler.jsonc" }] });
const api = server.getWorker();
let database;
let semester;

beforeAll(async () => { await server.listen(); });
beforeEach(async () => {
  await api.applyD1Migrations("DB");
  database = (await api.getEnv()).DB;
  semester = (await (await api.fetch("/v1/semesters/current")).json()).code;
});
afterEach(async ({ task }) => {
  if (task.result?.state === "fail") server.debug();
  await server.reset();
});
afterAll(async () => { await server.close(); });

function course(code, title) { return { ...fixture, code, title }; }
async function publish(department, time, entry) {
  await database.prepare(sql.publish).bind(semester, department, time, JSON.stringify({ [entry.code]: entry })).run();
}
async function complete(time, id, departments) {
  await database.batch([
    database.prepare(sql.advance).bind(semester, time, id),
    database.prepare(sql.prune).bind(semester, time, id, JSON.stringify(departments)),
  ]);
}
async function catalogue() {
  const response = await api.fetch("/v1/courses");
  expect(response.status).toBe(200);
  return response.json();
}

test("equal and older acquisitions cannot replace a department, but a newer one can", async () => {
  const original = course("COMP1021", "Original observation");
  await publish("COMP", 100, original);
  await publish("COMP", 100, course("COMP1021", "Equal timestamp contender"));
  await publish("COMP", 99, course("COMP1021", "Older contender"));
  expect(await catalogue()).toEqual([original]);
  const newer = course("COMP1021", "Newer observation");
  await publish("COMP", 101, newer);
  expect(await catalogue()).toEqual([newer]);
});

test("pruning blocks resurrection of both removed and previously unknown departments", async () => {
  const comp = course("COMP1021", "Current COMP");
  await publish("MATH", 100, course("MATH1012", "Removed MATH"));
  await publish("COMP", 220, comp);
  await complete(200, "complete-200", ["COMP"]);
  await publish("MATH", 150, course("MATH1012", "Late old MATH"));
  await publish("ELEC", 150, course("ELEC2991", "Late unknown ELEC"));
  expect(await catalogue()).toEqual([comp]);
  const current = course("MATH1012", "Genuinely newer MATH");
  await publish("MATH", 230, current);
  expect(await catalogue()).toEqual([comp, current]);
});

test("a same-clock delayed acquisition cannot resurrect a pruned department", async () => {
  const comp = course("COMP1021", "Retained COMP");
  await publish("MATH", 100, course("MATH1012", "Previous MATH"));
  await publish("COMP", 200, comp);
  await complete(200, "complete-200", ["COMP"]);
  await publish("MATH", 200, course("MATH1012", "Same-clock delayed MATH"));
  expect(await catalogue()).toEqual([comp]);
});

test("a same-clock excluded department is pruned even if it publishes first", async () => {
  const comp = course("COMP1021", "Retained COMP");
  await publish("COMP", 200, comp);
  await publish("MATH", 200, course("MATH1012", "Same-clock MATH"));
  await complete(200, "complete-200", ["COMP"]);
  expect(await catalogue()).toEqual([comp]);
});

test("older and equal-time complete runs cannot change the winning cutoff or run token", async () => {
  const comp = course("COMP1021", "Current COMP");
  await publish("COMP", 320, comp);
  await complete(300, "winner", ["COMP"]);
  await complete(200, "older", []);
  await complete(300, "equal-but-different-run", []);
  await publish("ELEC", 250, course("ELEC2991", "Must remain rejected"));
  expect(await catalogue()).toEqual([comp]);
  const publication = await database.prepare("SELECT cutoff_ms, run_id FROM semester_publications WHERE semester=?")
    .bind(semester).first();
  expect(publication).toEqual({ cutoff_ms: 300, run_id: "winner" });
});

test("a failed prune rolls back its cutoff so it cannot suppress later valid acquisitions", async () => {
  const comp = course("COMP1021", "Current COMP");
  const math = course("MATH1012", "Previous MATH");
  const elec = course("ELEC2991", "Still valid acquisition");
  await publish("MATH", 100, math);
  await publish("COMP", 220, comp);
  await database.prepare("CREATE TRIGGER reject_prune BEFORE DELETE ON department_snapshots BEGIN SELECT RAISE(ABORT, 'publication storage failure'); END").run();
  await expect(complete(200, "aborted-refresh", ["COMP"])).rejects.toThrow();
  await publish("ELEC", 150, elec);
  expect(await catalogue()).toEqual([comp, elec, math]);
});
