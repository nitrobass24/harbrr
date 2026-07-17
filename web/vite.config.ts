/// <reference types="vitest/config" />

import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { defineConfig } from "vite"
import { VitePWA } from "vite-plugin-pwa"

const __dirname = path.dirname(fileURLToPath(import.meta.url))

// Exported (not just inlined below) so pwa.test.ts can assert on it directly
// without re-running a full production build.
export const pwaOptions = {
  registerType: "autoUpdate" as const,
  workbox: {
    // Precache the built app shell only; /api is live authed data and must
    // never be served from cache or replayed to a logged-out session.
    globPatterns: ["**/*.{js,css,html,ico,png,svg,webp}"],
    navigateFallbackDenylist: [/^\/api(?:\/|$)/],
    runtimeCaching: [
      {
        // A callback, not a RegExp: Workbox tests a RegExp against the FULL
        // request URL (https://host/api/...), so a ^-anchored pathname regex
        // would never match and the route would be dead code.
        urlPattern: ({ url }: { url: URL }) => url.pathname.startsWith("/api/"),
        handler: "NetworkOnly" as const,
      },
    ],
  },
  manifest: {
    name: "harbrr",
    short_name: "harbrr",
    description: "Cardigann-compatible Torznab/Newznab search provider for the autobrr family",
    display: "standalone" as const,
    start_url: "/",
    theme_color: "#0074ca",
    background_color: "#111114",
    icons: [
      {
        src: "pwa-192x192.png",
        sizes: "192x192",
        type: "image/png",
      },
      {
        src: "pwa-512x512.png",
        sizes: "512x512",
        type: "image/png",
      },
    ],
  },
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss(), VitePWA(pwaOptions)],
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
    // globals gives @testing-library/react its afterEach auto-cleanup hook.
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
  },
})
