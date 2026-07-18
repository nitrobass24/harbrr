import { useState } from "react"
import { Pencil, Plus, Trash2 } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { NativeSelect } from "@/components/ui/native-select"
import { Switch } from "@/components/ui/switch"
import {
  useCreateDownloadClient,
  useDeleteDownloadClient,
  useDownloadClients,
  useSetDownloadClientEnabled,
  useTestDownloadClient,
  useUpdateDownloadClient
} from "@/hooks/useDownloadClients"
import { notifyError, notifySuccess } from "@/lib/notify"
import type { CreateDownloadClient, DownloadClient, DownloadClientKind, DownloadClientSettings, UpdateDownloadClient } from "@/lib/api"

// Only kinds with a registered driver work today (autobrr/harbrr#240, #243,
// #244); the rest are seeded server-side but rejected on create until their own
// driver lands (autobrr/harbrr#8). Keep the picker limited to what actually works.
const DOWNLOAD_CLIENT_KINDS: DownloadClientKind[] = ["qbittorrent", "blackhole", "transmission", "deluge", "rtorrent"]

// HOST_PLACEHOLDER is per-kind: every kind but Deluge takes an absolute http(s)
// URL; Deluge's daemon RPC is a raw "host:port" socket address, not a URL.
const HOST_PLACEHOLDER: Partial<Record<DownloadClientKind, string>> = {
  deluge: "localhost:58846",
}

// `null` = closed; `{ client: null }` = add; `{ client }` = edit that client.
type Editing = { client: DownloadClient | null } | null

// The form always produces a full CreateDownloadClient shape; kind is immutable
// on edit so an update just drops it before the PATCH goes out (kind isn't even
// a field UpdateDownloadClient accepts).
type FormBody = CreateDownloadClient

// Configured download clients harbrr can hand a grabbed release to. Host/username
// are plain (visible on read); only the secret (password/API key, depending on
// kind) is stored encrypted and rotates only when a new one is typed.
export function DownloadClientsSection() {
  const clients = useDownloadClients()
  const create = useCreateDownloadClient()
  const update = useUpdateDownloadClient()
  const remove = useDeleteDownloadClient()
  const toggle = useSetDownloadClientEnabled()
  const test = useTestDownloadClient()
  const [editing, setEditing] = useState<Editing>(null)

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <h2 className="text-[14px] font-semibold tracking-tight">Download clients</h2>
        <Button variant="outline" size="sm" className="ml-auto" onClick={() => setEditing({ client: null })}>
          <Plus className="h-3.5 w-3.5" /> Add download client
        </Button>
      </div>

      <div className="flex flex-col rounded-xl border border-border bg-card px-5 py-2 text-[13px]">
        {(clients.data ?? []).map((c) => (
          <div key={c.id} className="flex items-center gap-3 border-b border-border/60 py-2.5 last:border-b-0">
            <span className="font-medium">{c.name}</span>
            <Badge variant="secondary" className="px-1.5 py-0 text-[11px]">{c.kind}</Badge>
            <span className="text-muted-foreground">{c.host}</span>
            <span className="ml-auto flex items-center gap-1">
              <Switch
                aria-label={`${c.enabled ? "Disable" : "Enable"} ${c.name}`}
                checked={c.enabled}
                onCheckedChange={(checked) => toggle.mutate({ id: c.id, enabled: checked })}
              />
              <Button
                variant="outline"
                size="sm"
                disabled={test.isPending && test.variables === c.id}
                onClick={() => test.mutate(c.id, {
                  onSuccess: (r) => r.ok ? notifySuccess("Connection OK") : notifyError(`Test failed — ${r.error ?? "unknown error"}`),
                  onError: (err) => notifyError("Test request failed", err),
                })}
              >
                Test
              </Button>
              <Button variant="ghost" size="icon" aria-label={`Edit ${c.name}`} onClick={() => setEditing({ client: c })}>
                <Pencil className="h-4 w-4" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label={`Delete ${c.name}`}
                onClick={() => remove.mutate(c.id, {
                  onSuccess: () => notifySuccess(`${c.name} deleted`),
                  onError: (err) => notifyError(`Deleting ${c.name} failed`, err),
                })}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </span>
          </div>
        ))}
        {clients.data?.length === 0 && (
          <p className="py-3 text-muted-foreground">No download clients. Add one to hand off grabbed releases.</p>
        )}
      </div>

      <Dialog open={editing !== null} onOpenChange={(open) => { if (!open) setEditing(null) }}>
        {editing !== null && (
          <DialogContent>
            <DownloadClientForm
              // Remount (fresh state seeded from props) per target.
              key={editing.client?.id ?? "new"}
              client={editing.client}
              pending={create.isPending || update.isPending}
              onSubmit={(id, body) => {
                const done = { onSuccess: () => setEditing(null), onError: (err: Error) => notifyError(`Save failed: ${err.message}`, err) }
                if (id === null) create.mutate(body, done)
                else {
                  const patch: UpdateDownloadClient = { name: body.name, host: body.host, username: body.username, secret: body.secret, settings: body.settings }
                  update.mutate({ id, body: patch }, done)
                }
              }}
            />
          </DialogContent>
        )}
      </Dialog>
    </section>
  )
}

