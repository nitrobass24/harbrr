/// <reference types="vite/client" />

interface Window {
  // Injected by the harbrr server into index.html at serve time (internal/web/ui).
  __HARBRR_BASE_URL__?: string
  __HARBRR_VERSION__?: string
}
