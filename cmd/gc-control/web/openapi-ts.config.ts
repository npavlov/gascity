import { defineConfig } from "@hey-api/openapi-ts";

export default defineConfig({
  input: "../../../internal/controlcenter/openapi.json",
  output: {
    path: "./src/generated/sse",
  },
  plugins: [
    "@hey-api/client-fetch",
    "@hey-api/typescript",
    "@hey-api/sdk",
  ],
});
