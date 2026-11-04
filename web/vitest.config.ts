import { defineConfig } from "vitest/config";

export default defineConfig({
  // Component tests compile JSX with the automatic runtime, as the app build does.
  esbuild: { jsx: "automatic" },
  test: { include: ["src/**/*.test.{ts,tsx}"], environment: "node" },
});
