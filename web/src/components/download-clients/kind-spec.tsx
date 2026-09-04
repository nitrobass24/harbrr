import type { ReactNode } from "react"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import type { DownloadClientKind, DownloadClientSettings } from "@/lib/api"

// Each kind's settings schema, indexed out of the generated union (api.ts:5-9 rule:
// derived references, never hand-mirrored copies).
type SettingsMap = {
  qbittorrent: NonNullable<DownloadClientSettings["qbittorrent"]>
  blackhole: NonNullable<DownloadClientSettings["blackhole"]>
  sabnzbd: NonNullable<DownloadClientSettings["sabnzbd"]>
  nzbget: NonNullable<DownloadClientSettings["nzbget"]>
  qui: NonNullable<DownloadClientSettings["qui"]>
  flood: NonNullable<DownloadClientSettings["flood"]>
  "download-station": NonNullable<DownloadClientSettings["downloadStation"]>
  transmission: NonNullable<DownloadClientSettings["transmission"]>
  deluge: NonNullable<DownloadClientSettings["deluge"]>
  rtorrent: NonNullable<DownloadClientSettings["rtorrent"]>
}

// The in-form shape of a kind's settings, derived from the generated schema so the
// compiler ties decode/encode/Fields to the same field list: every field concrete
// (controlled inputs), string[] edited as comma-joined text, number (qui's
// instanceId) as the input's string ("" = unset).
type FormValue<T> = T extends string[] ? string : T extends number ? string : T
export type SettingsForm<K extends DownloadClientKind> = {
  [F in keyof Required<SettingsMap[K]>]: FormValue<Required<SettingsMap[K]>[F]>
}

// How a kind's host column is entered and validated. The Go side's twin is
// internal/download/download.go's drivers map (hostURL/hostPort/hostNone); "app" is
// the web-only qui path where an App holds the URL identity.
export type HostMode = "url" | "hostport" | "none" | "app"

// One kind's whole form contract: how its host is entered/validated, how the stored
// settings union becomes form state and back, and its settings JSX.
export type KindSpec<K extends DownloadClientKind> = {
  hostMode: HostMode
  decode: (u: DownloadClientSettings | undefined) => SettingsForm<K>
  encode: (s: SettingsForm<K>) => DownloadClientSettings
  // children is the qui instance picker slot — identity machinery that stays in the
  // form (it needs the App/instances state); every other kind ignores it.
  Fields: (props: { s: SettingsForm<K>; set: (s: SettingsForm<K>) => void; children?: ReactNode }) => ReactNode
}

// The union of every kind's form settings — the type of the section's one
// `settings` state.
export type AnySettingsForm = { [K in DownloadClientKind]: SettingsForm<K> }[DownloadClientKind]

// KIND_SPEC[kind] widened for the form: `settings` always holds the CURRENT kind's
// decode() output (it is reset on every kind switch), a kind↔settings correlation
// TS can't carry through the table lookup — the section casts the looked-up spec to
// this instead of threading per-kind generics through JSX.
export type AnyKindSpec = {
  hostMode: HostMode
  decode: (u: DownloadClientSettings | undefined) => AnySettingsForm
  encode: (s: AnySettingsForm) => DownloadClientSettings
  Fields: (props: { s: AnySettingsForm; set: (s: AnySettingsForm) => void; children?: ReactNode }) => ReactNode
}

const joinTags = (tags: string[] | undefined) => (tags ?? []).join(", ")
const splitTags = (tags: string) => (tags ? tags.split(",").map((t) => t.trim()).filter(Boolean) : undefined)

// sabnzbd and nzbget share one category-only settings shape.
function CategoryFields({ s, set }: { s: { category: string }; set: (s: { category: string }) => void }) {
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
      <span className="flex flex-col gap-1.5">
        <Label htmlFor="dlc-category">Category <span className="text-faint">(optional)</span></Label>
        <Input id="dlc-category" value={s.category} onChange={(e) => set({ category: e.target.value })} />
      </span>
    </div>
  )
}

