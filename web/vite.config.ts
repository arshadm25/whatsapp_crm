/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In development the dashboard proxies API calls to the Go api on :8080, matching production,
// where the ingress routes /internal and /v1 on the dashboard host to the api service.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/internal": "http://localhost:8080",
      "/v1": "http://localhost:8080",
    },
  },
  test: {
    environment: "jsdom",
  },
});
