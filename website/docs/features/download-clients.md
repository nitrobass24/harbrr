# Download clients & send-to-client

harbrr can hand a search result straight to a download client — from the **Search** page in
the web UI (the send icon on every result row) or via the API. Configure clients once under
**Download Clients** in the UI (or `/api/download-clients`), then any grabbed release can be
routed to them.

## Supported clients

| Kind | Protocol | Notes |
| --- | --- | --- |
| qBittorrent | torrent | The live-proven client — see [Test status](../test-status.md#download-clients) |
| Deluge | torrent | |
| Transmission | torrent | |
| rTorrent | torrent | XML-RPC |
| Flood | torrent | |
| Synology Download Station | torrent | |
| qui | torrent | Sends through a connected qui instance |
| SABnzbd | usenet | |
| NZBGet | usenet | |
| Blackhole | torrent | Writes the `.torrent` into a watch directory (absolute path, no host/credentials) |

A networked client's identity and credential live on an **App** (the same ADR-0004 identity
that app-sync connections use), so the secret is encrypted at rest and never echoed back by
the API — reads return a `<redacted>` sentinel.

## How a grab travels

`POST /api/download-clients/{id}/grab` (what the UI's send button calls) classifies the
link it is given:

- A **harbrr-sealed** download link (`…/api/indexers/{slug}/download/{token}`) is resolved
  **server-side**: harbrr fetches the `.torrent`/`.nzb` through the indexer's own session and
  hands the client the bytes — or the magnet the link resolves to. The tracker passkey never
  reaches the client.
- Any **other** link is passed through for the client to fetch itself — deliberately *not*
  through the indexer's session, so harbrr never attaches credentials to a caller-supplied
  URL.
- A client that can't handle the release's protocol (an nzb-only client sent a torrent, or
  the reverse) is refused with a `400`.

Every client also has a **Test** action (`POST /api/download-clients/{id}/test`) that
verifies reachability with the stored credentials, plus enable/disable toggles.

## Validation status

The full **Sonarr → harbrr → qBittorrent** grab pipeline is proven against live trackers;
the in-UI send-to-client flow and the other client kinds are built and covered by offline
tests but not yet live-validated — the current state is tracked in
[Test status](../test-status.md#download-clients).
