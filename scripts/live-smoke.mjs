import { spawn } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { performance } from "node:perf_hooks";

const MAX_HTML_BYTES = 4 * 1024 * 1024;
const MAX_STDERR_BYTES = 8192;
const PARSER_TIMEOUT_MS = 30_000;
const MAX_MESSAGE_LENGTH = 1000;
const SEMESTER = /^\d{2}(?:10|20|30|40)$/;
const DEPARTMENT = /^[A-Z]+$/;

class SmokeError extends Error {
  constructor(stage, message) {
    super(message);
    this.stage = stage;
  }
}

function diagnostic(error) {
  const text = error instanceof Error ? error.message : String(error);
  return text.replace(/[\u0000-\u001f\u007f]/g, " ").slice(0, MAX_MESSAGE_LENGTH);
}

function remainingTime(deadline) {
  const remaining = deadline.expired ? 0 : deadline.at - performance.now();
  if (remaining <= 0) {
    deadline.expired = true;
    throw new SmokeError("runner", "Total run deadline exceeded");
  }
  return remaining;
}

function runParser(parserPath, args, input, deadline) {
  const remaining = remainingTime(deadline);
  const timeout = Math.min(PARSER_TIMEOUT_MS, remaining);
  return new Promise((resolveOutput, reject) => {
    const child = spawn(parserPath, args, { stdio: ["pipe", "pipe", "pipe"], windowsHide: true });
    const stdout = [];
    const stderr = [];
    let stdoutBytes = 0;
    let stderrBytes = 0;
    let inputError;
    let failure;
    const timer = setTimeout(() => {
      if (remaining <= PARSER_TIMEOUT_MS) deadline.expired = true;
      failure = new SmokeError("runner", remaining <= PARSER_TIMEOUT_MS
        ? "Total run deadline exceeded while running the native parser"
        : "Native parser exceeded its 30-second runtime limit");
      child.kill("SIGKILL");
    }, timeout);
    const failOutput = () => {
      failure = new SmokeError("runner", "Native parser exceeded its bounded output limit");
      child.kill("SIGKILL");
    };
    child.stdout.on("data", (chunk) => {
      stdoutBytes += chunk.length;
      if (stdoutBytes > MAX_HTML_BYTES) failOutput();
      else stdout.push(chunk);
    });
    child.stderr.on("data", (chunk) => {
      stderrBytes += chunk.length;
      if (stderrBytes > MAX_STDERR_BYTES) failOutput();
      else stderr.push(chunk);
    });
    child.stdin.on("error", (error) => { inputError = error; });
    child.on("error", (error) => {
      clearTimeout(timer);
      reject(new SmokeError("runner", `Cannot run native parser: ${diagnostic(error)}`));
    });
    child.on("close", (status, signal) => {
      clearTimeout(timer);
      if (failure) return reject(failure);
      if (signal) return reject(new SmokeError("runner", `Native parser terminated by ${signal}`));
      if (status !== 0) {
        // The native adapter reserves 1 for source rejection, 2 for boundary/setup errors.
        const stage = args[0] === "parse" && status === 1 ? "parse" : "runner";
        const details = diagnostic(Buffer.concat(stderr).toString("utf8").trim());
        return reject(new SmokeError(stage, `Native parser exited with status ${status}${details ? `: ${details}` : ""}`));
      }
      if (inputError) return reject(new SmokeError("runner", `Cannot supply parser input: ${diagnostic(inputError)}`));
      resolveOutput(Buffer.concat(stdout).toString("utf8").trim());
    });
    child.stdin.end(input);
  });
}

