import { loadEnv } from "vite";
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// The dev server proxies the API so the browser talks to one origin. The
// API's port comes from PORT: the shell environment first, then the repo's
// .env (the same rule as `make run`), defaulting to 8080.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, "..", "");
  const api = `http://127.0.0.1:${env.PORT || "8080"}`;
  return {
    plugins: [react()],
    server: {
      port: 5173,
      proxy: { "/v1": api, "/healthz": api },
    },
    test: {
      environment: "jsdom",
      setupFiles: ["src/test-setup.ts"],
    },
  };
});
