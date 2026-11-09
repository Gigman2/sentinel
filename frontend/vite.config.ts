import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// The dev server proxies the API so the browser talks to one origin.
// PORT matches the API's listen port (see .env.example).
const api = `http://127.0.0.1:${process.env.PORT || "8080"}`;

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: { "/v1": api, "/healthz": api },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["src/test-setup.ts"],
  },
});
