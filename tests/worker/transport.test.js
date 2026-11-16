import { spawn, execFile } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { afterEach, expect, test } from "vitest";

// The test harness proxies origin fetches through Node and does not forward
// response-stream cancellation. Use the real CLI transport for this seam.
const cli = resolve("node_modules/.bin/wrangler");
const run = promisify(execFile);
const config = resolve("wrangler.jsonc");
const fixture = await readFile(new URL("../fixtures/2610-comp.html", import.meta.url), "utf8");
const expected = JSON.parse(await readFile(new URL("../fixtures/2610-comp.expected.json", import.meta.url), "utf8")).courses;
const environment = { ...process.env, CI: "true", WRANGLER_SEND_METRICS: "false", NO_COLOR: "1", FORCE_COLOR: "0" };
let origin;
let child;
let exited;
let state;
let apiUrl;
let semester;
let handler;
let workerOutput = "";
let stage;

async function start(fetchTimeout, refreshTimeout) {
  workerOutput = "";
  stage = "initializing local storage";
  state = await mkdtemp(join(tmpdir(), "courseinfo-transport-"));
  const isolatedEnvironment = { ...environment, WRANGLER_REGISTRY_PATH: join(state, "registry") };
  origin = createServer((request, response) => handler(request, response));
  origin.listen(0, "127.0.0.1");
  await once(origin, "listening");
  const baseUrl = `http://127.0.0.1:${origin.address().port}/wcq/cgi-bin`;
  await run(cli, ["d1", "migrations", "apply", "DB", "--local", "--persist-to", state, "--config", config], {
    env: isolatedEnvironment, timeout: 15000,
  });
  stage = "starting local Worker";
  child = spawn(cli, [
    "dev", "--local", "--config", config, "--ip", "127.0.0.1", "--port", "0", "--inspector-port", "0",
    "--persist-to", state, "--var", `BASE_URL:${baseUrl}`,
    "--var", `FETCH_TIMEOUT_MS:${fetchTimeout}`, "--var", `REFRESH_TIMEOUT_MS:${refreshTimeout}`,
  ], { env: isolatedEnvironment, stdio: ["ignore", "pipe", "pipe"] });
  exited = new Promise((done) => child.once("exit", done));
  apiUrl = await new Promise((done, reject) => {
    const capture = (chunk) => {
      workerOutput += chunk.toString();
      const ready = workerOutput.match(/Ready on (http:\/\/127\.0\.0\.1:\d+)/);
      if (ready) done(ready[1]);
    };
    child.stdout.on("data", capture);
    child.stderr.on("data", capture);
    child.once("error", reject);
    child.once("exit", () => reject(new Error(`Local Worker exited before startup:\n${workerOutput}`)));
  });
  stage = "reading current semester";
  semester = await (await fetch(`${apiUrl}/v1/semesters/current`)).json();
  stage = "exercising HTTP";
}

function compPage(index) {
  const [start, end] = semester.cohort.split(" - ");
  return fixture.replaceAll("2026-27 Fall", `${start}-${end.slice(-2)} ${semester.name.split(" ").at(-1)}`)
    .replaceAll("/2610/", `/${semester.code}/`)
    .replace(/<a class="(?:ug|pg)" href="[^"]+">([^<]+)<\/a>/g, (link, code) => index.includes(code) ? link : "");
}

function streamUntilCanceled(response, closed) {
  response.on("close", () => closed.resolve(response.writableEnded));
  response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
  response.write("<html>");
}

function deferred() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { resolve, promise };
}

async function catalogue() {
  const response = await fetch(`${apiUrl}/v1/courses`);
  expect(response.status).toBe(200);
  return response.json();
}

afterEach(async ({ task }) => {
  if (task.result?.state === "fail") console.error({ stage, workerOutput });
  if (child) {
    const process = child;
    const kill = setTimeout(() => process.kill("SIGKILL"), 5000);
    kill.unref();
    process.kill("SIGTERM");
    await exited;
    clearTimeout(kill);
    child = undefined;
  }
  if (origin) {
    origin.closeAllConnections();
    await new Promise((done) => origin.close(done));
    origin = undefined;
  }
  if (state) { await rm(state, { recursive: true, force: true }); state = undefined; }
});

test("a page deadline closes the actual upstream connection and preserves its cached snapshot", async () => {
  await start(1000, 5000);
  handler = (_, response) => response.end(compPage(["COMP"]));
  expect((await fetch(`${apiUrl}/v1/courses/COMP1021`)).status).toBe(200);
  const closed = deferred();
  handler = (_, response) => streamUntilCanceled(response, closed);
  expect((await fetch(`${apiUrl}/v1/courses/COMP9999`)).status).toBe(502);
  stage = "waiting for upstream cancellation";
  expect(await closed.promise).toBe(false);
  expect(await catalogue()).toEqual(Object.values(expected));
});

test("the total refresh deadline closes an in-flight department body", async () => {
  await start(30000, 1000);
  // Local Wrangler permits simulated edge headers; production Cloudflare supplies them.
  expect((await fetch(`${apiUrl}/v1/courses`, { method: "PATCH" })).status).toBe(403);
  const closed = deferred();
  handler = (request, response) => {
    if (request.url.endsWith("/COMP")) response.end(compPage(["COMP", "ELEC"]));
    else streamUntilCanceled(response, closed);
  };
  expect((await fetch(`${apiUrl}/v1/courses`, {
    method: "PATCH", headers: { "CF-Connecting-IP": "143.89.1.1" },
  })).status).toBe(502);
  stage = "waiting for refresh cancellation";
  expect(await closed.promise).toBe(false);
  expect(await catalogue()).toEqual(Object.values(expected));
});
