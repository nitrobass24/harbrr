// Package download sends grabbed releases to the user's configured download
// clients (qBittorrent, SABnzbd, and NZBGet today; qui, Flood, Download Station,
// Transmission, Deluge, rTorrent, and a blackhole watch-folder are seeded kind
// constants awaiting their own driver — see autobrr/harbrr#8). harbrr never
// downloads a torrent/nzb itself; it hands the resolved Payload (bytes it fetched
// server-side, or a URL the client fetches itself — magnet, a sealed /dl link, an
// nzb URL) to a Driver, which is all a client kind needs to implement.
//
// Service (service.go) is the CRUD + connection-resource surface, mirroring
// internal/notify: connresource.Lifecycle sequences create/update/delete against
// the encrypted-at-rest secret (the client's password or API key, meaning depends
// on kind), Kind is immutable once created, and TestConnection builds a Driver on
// demand via the package-level factory (newDriver) — decrypt-then-build stays a
// Service seam so a future sync-clients-to-apps feature (#237) can reach the
// plaintext secret at sync time without redesigning this package.
//
// The factory (download.go) is the single source of truth for both construction
// and kind validity: adding driver #2 is one entry in the drivers map plus its own
// file — no Service, route, or handler edits.
//
// See AGENTS.md and docs/architecture.md.
package download
