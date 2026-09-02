import { describe, expect, it } from "vitest"
import type { DownloadClientKind, DownloadClientSettings } from "@/lib/api"
import { KIND_SPEC } from "./kind-spec"
import type { AnyKindSpec } from "./kind-spec"

// One fully-populated settings object per kind: encode(decode(u)) must preserve it.
// This is the table's round-trip contract — the two halves agree on the field list.
const FIXTURES: { [K in DownloadClientKind]: DownloadClientSettings } = {
  qbittorrent: { qbittorrent: { category: "tv", tags: ["a", "b"], startPaused: true, tlsSkipVerify: true } },
  blackhole: { blackhole: { torrentDir: "/watch/torrents", nzbDir: "/watch/nzbs", saveMagnetFiles: true } },
  sabnzbd: { sabnzbd: { category: "movies" } },
  nzbget: { nzbget: { category: "tv" } },
  qui: { qui: { instanceId: 3, category: "tv", tags: ["a"], startPaused: true } },
  flood: { flood: { destination: "/downloads", tags: ["x"], startPaused: true } },
  "download-station": { downloadStation: { directory: "video" } },
  transmission: { transmission: { downloadDir: "/downloads", startPaused: true } },
  deluge: { deluge: { v1: true, label: "tv", downloadDir: "/downloads", startPaused: true } },
  rtorrent: { rtorrent: { label: "tv", directory: "/downloads", startPaused: true, tlsSkipVerify: true } },
}

// The exact form-state defaults decode(undefined) must seed — every field concrete
// so the inputs render controlled (arrays/numbers as strings, per SettingsForm).
const DEFAULTS: { [K in DownloadClientKind]: unknown } = {
  qbittorrent: { category: "", tags: "", startPaused: false, tlsSkipVerify: false },
  blackhole: { torrentDir: "", nzbDir: "", saveMagnetFiles: false },
  sabnzbd: { category: "" },
  nzbget: { category: "" },
  qui: { instanceId: "", category: "", tags: "", startPaused: false },
  flood: { destination: "", tags: "", startPaused: false },
  "download-station": { directory: "" },
  transmission: { downloadDir: "", startPaused: false },
  deluge: { v1: false, label: "", downloadDir: "", startPaused: false },
  rtorrent: { label: "", directory: "", startPaused: false, tlsSkipVerify: false },
}

const KINDS = Object.keys(KIND_SPEC) as DownloadClientKind[]

describe("KIND_SPEC", () => {
  it.each(KINDS)("%s: encode(decode(u)) round-trips a populated fixture", (k) => {
    const spec = KIND_SPEC[k] as unknown as AnyKindSpec
    expect(spec.encode(spec.decode(FIXTURES[k]))).toEqual(FIXTURES[k])
  })

  it.each(KINDS)("%s: decode(undefined) yields the form defaults", (k) => {
    const spec = KIND_SPEC[k] as unknown as AnyKindSpec
    expect(spec.decode(undefined)).toEqual(DEFAULTS[k])
  })
})
