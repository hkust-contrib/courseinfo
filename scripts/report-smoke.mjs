import { open } from "node:fs/promises";

const marker = "<!-- courseinfo-live-parser-smoke -->";
const title = "Weekly catalogue source smoke test failed";
const maxReportBytes = 1024 * 1024;
const maxDisplayedFailures = 10;
const stages = {
  fetch: "Source retrieval failure",
  parse: "Parser rejection",
  runner: "Runner/setup failure",
};

function isCount(value) {
  return Number.isSafeInteger(value) && value >= 0;
}

function isNullableString(value) {
  return value === null || typeof value === "string";
}

function isReport(value) {
  return value !== null && typeof value === "object"
    && value.schema_version === 1
    && ["passed", "failed"].includes(value.status)
    && isNullableString(value.semester)
    && typeof value.started_at === "string" && Number.isFinite(Date.parse(value.started_at))
    && typeof value.finished_at === "string" && Number.isFinite(Date.parse(value.finished_at))
    && isCount(value.discovered_departments)
    && isCount(value.checked_departments)
    && isCount(value.course_count)
    && Array.isArray(value.pages) && value.pages.every((page) => page !== null && typeof page === "object"
      && typeof page.department === "string" && typeof page.url === "string"
      && isCount(page.courses) && isCount(page.snapshot_bytes))
    && Array.isArray(value.failures) && value.failures.every((failure) => failure !== null && typeof failure === "object"
      && isNullableString(failure.department) && isNullableString(failure.url)
      && typeof failure.stage === "string" && Object.hasOwn(stages, failure.stage) && typeof failure.message === "string")
    && (value.status !== "passed" || value.failures.length === 0);
}

async function readReport(path) {
  let file;
  try {
    file = await open(path, "r");
    // Read through a fixed buffer instead of trusting the file's advertised size.
    const bytes = Buffer.alloc(maxReportBytes + 1);
    let length = 0;
    while (length < bytes.length) {
      const { bytesRead } = await file.read(bytes, length, bytes.length - length, null);
      if (bytesRead === 0) break;
      length += bytesRead;
    }
    if (length > maxReportBytes) return { error: "The smoke report exceeds the 1 MiB safety limit." };
    let value;
    try {
      value = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes.subarray(0, length)));
    } catch {
      return { error: "The smoke report is not valid UTF-8 JSON." };
    }
    if (!isReport(value)) return { error: "The smoke report has an invalid or unsupported schema." };
    return { report: value };
  } catch (error) {
    return { error: error.code === "ENOENT" ? "The smoke report is missing." : "The smoke report could not be read." };
  } finally {
    if (file) await file.close();
  }
}

function safeText(value, limit) {
  // Every punctuation character is an entity, including @, backticks and colons.
  // Source text cannot become Markdown, mentions, raw HTML or workflow commands.
  const text = String(value ?? "unknown").replace(/[\p{C}\p{Z}]/gu, " ");
  let result = "";
  for (const character of text) {
    const escaped = /^[\p{L}\p{N} ]$/u.test(character) ? character : `&#${character.codePointAt(0)};`;
    if (result.length + escaped.length > limit - 1) return `${result}…`;
    result += escaped;
  }
  return result;
}

function issueBody({ context, report, error, buildOutcome, smokeOutcome }) {
  const serverUrl = context.serverUrl ?? "https://github.com";
  const runUrl = `${serverUrl.replace(/\/$/, "")}/${encodeURIComponent(context.repo.owner)}/${encodeURIComponent(context.repo.repo)}/actions/runs/${encodeURIComponent(context.runId)}`;
  const lines = [
    marker,
    "## Weekly live-source smoke failure",
    "",
    "An upstream HTML change is possible, not confirmed. Source retrieval problems and runner/setup failures are distinct from parser rejection.",
    "",
    `- Run: [workflow run](${runUrl})`,
    `- Commit: <code>${safeText(context.sha, 128)}</code>`,
    `- Build step: <code>${safeText(buildOutcome, 64)}</code>; smoke step: <code>${safeText(smokeOutcome, 64)}</code>`,
    `- Semester: <code>${safeText(report?.semester, 128)}</code>`,
    report ? `- Coverage: ${report.checked_departments} checked / ${report.discovered_departments} discovered departments; ${report.course_count} courses parsed.` : "- Coverage: unavailable because there is no valid smoke report.",
    "",
    "### Failure details",
    "",
  ];
  if (buildOutcome !== "success") lines.push("- **Runner/setup failure:** the native parser build did not succeed. Inspect the build step logs.");
  if (smokeOutcome !== "success") lines.push("- **Runner/setup status:** the smoke step did not succeed. Inspect the smoke step logs and report below.");
  if (error) lines.push(`- **Runner/setup failure:** ${error} This does not establish upstream HTML drift.`);
  if (report) {
    for (const failure of report.failures.slice(0, maxDisplayedFailures)) {
      lines.push(
        `- **${stages[failure.stage]}** — department <code>${safeText(failure.department, 256)}</code>`,
        `  - Source: <code>${safeText(failure.url, 1536)}</code>`,
        `  - Diagnostic: <code>${safeText(failure.message, 2048)}</code>`,
      );
    }
    if (report.failures.length > maxDisplayedFailures) {
      lines.push(`- ${report.failures.length - maxDisplayedFailures} additional failures omitted; see the full JSON artifact.`);
    }
    if (report.failures.length === 0) lines.push("- No per-department diagnostics were recorded. Inspect the build and smoke step logs.");
  }
  lines.push(
    "",
    "### Investigation",
    "",
    `Download [this run's artifacts](${runUrl}#artifacts) for \`report.json\`, \`summary.md\`, and any captured rejected pages. Raw source HTML is not pasted into this issue.`,
    "",
    "Reproduce from the repository with Bun and the Rust toolchain installed:",
    "",
    "```sh",
    "bun run smoke:live",
    "```",
    "",
    "For retrieval failures, check upstream availability and HTTP responses. For parser rejection, compare the rejected page with the captured fixtures and existing parser rules. For runner/setup failures, inspect the workflow logs first.",
    "",
    "This tracking issue is not closed automatically after a successful run.",
  );
  return lines.join("\n");
}

export async function reportSmoke({ github, context, reportPath = "smoke-results/report.json", buildOutcome = "success", smokeOutcome }) {
  const { report, error } = await readReport(reportPath);
  if (buildOutcome === "success" && smokeOutcome === "success" && report?.status === "passed") return { action: "none" };

  const body = issueBody({ context, report, error, buildOutcome, smokeOutcome });
  const repository = { owner: context.repo.owner, repo: context.repo.repo };
  for (let page = 1; ; page += 1) {
    const { data: issues } = await github.rest.issues.listForRepo({ ...repository, state: "open", per_page: 100, page });
    const existing = issues.find((issue) => !Object.hasOwn(issue, "pull_request") && typeof issue.body === "string" && issue.body.includes(marker));
    if (existing) {
      await github.rest.issues.createComment({ ...repository, issue_number: existing.number, body });
      return { action: "commented", issueNumber: existing.number };
    }
    if (issues.length < 100) break;
  }
  const { data: issue } = await github.rest.issues.create({ ...repository, title, body });
  return { action: "created", issueNumber: issue.number };
}
