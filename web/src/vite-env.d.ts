/// <reference types="vite/client" />

interface Window {
  // Injected by the harbrr server into index.html at serve time (internal/web/ui).
  __HARBRR_BASE_URL__?: string
  __HARBRR_VERSION__?: string
  // The configured server.external_url, or "" when unset.
  __HARBRR_EXTERNAL_URL__?: string
}