export const KIND_SPEC = {
  qbittorrent: {
    hostMode: "url",
    decode: (u) => ({
      category: u?.qbittorrent?.category ?? "",
      tags: joinTags(u?.qbittorrent?.tags),
      startPaused: u?.qbittorrent?.startPaused ?? false,
      tlsSkipVerify: u?.qbittorrent?.tlsSkipVerify ?? false,
    }),
    encode: (s) => ({ qbittorrent: { category: s.category || undefined, tags: splitTags(s.tags), startPaused: s.startPaused || undefined, tlsSkipVerify: s.tlsSkipVerify || undefined } }),
    Fields: ({ s, set }) => (
      <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
        <div className="grid grid-cols-2 gap-3">
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-category">Category <span className="text-faint">(optional)</span></Label>
            <Input id="dlc-category" value={s.category} onChange={(e) => set({ ...s, category: e.target.value })} />
          </span>
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-tags">Tags <span className="text-faint">(comma-separated, optional)</span></Label>
            <Input id="dlc-tags" value={s.tags} onChange={(e) => set({ ...s, tags: e.target.value })} />
          </span>
        </div>
        <label className="flex items-center gap-2 text-[13px]">
          <Switch checked={s.startPaused} onCheckedChange={(v) => set({ ...s, startPaused: v })} />
          Start paused
        </label>
        <label className="flex items-center gap-2 text-[13px]">
          <Switch checked={s.tlsSkipVerify} onCheckedChange={(v) => set({ ...s, tlsSkipVerify: v })} />
          Skip TLS certificate verification
        </label>
      </div>
    ),
  },
  blackhole: {
    hostMode: "none",
    decode: (u) => ({
      torrentDir: u?.blackhole?.torrentDir ?? "",
      nzbDir: u?.blackhole?.nzbDir ?? "",
      saveMagnetFiles: u?.blackhole?.saveMagnetFiles ?? false,
    }),
    encode: (s) => ({ blackhole: { torrentDir: s.torrentDir || undefined, nzbDir: s.nzbDir || undefined, saveMagnetFiles: s.saveMagnetFiles || undefined } }),
    Fields: ({ s, set }) => (
      <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
        <div className="grid grid-cols-2 gap-3">
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-torrent-dir">Torrent watch folder <span className="text-faint">(optional)</span></Label>
            <Input id="dlc-torrent-dir" placeholder="/watch/torrents" value={s.torrentDir} onChange={(e) => set({ ...s, torrentDir: e.target.value })} />
          </span>
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-nzb-dir">NZB watch folder <span className="text-faint">(optional)</span></Label>
            <Input id="dlc-nzb-dir" placeholder="/watch/nzbs" value={s.nzbDir} onChange={(e) => set({ ...s, nzbDir: e.target.value })} />
          </span>
        </div>
        <label className="flex items-center gap-2 text-[13px]">
          <Switch checked={s.saveMagnetFiles} onCheckedChange={(v) => set({ ...s, saveMagnetFiles: v })} />
          Save magnet-only releases as .magnet files
        </label>
      </div>
    ),
  },
  sabnzbd: {
    hostMode: "url",
    decode: (u) => ({ category: u?.sabnzbd?.category ?? "" }),
    encode: (s) => ({ sabnzbd: { category: s.category || undefined } }),
    Fields: CategoryFields,
  },
  nzbget: {
    hostMode: "url",
    decode: (u) => ({ category: u?.nzbget?.category ?? "" }),
    encode: (s) => ({ nzbget: { category: s.category || undefined } }),
    Fields: CategoryFields,
  },
  qui: {
    hostMode: "app",
    decode: (u) => ({
      instanceId: u?.qui?.instanceId ? String(u.qui.instanceId) : "",
      category: u?.qui?.category ?? "",
      tags: joinTags(u?.qui?.tags),
      startPaused: u?.qui?.startPaused ?? false,
    }),
    encode: (s) => ({ qui: { instanceId: Number(s.instanceId) || 0, category: s.category || undefined, tags: splitTags(s.tags), startPaused: s.startPaused || undefined } }),
    Fields: ({ s, set, children }) => (
      <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
        {children}
        <div className="grid grid-cols-2 gap-3">
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-category">Category <span className="text-faint">(optional)</span></Label>
            <Input id="dlc-category" value={s.category} onChange={(e) => set({ ...s, category: e.target.value })} />
          </span>
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-tags">Tags <span className="text-faint">(comma-separated, optional)</span></Label>
            <Input id="dlc-tags" value={s.tags} onChange={(e) => set({ ...s, tags: e.target.value })} />
          </span>
        </div>
        <label className="flex items-center gap-2 text-[13px]">
          <Switch checked={s.startPaused} onCheckedChange={(v) => set({ ...s, startPaused: v })} />
          Start paused
        </label>
      </div>
    ),
  },
  flood: {
    hostMode: "url",
    decode: (u) => ({
      destination: u?.flood?.destination ?? "",
      tags: joinTags(u?.flood?.tags),
      startPaused: u?.flood?.startPaused ?? false,
    }),
    encode: (s) => ({ flood: { destination: s.destination || undefined, tags: splitTags(s.tags), startPaused: s.startPaused || undefined } }),
    Fields: ({ s, set }) => (
      <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
        <div className="grid grid-cols-2 gap-3">
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-destination">Destination <span className="text-faint">(optional)</span></Label>
            <Input id="dlc-destination" value={s.destination} onChange={(e) => set({ ...s, destination: e.target.value })} />
          </span>
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-tags">Tags <span className="text-faint">(comma-separated, optional)</span></Label>
            <Input id="dlc-tags" value={s.tags} onChange={(e) => set({ ...s, tags: e.target.value })} />
          </span>
        </div>
        <label className="flex items-center gap-2 text-[13px]">
          <Switch checked={s.startPaused} onCheckedChange={(v) => set({ ...s, startPaused: v })} />
          Start paused
        </label>
      </div>
    ),
  },
  "download-station": {
    hostMode: "url",
    decode: (u) => ({ directory: u?.downloadStation?.directory ?? "" }),
    encode: (s) => ({ downloadStation: { directory: s.directory || undefined } }),
    Fields: ({ s, set }) => (
      <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="dlc-directory">Directory <span className="text-faint">(optional, relative to a shared folder)</span></Label>
          <Input id="dlc-directory" value={s.directory} onChange={(e) => set({ ...s, directory: e.target.value })} />
        </span>
      </div>
    ),
  },
  transmission: {
    hostMode: "url",
    decode: (u) => ({
      downloadDir: u?.transmission?.downloadDir ?? "",
      startPaused: u?.transmission?.startPaused ?? false,
    }),
    encode: (s) => ({ transmission: { downloadDir: s.downloadDir || undefined, startPaused: s.startPaused || undefined } }),
    Fields: ({ s, set }) => (
      <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="dlc-transmission-dir">Download directory <span className="text-faint">(optional)</span></Label>
          <Input id="dlc-transmission-dir" value={s.downloadDir} onChange={(e) => set({ ...s, downloadDir: e.target.value })} />
        </span>
        <label className="flex items-center gap-2 text-[13px]">
          <Switch checked={s.startPaused} onCheckedChange={(v) => set({ ...s, startPaused: v })} />
          Start paused
        </label>
      </div>
    ),
  },
  deluge: {
    hostMode: "hostport",
    decode: (u) => ({
      v1: u?.deluge?.v1 ?? false,
      label: u?.deluge?.label ?? "",
      downloadDir: u?.deluge?.downloadDir ?? "",
      startPaused: u?.deluge?.startPaused ?? false,
    }),
    encode: (s) => ({ deluge: {
      v1: s.v1 || undefined,
      label: s.label || undefined,
      downloadDir: s.downloadDir || undefined,
      startPaused: s.startPaused || undefined,
    } }),
    Fields: ({ s, set }) => (
      <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
        <div className="grid grid-cols-2 gap-3">
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-deluge-label">Label <span className="text-faint">(optional)</span></Label>
            <Input id="dlc-deluge-label" value={s.label} onChange={(e) => set({ ...s, label: e.target.value })} />
          </span>
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-deluge-dir">Download directory <span className="text-faint">(optional)</span></Label>
            <Input id="dlc-deluge-dir" value={s.downloadDir} onChange={(e) => set({ ...s, downloadDir: e.target.value })} />
          </span>
        </div>
        <label className="flex items-center gap-2 text-[13px]">
          <Switch checked={s.v1} onCheckedChange={(v) => set({ ...s, v1: v })} />
          Deluge 1.3 daemon <span className="text-faint">(default is the v2 daemon)</span>
        </label>
        <label className="flex items-center gap-2 text-[13px]">
          <Switch checked={s.startPaused} onCheckedChange={(v) => set({ ...s, startPaused: v })} />
          Start paused
        </label>
      </div>
    ),
  },
  rtorrent: {
    hostMode: "url",
    decode: (u) => ({
      label: u?.rtorrent?.label ?? "",
      directory: u?.rtorrent?.directory ?? "",
      startPaused: u?.rtorrent?.startPaused ?? false,
      tlsSkipVerify: u?.rtorrent?.tlsSkipVerify ?? false,
    }),
    encode: (s) => ({ rtorrent: {
      label: s.label || undefined,
      directory: s.directory || undefined,
      startPaused: s.startPaused || undefined,
      tlsSkipVerify: s.tlsSkipVerify || undefined,
    } }),
    Fields: ({ s, set }) => (
      <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
        <div className="grid grid-cols-2 gap-3">
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-rtorrent-label">Label <span className="text-faint">(optional)</span></Label>
            <Input id="dlc-rtorrent-label" value={s.label} onChange={(e) => set({ ...s, label: e.target.value })} />
          </span>
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-rtorrent-dir">Directory <span className="text-faint">(optional)</span></Label>
            <Input id="dlc-rtorrent-dir" value={s.directory} onChange={(e) => set({ ...s, directory: e.target.value })} />
          </span>
        </div>
        <label className="flex items-center gap-2 text-[13px]">
          <Switch checked={s.startPaused} onCheckedChange={(v) => set({ ...s, startPaused: v })} />
          Start paused
        </label>
        <label className="flex items-center gap-2 text-[13px]">
          <Switch checked={s.tlsSkipVerify} onCheckedChange={(v) => set({ ...s, tlsSkipVerify: v })} />
          Skip TLS certificate verification
        </label>
      </div>
    ),
  },
} satisfies { [K in DownloadClientKind]: KindSpec<K> }