function DownloadClientForm({ client, pending, onSubmit }: {
  client: DownloadClient | null
  pending: boolean
  onSubmit: (id: number | null, body: FormBody) => void
}) {
  const isEdit = client !== null
  const [name, setName] = useState(client?.name ?? "")
  const [kind, setKind] = useState<DownloadClientKind>(client?.kind ?? "qbittorrent")
  const [host, setHost] = useState(client?.host ?? "")
  const [username, setUsername] = useState(client?.username ?? "")
  const [secret, setSecret] = useState("")
  const [category, setCategory] = useState(client?.settings.qbittorrent?.category ?? "")
  const [tags, setTags] = useState((client?.settings.qbittorrent?.tags ?? []).join(", "))
  const [startPaused, setStartPaused] = useState(client?.settings.qbittorrent?.startPaused ?? false)
  const [tlsSkipVerify, setTlsSkipVerify] = useState(client?.settings.qbittorrent?.tlsSkipVerify ?? false)
  const [torrentDir, setTorrentDir] = useState(client?.settings.blackhole?.torrentDir ?? "")
  const [nzbDir, setNzbDir] = useState(client?.settings.blackhole?.nzbDir ?? "")
  const [saveMagnetFiles, setSaveMagnetFiles] = useState(client?.settings.blackhole?.saveMagnetFiles ?? false)

  const [transmissionDownloadDir, setTransmissionDownloadDir] = useState(client?.settings.transmission?.downloadDir ?? "")
  const [transmissionStartPaused, setTransmissionStartPaused] = useState(client?.settings.transmission?.startPaused ?? false)

  const [delugeV1, setDelugeV1] = useState(client?.settings.deluge?.v1 ?? false)
  const [delugeLabel, setDelugeLabel] = useState(client?.settings.deluge?.label ?? "")
  const [delugeDownloadDir, setDelugeDownloadDir] = useState(client?.settings.deluge?.downloadDir ?? "")
  const [delugeStartPaused, setDelugeStartPaused] = useState(client?.settings.deluge?.startPaused ?? false)

  const [rtorrentLabel, setRtorrentLabel] = useState(client?.settings.rtorrent?.label ?? "")
  const [rtorrentDirectory, setRtorrentDirectory] = useState(client?.settings.rtorrent?.directory ?? "")
  const [rtorrentStartPaused, setRtorrentStartPaused] = useState(client?.settings.rtorrent?.startPaused ?? false)
  const [rtorrentTlsSkipVerify, setRtorrentTlsSkipVerify] = useState(client?.settings.rtorrent?.tlsSkipVerify ?? false)

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        let settings: DownloadClientSettings = {}
        if (kind === "qbittorrent") {
          settings = { qbittorrent: {
            category: category || undefined,
            tags: tags ? tags.split(",").map((t) => t.trim()).filter(Boolean) : undefined,
            startPaused: startPaused || undefined,
            tlsSkipVerify: tlsSkipVerify || undefined,
          } }
        } else if (kind === "blackhole") {
          settings = { blackhole: {
            torrentDir: torrentDir || undefined,
            nzbDir: nzbDir || undefined,
            saveMagnetFiles: saveMagnetFiles || undefined,
          } }
        } else if (kind === "transmission") {
          settings = { transmission: { downloadDir: transmissionDownloadDir || undefined, startPaused: transmissionStartPaused || undefined } }
        } else if (kind === "deluge") {
          settings = { deluge: {
            v1: delugeV1 || undefined,
            label: delugeLabel || undefined,
            downloadDir: delugeDownloadDir || undefined,
            startPaused: delugeStartPaused || undefined,
          } }
        } else if (kind === "rtorrent") {
          settings = { rtorrent: {
            label: rtorrentLabel || undefined,
            directory: rtorrentDirectory || undefined,
            startPaused: rtorrentStartPaused || undefined,
            tlsSkipVerify: rtorrentTlsSkipVerify || undefined,
          } }
        }
        // On edit, an empty secret keeps the stored one (only a typed value rotates).
        // blackhole has no network endpoint of its own — its host must always be empty.
        onSubmit(client?.id ?? null, {
          name, kind, host: kind === "blackhole" ? "" : host, username, settings,
          secret: isEdit ? (secret || undefined) : secret,
        })
      }}
    >
      <DialogHeader>
        <DialogTitle>{isEdit ? "Edit download client" : "Add download client"}</DialogTitle>
        <DialogDescription>Host and username are visible; the secret is stored encrypted and never shown again.</DialogDescription>
      </DialogHeader>
      <div className="grid grid-cols-2 gap-3">
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="dlc-name">Name</Label>
          <Input id="dlc-name" value={name} onChange={(e) => setName(e.target.value)} />
        </span>
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="dlc-kind">Kind</Label>
          <NativeSelect id="dlc-kind" value={kind} disabled={isEdit} onChange={(e) => setKind(e.target.value as DownloadClientKind)}>
            {DOWNLOAD_CLIENT_KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
          </NativeSelect>
        </span>
      </div>
      {kind !== "blackhole" && (
        <>
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-host">Host</Label>
            <Input id="dlc-host" placeholder={HOST_PLACEHOLDER[kind] ?? "http://localhost:8080"} value={host} onChange={(e) => setHost(e.target.value)} />
          </span>
          <div className="grid grid-cols-2 gap-3">
            <span className="flex flex-col gap-1.5">
              <Label htmlFor="dlc-username">Username <span className="text-faint">(optional)</span></Label>
              <Input id="dlc-username" autoComplete="off" value={username} onChange={(e) => setUsername(e.target.value)} />
            </span>
            <span className="flex flex-col gap-1.5">
              <Label htmlFor="dlc-secret">Password {isEdit && <span className="text-faint">(leave blank to keep)</span>}</Label>
              <Input id="dlc-secret" type="password" autoComplete="off" value={secret} onChange={(e) => setSecret(e.target.value)} />
            </span>
          </div>
        </>
      )}
      {kind === "blackhole" && (
        <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
          <div className="grid grid-cols-2 gap-3">
            <span className="flex flex-col gap-1.5">
              <Label htmlFor="dlc-torrent-dir">Torrent watch folder <span className="text-faint">(optional)</span></Label>
              <Input id="dlc-torrent-dir" placeholder="/watch/torrents" value={torrentDir} onChange={(e) => setTorrentDir(e.target.value)} />
            </span>
            <span className="flex flex-col gap-1.5">
              <Label htmlFor="dlc-nzb-dir">NZB watch folder <span className="text-faint">(optional)</span></Label>
              <Input id="dlc-nzb-dir" placeholder="/watch/nzbs" value={nzbDir} onChange={(e) => setNzbDir(e.target.value)} />
            </span>
          </div>
          <label className="flex items-center gap-2 text-[13px]">
            <Switch checked={saveMagnetFiles} onCheckedChange={setSaveMagnetFiles} />
            Save magnet-only releases as .magnet files
          </label>
        </div>
      )}
      {kind === "qbittorrent" && (
        <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
          <div className="grid grid-cols-2 gap-3">
            <span className="flex flex-col gap-1.5">
              <Label htmlFor="dlc-category">Category <span className="text-faint">(optional)</span></Label>
              <Input id="dlc-category" value={category} onChange={(e) => setCategory(e.target.value)} />
            </span>
            <span className="flex flex-col gap-1.5">
              <Label htmlFor="dlc-tags">Tags <span className="text-faint">(comma-separated, optional)</span></Label>
              <Input id="dlc-tags" value={tags} onChange={(e) => setTags(e.target.value)} />
            </span>
          </div>
          <label className="flex items-center gap-2 text-[13px]">
            <Switch checked={startPaused} onCheckedChange={setStartPaused} />
            Start paused
          </label>
          <label className="flex items-center gap-2 text-[13px]">
            <Switch checked={tlsSkipVerify} onCheckedChange={setTlsSkipVerify} />
            Skip TLS certificate verification
          </label>
        </div>
      )}
      {kind === "transmission" && (
        <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-transmission-dir">Download directory <span className="text-faint">(optional)</span></Label>
            <Input id="dlc-transmission-dir" value={transmissionDownloadDir} onChange={(e) => setTransmissionDownloadDir(e.target.value)} />
          </span>
          <label className="flex items-center gap-2 text-[13px]">
            <Switch checked={transmissionStartPaused} onCheckedChange={setTransmissionStartPaused} />
            Start paused
          </label>
        </div>
      )}
      {kind === "deluge" && (
        <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
          <div className="grid grid-cols-2 gap-3">
            <span className="flex flex-col gap-1.5">
              <Label htmlFor="dlc-deluge-label">Label <span className="text-faint">(optional)</span></Label>
              <Input id="dlc-deluge-label" value={delugeLabel} onChange={(e) => setDelugeLabel(e.target.value)} />
            </span>
            <span className="flex flex-col gap-1.5">
              <Label htmlFor="dlc-deluge-dir">Download directory <span className="text-faint">(optional)</span></Label>
              <Input id="dlc-deluge-dir" value={delugeDownloadDir} onChange={(e) => setDelugeDownloadDir(e.target.value)} />
            </span>
          </div>
          <label className="flex items-center gap-2 text-[13px]">
            <Switch checked={delugeV1} onCheckedChange={setDelugeV1} />
            Deluge 1.3 daemon <span className="text-faint">(default is the v2 daemon)</span>
          </label>
          <label className="flex items-center gap-2 text-[13px]">
            <Switch checked={delugeStartPaused} onCheckedChange={setDelugeStartPaused} />
            Start paused
          </label>
        </div>
      )}
      {kind === "rtorrent" && (
        <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
          <div className="grid grid-cols-2 gap-3">
            <span className="flex flex-col gap-1.5">
              <Label htmlFor="dlc-rtorrent-label">Label <span className="text-faint">(optional)</span></Label>
              <Input id="dlc-rtorrent-label" value={rtorrentLabel} onChange={(e) => setRtorrentLabel(e.target.value)} />
            </span>
            <span className="flex flex-col gap-1.5">
              <Label htmlFor="dlc-rtorrent-dir">Directory <span className="text-faint">(optional)</span></Label>
              <Input id="dlc-rtorrent-dir" value={rtorrentDirectory} onChange={(e) => setRtorrentDirectory(e.target.value)} />
            </span>
          </div>
          <label className="flex items-center gap-2 text-[13px]">
            <Switch checked={rtorrentStartPaused} onCheckedChange={setRtorrentStartPaused} />
            Start paused
          </label>
          <label className="flex items-center gap-2 text-[13px]">
            <Switch checked={rtorrentTlsSkipVerify} onCheckedChange={setRtorrentTlsSkipVerify} />
            Skip TLS certificate verification
          </label>
        </div>
      )}
      <DialogFooter>
        <Button type="submit" disabled={pending || !name || (kind !== "blackhole" ? !host : !torrentDir && !nzbDir)}>
          {pending ? "Saving…" : isEdit ? "Save changes" : "Add download client"}
        </Button>
      </DialogFooter>
    </form>
  )
}
