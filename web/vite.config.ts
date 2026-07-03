/// <reference types="vitest/config" />

import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { defineConfig } from "vite"

const __dirname = path.dirname(fileURLToPath(import.meta.url))

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    proxy: {
      "/api": {
        target: "http://localhost:7478",
        changeOrigin: true,
      },
      "/healthz": {
        target: "http://localhost:7478",
        changeOrigin: true,
      },
    },
  },
  test: {
    environment: "jsdom",
  },
})
