import { useEffect, useRef, useState } from "react"
import { Pencil, Plus, Trash2 } from "lucide-react"
import { ManagedByAppHint } from "@/components/applications/ManagedByAppHint"
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
import { useApps, useQuiInstances } from "@/hooks/useApps"
import { hostname } from "@/lib/format"
import { notifyError, notifySuccess } from "@/lib/notify"
import type { CreateDownloadClient, DownloadClient, DownloadClientKind, DownloadClientSettings, UpdateDownloadClient } from "@/lib/api"

// Only kinds with a registered driver work today (autobrr/harbrr#240, #241,
// #242, #244); the rest are seeded server-side but rejected on create until
// their own driver lands (autobrr/harbrr#8). Keep the picker limited to what
// actually works.
const DOWNLOAD_CLIENT_KINDS: DownloadClientKind[] = ["qbittorrent", "blackhole", "sabnzbd", "nzbget", "qui", "flood", "download-station"]

// Sentinel select value for "no existing qui App picked, use the inline host/API key
// fields below" — the create-time fallback for the very first qui app.
const NEW_APP = "new"

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
                  // Identity/credential (host, username, secret) are App-level now
                  // (ADR 0004) — the update surface is name + settings only.
                  const patch: UpdateDownloadClient = { name: body.name, settings: body.settings }
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
  // Create-only: which qui App backs this client. NEW_APP reveals the inline host/API
  // key fields below (the fallback for the very first qui app); anything else reuses
  // that App's identity and drives the instance dropdown instead of a typed id.
  const [appSel, setAppSel] = useState<string>(NEW_APP)
  const [host, setHost] = useState(client?.host ?? "")
  const [username, setUsername] = useState(client?.username ?? "")
  const [secret, setSecret] = useState("")
  // category/tags/startPaused are shared across kinds with identical concepts;
  // destination/directory/instanceId/tlsSkipVerify are single-kind.
  const [category, setCategory] = useState(
    client?.settings.qbittorrent?.category ?? client?.settings.qui?.category ?? client?.settings.sabnzbd?.category ?? client?.settings.nzbget?.category ?? ""
  )
  const [tags, setTags] = useState((client?.settings.qbittorrent?.tags ?? client?.settings.qui?.tags ?? client?.settings.flood?.tags ?? []).join(", "))
  const [startPaused, setStartPaused] = useState(
    client?.settings.qbittorrent?.startPaused ?? client?.settings.qui?.startPaused ?? client?.settings.flood?.startPaused ?? false
  )
  const [tlsSkipVerify, setTlsSkipVerify] = useState(client?.settings.qbittorrent?.tlsSkipVerify ?? false)
  const [instanceId, setInstanceId] = useState(client?.settings.qui?.instanceId ? String(client.settings.qui.instanceId) : "")
  const [destination, setDestination] = useState(client?.settings.flood?.destination ?? "")
  const [directory, setDirectory] = useState(client?.settings.downloadStation?.directory ?? "")
  const [torrentDir, setTorrentDir] = useState(client?.settings.blackhole?.torrentDir ?? "")
  const [nzbDir, setNzbDir] = useState(client?.settings.blackhole?.nzbDir ?? "")
  const [saveMagnetFiles, setSaveMagnetFiles] = useState(client?.settings.blackhole?.saveMagnetFiles ?? false)

  const apps = useApps()
  const quiApps = (apps.data ?? []).filter((a) => a.kind === "qui")
  // One-click: when exactly one qui App already exists, default straight to it instead
  // of making the operator pick from a list of one. Fires once (autoSelectedRef) so a
  // later manual switch back to "New app…" isn't overridden.
  const autoSelectedRef = useRef(false)
  useEffect(() => {
    if (isEdit || kind !== "qui" || autoSelectedRef.current) return
    if (quiApps.length === 1) {
      setAppSel(String(quiApps[0].id))
      autoSelectedRef.current = true
    }
  }, [isEdit, kind, quiApps])

  const usingQuiApp = kind === "qui" && !isEdit && appSel !== NEW_APP
  const quiInstances = useQuiInstances(usingQuiApp ? Number(appSel) : null)
  // Edit never touches identity (host/instance are fixed or App-level now); create
  // needs a watch folder (blackhole), a picked instance (qui via an App), or a host.
  const identityValid = kind === "blackhole"? torrentDir !== "" || nzbDir !== "": isEdit || (usingQuiApp ? instanceId !== "" : host !== "")

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        const tagList = tags ? tags.split(",").map((t) => t.trim()).filter(Boolean) : undefined
        let settings: DownloadClientSettings = {}
        if (kind === "qbittorrent") {
          settings = { qbittorrent: { category: category || undefined, tags: tagList, startPaused: startPaused || undefined, tlsSkipVerify: tlsSkipVerify || undefined } }
        } else if (kind === "blackhole") {
          settings = { blackhole: { torrentDir: torrentDir || undefined, nzbDir: nzbDir || undefined, saveMagnetFiles: saveMagnetFiles || undefined } }
        } else if (kind === "sabnzbd") {
          settings = { sabnzbd: { category: category || undefined } }
        } else if (kind === "nzbget") {
          settings = { nzbget: { category: category || undefined } }
        } else if (kind === "qui") {
          settings = { qui: { instanceId: Number(instanceId) || 0, category: category || undefined, tags: tagList, startPaused: startPaused || undefined } }
        } else if (kind === "flood") {
          settings = { flood: { destination: destination || undefined, tags: tagList, startPaused: startPaused || undefined } }
        } else if (kind === "download-station") {
          settings = { downloadStation: { directory: directory || undefined } }
        }
        // On edit, an empty secret keeps the stored one (only a typed value rotates).
        // blackhole has no network endpoint of its own — its host must always be empty.
        // Picking an existing qui App reuses its identity — no host/username/secret.
        const identity = usingQuiApp? { appId: Number(appSel) }: { host: kind === "blackhole" ? "" : host, username: kind === "qui" ? "" : username, secret: isEdit ? (secret || undefined) : secret }
        onSubmit(client?.id ?? null, { name, kind, settings, ...identity })
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
          <NativeSelect
            id="dlc-kind"
            value={kind}
            disabled={isEdit}
            onChange={(e) => {
              setKind(e.target.value as DownloadClientKind)
              setAppSel(NEW_APP) // the app list for the new kind is different; re-pick.
            }}
          >
            {DOWNLOAD_CLIENT_KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
          </NativeSelect>
        </span>
      </div>
      {kind === "qui" && !isEdit && (
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="dlc-qui-app">qui app</Label>
          <NativeSelect id="dlc-qui-app" value={appSel} onChange={(e) => { setAppSel(e.target.value); setInstanceId("") }}>
            <option value={NEW_APP}>New app…</option>
            {quiApps.map((a) => <option key={a.id} value={a.id}>{a.name} ({hostname(a.baseUrl)})</option>)}
          </NativeSelect>
        </span>
      )}
      {isEdit && kind !== "blackhole" && <ManagedByAppHint appId={client?.appId} />}
      {!isEdit && kind !== "blackhole" && !usingQuiApp && (
        <>
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-host">Host</Label>
            <Input id="dlc-host" placeholder="http://localhost:8080" value={host} onChange={(e) => setHost(e.target.value)} />
          </span>
          <div className="grid grid-cols-2 gap-3">
            {kind !== "qui" && kind !== "sabnzbd" && (
              <span className="flex flex-col gap-1.5">
                <Label htmlFor="dlc-username">Username <span className="text-faint">(optional)</span></Label>
                <Input id="dlc-username" autoComplete="off" value={username} onChange={(e) => setUsername(e.target.value)} />
              </span>
            )}
            <span className={`flex flex-col gap-1.5 ${kind === "qui" || kind === "sabnzbd" ? "col-span-2" : ""}`}>
              <Label htmlFor="dlc-secret">{kind === "qui" || kind === "sabnzbd" ? "API key" : "Password"}</Label>
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
      {(kind === "sabnzbd" || kind === "nzbget") && (
        <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-category">Category <span className="text-faint">(optional)</span></Label>
            <Input id="dlc-category" value={category} onChange={(e) => setCategory(e.target.value)} />
          </span>
        </div>
      )}
      {(kind === "qbittorrent" || kind === "qui") && (
        <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
          {kind === "qui" && usingQuiApp && (
            <span className="flex flex-col gap-1.5">
              <Label htmlFor="dlc-instance-select">Instance</Label>
              <NativeSelect
                id="dlc-instance-select"
                value={instanceId}
                onChange={(e) => {
                  setInstanceId(e.target.value)
                  const picked = quiInstances.data?.instances?.find((i) => String(i.id) === e.target.value)
                  if (picked) setName(picked.name)
                }}
              >
                <option value="">Select an instance…</option>
                {(quiInstances.data?.instances ?? []).map((i) => <option key={i.id} value={i.id}>{i.name}</option>)}
              </NativeSelect>
              {quiInstances.data && !quiInstances.data.ok && (
                <p className="text-[12px] text-bad">{quiInstances.data.error ?? "Couldn't reach qui"}</p>
              )}
            </span>
          )}
          {kind === "qui" && !usingQuiApp && (
            <span className="flex flex-col gap-1.5">
              <Label htmlFor="dlc-instance-id">Instance ID</Label>
              <Input id="dlc-instance-id" type="number" min={1} value={instanceId} onChange={(e) => setInstanceId(e.target.value)} />
            </span>
          )}
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
          {kind === "qbittorrent" && (
            <label className="flex items-center gap-2 text-[13px]">
              <Switch checked={tlsSkipVerify} onCheckedChange={setTlsSkipVerify} />
              Skip TLS certificate verification
            </label>
          )}
        </div>
      )}
      {kind === "flood" && (
        <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
          <div className="grid grid-cols-2 gap-3">
            <span className="flex flex-col gap-1.5">
              <Label htmlFor="dlc-destination">Destination <span className="text-faint">(optional)</span></Label>
              <Input id="dlc-destination" value={destination} onChange={(e) => setDestination(e.target.value)} />
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
        </div>
      )}
      {kind === "download-station" && (
        <div className="flex flex-col gap-3 rounded-lg border border-border/60 p-3">
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-directory">Directory <span className="text-faint">(optional, relative to a shared folder)</span></Label>
            <Input id="dlc-directory" value={directory} onChange={(e) => setDirectory(e.target.value)} />
          </span>
        </div>
      )}
      <DialogFooter>
        <Button type="submit" disabled={pending || !name || !identityValid}>
          {pending ? "Saving…" : isEdit ? "Save changes" : "Add download client"}
        </Button>
      </DialogFooter>
    </form>
  )
}
