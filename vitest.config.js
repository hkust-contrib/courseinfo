import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: ["tests/worker/**/*.test.js"],
    fileParallelism: false,
    testTimeout: 30000,
    hookTimeout: 60000,
  },
});
