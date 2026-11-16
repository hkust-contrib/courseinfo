import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const result = spawnSync("worker-build", ["--release", "--locked", "--out-dir", "../../build"], {
  cwd: fileURLToPath(new URL("../crates/courseinfo-worker/", import.meta.url)),
  env: process.env,
  stdio: "inherit",
});
if (result.error) throw result.error;
process.exit(result.status ?? 1);