async function fetchHtml(url, fetchTimeoutMs, deadline) {
  const remaining = remainingTime(deadline);
  const timeout = Math.min(fetchTimeoutMs, remaining);
  const operationDeadline = performance.now() + timeout;
  const controller = new AbortController();
  let reader;
  const timeoutError = () => {
    const totalExpired = remaining <= fetchTimeoutMs;
    // Timer delays are integer milliseconds; expiration must survive clock rechecks.
    if (totalExpired) deadline.expired = true;
    return new SmokeError(totalExpired ? "runner" : "fetch",
      totalExpired ? "Total run deadline exceeded while fetching source"
        : `Source fetch exceeded its ${fetchTimeoutMs} ms limit (including response body)`);
  };
  const timer = setTimeout(() => controller.abort(timeoutError()), timeout);
  try {
    const response = await fetch(url, { redirect: "error", signal: controller.signal });
    if (response.status !== 200) throw new SmokeError("fetch", `Source returned HTTP ${response.status}`);
    const contentLength = response.headers.get("content-length");
    if (contentLength !== null && Number(contentLength) > MAX_HTML_BYTES) {
      throw new SmokeError("fetch", "Source response exceeds the 4 MiB HTML limit");
    }
    reader = response.body?.getReader();
    const chunks = [];
    let bytes = 0;
    if (reader) {
      while (true) {
        const { done, value } = await reader.read();
        if (performance.now() >= operationDeadline) throw timeoutError();
        if (done) break;
        bytes += value.byteLength;
        if (bytes > MAX_HTML_BYTES) throw new SmokeError("fetch", "Source response exceeds the 4 MiB HTML limit");
        chunks.push(value);
      }
    }
    return Buffer.concat(chunks, bytes);
  } catch (error) {
    if (error instanceof SmokeError) throw error;
    if (controller.signal.aborted) throw controller.signal.reason;
    throw new SmokeError("fetch", `Source request failed: ${diagnostic(error)}`);
  } finally {
    clearTimeout(timer);
    controller.abort();
    if (reader) await reader.cancel().catch(() => {});
  }
}

function parseSummary(output, department) {
  let summary;
  try {
    summary = JSON.parse(output);
  } catch {
    throw new SmokeError("runner", "Native parser returned invalid JSON");
  }
  if (!summary || !Array.isArray(summary.departments) || summary.departments.length === 0
      || !summary.departments.every((code) => typeof code === "string" && DEPARTMENT.test(code))
      || new Set(summary.departments).size !== summary.departments.length
      || !summary.departments.includes(department)
      || !Number.isSafeInteger(summary.courses) || summary.courses <= 0
      || !Number.isSafeInteger(summary.snapshot_bytes) || summary.snapshot_bytes <= 0
      || summary.snapshot_bytes > 1_800_000) {
    throw new SmokeError("runner", "Native parser returned an invalid department summary");
  }
  return summary;
}

