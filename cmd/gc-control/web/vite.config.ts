import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": new URL("./src", import.meta.url).pathname,
    },
  },
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8477",
      "/ws": "http://127.0.0.1:8477",
      "/openapi.json": "http://127.0.0.1:8477",
    },
  },
  test: {
    css: true,
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
  },
});