function markdown(text) {
  return String(text).replace(/[&<>@`|\\*_\[\]#~!]/g, (character) => `&#${character.charCodeAt(0)};`);
}

function summarize(report) {
  const lines = [
    `# Weekly catalogue source smoke: ${report.status}`,
    "",
    `Semester: ${markdown(report.semester ?? "unresolved")}`,
    `Departments checked: ${report.checked_departments}/${report.discovered_departments}`,
    `Successful departments: ${report.pages.length}; courses: ${report.course_count}`,
    "",
    "Fetch failures are source retrieval problems. Parser rejection may indicate changed upstream HTML; it is not proof of an HTML change. Runner failures indicate setup, execution, or incomplete coverage.",
    "",
  ];
  if (report.failures.length === 0) {
    lines.push("Every discovered department passed production parsing and snapshot serialization.");
  } else {
    const labels = { fetch: "Source retrieval", parse: "Parser rejection", runner: "Runner/setup" };
    for (const failure of report.failures.slice(0, 25)) {
      lines.push(`- ${labels[failure.stage]} (${markdown(failure.department ?? "run")}): ${markdown(failure.message)}`);
    }
    if (report.failures.length > 25) lines.push(`- ${report.failures.length - 25} more failures are recorded in report.json.`);
    lines.push("", "Rejected parser inputs are saved as DEPARTMENT.html when available; treat them as untrusted source, not executable content.");
  }
  lines.push("", "Full URLs, per-department counts, and all failure details: report.json.", "");
  return lines.join("\n");
}

export async function runSmoke({
  baseUrl = "https://w5.ab.ust.hk/wcq/cgi-bin",
  semester,
  parserPath = "target/debug/examples/smoke_parser",
  outputDir = "smoke-results",
  fetchTimeoutMs = 30_000,
  totalTimeoutMs = 720_000,
} = {}) {
  const started = performance.now();
  const report = {
    schema_version: 1,
    status: "failed",
    semester: null,
    started_at: new Date().toISOString(),
    finished_at: "",
    discovered_departments: 0,
    checked_departments: 0,
    course_count: 0,
    pages: [],
    failures: [],
  };
  const addFailure = (error, department = null, url = null) => {
    report.failures.push({ department, url, stage: error instanceof SmokeError ? error.stage : "runner", message: diagnostic(error) });
  };
  await mkdir(outputDir, { recursive: true });
  try {
    for (const [name, value] of [["fetchTimeoutMs", fetchTimeoutMs], ["totalTimeoutMs", totalTimeoutMs]]) {
      if (!Number.isFinite(value) || value <= 0 || value > 2_147_483_647) {
        throw new SmokeError("runner", `${name} must be a positive, finite timer duration`);
      }
    }
    const deadline = { at: started + totalTimeoutMs, expired: false };
    const root = new URL(`${baseUrl.replace(/\/+$/, "")}/`);
    if (!["https:", "http:"].includes(root.protocol) || root.search || root.hash || root.username || root.password) {
      throw new SmokeError("runner", "Source base URL must be an HTTP(S) URL without credentials, query, or fragment");
    }
    const resolvedSemester = semester === undefined
      ? await runParser(parserPath, ["semester"], undefined, deadline)
      : semester;
    if (typeof resolvedSemester !== "string" || !SEMESTER.test(resolvedSemester)) {
      throw new SmokeError("runner", "Invalid semester supplied or returned by native parser");
    }
    report.semester = resolvedSemester;
    const checkDepartment = async (department) => {
      const url = new URL(`${resolvedSemester}/subject/${department}`, root).href;
      let html;
      report.checked_departments += 1;
      try {
        html = await fetchHtml(url, fetchTimeoutMs, deadline);
        const output = await runParser(parserPath, ["parse", resolvedSemester, department], html, deadline);
        const result = parseSummary(output, department);
        remainingTime(deadline);
        report.pages.push({ department, url, courses: result.courses, snapshot_bytes: result.snapshot_bytes });
        report.course_count += result.courses;
        return result;
      } catch (error) {
        addFailure(error, department, url);
        if (error instanceof SmokeError && error.stage === "parse" && html !== undefined) {
          try {
            await writeFile(join(outputDir, `${department}.html`), html);
          } catch (saveError) {
            addFailure(new SmokeError("runner", `Cannot save rejected source: ${diagnostic(saveError)}`), department, url);
          }
        }
        return null;
      }
    };
    const discovery = await checkDepartment("COMP");
    if (discovery) {
      report.discovered_departments = discovery.departments.length;
      for (const department of discovery.departments) {
        if (department === "COMP") continue;
        remainingTime(deadline);
        await checkDepartment(department);
      }
    }
  } catch (error) {
    addFailure(error);
  }
  if (report.discovered_departments > 0 && report.checked_departments !== report.discovered_departments) {
    addFailure(new SmokeError("runner", `Incomplete run: checked ${report.checked_departments} of ${report.discovered_departments} discovered departments`));
  }
  if (report.discovered_departments === 0 && report.failures.length === 0) {
    addFailure(new SmokeError("runner", "No verified department discovery was completed"));
  }
  report.status = report.failures.length === 0 ? "passed" : "failed";
  report.finished_at = new Date().toISOString();
  await writeFile(join(outputDir, "report.json"), `${JSON.stringify(report, null, 2)}\n`);
  await writeFile(join(outputDir, "summary.md"), summarize(report));
  return report;
}

if (import.meta.main) {
  try {
    const report = await runSmoke();
    console.log(`Catalogue source smoke ${report.status}: ${report.checked_departments}/${report.discovered_departments} departments checked; see smoke-results/report.json`);
    process.exitCode = report.status === "passed" ? 0 : 1;
  } catch (error) {
    console.error(`Cannot complete catalogue source smoke: ${diagnostic(error)}`);
    process.exitCode = 1;
  }
}
