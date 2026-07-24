import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { ChevronDown, Pencil, Trash2 } from "lucide-react"
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu"
import { HostPortFields } from "@/components/forms/HostPortFields"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { useApps, useDeleteApp, useUpdateApp } from "@/hooks/useApps"
import { hostname } from "@/lib/format"
import { composeHostURL, decomposeHostURL } from "@/lib/hosturl"
import { notifySuccess } from "@/lib/notify"
import type { App, UpdateApp } from "@/lib/api"

// Which surfaces' create dialog knows how to reuse an App of a given kind — mirrors
// each dialog's own hardcoded compatible-kind set (ConnectionDialog's KINDS,
// AnnounceSection's qui/crossseed-v6, DownloadClientsSection's qui-only reuse path).
// Deliberately NOT hoisted into a shared surface→kinds mapping (autobrr/harbrr#304's
// tripwire) — these three literals are this file's own copy, same as the dialogs'.
const SYNC_KINDS = ["sonarr", "radarr", "lidarr", "readarr", "whisparr", "qui"]
const ANNOUNCE_KINDS = ["qui", "crossseed-v6"]
const DOWNLOAD_KINDS = ["qui"]

// Apps are the first-class (kind, base URL) identities behind app-sync, announce, and
// download-client surfaces (ADR 0004): one stored credential, referenced by id instead
// of re-entered per surface. Editing one (especially rotating its credential) updates
// every surface that references it; deleting one is blocked (409) while any surface
// still does.
export function AppsSection() {
  const apps = useApps()
  const update = useUpdateApp()
  const remove = useDeleteApp()
  const [editing, setEditing] = useState<App | null>(null)

  return (
    <section id="apps-section" className="flex flex-col gap-3">
      <div className="flex flex-col">
        <h2 className="text-[14px] font-semibold tracking-tight">Apps</h2>
        <p className="text-[12px] text-faint">
          One identity + credential per (kind, base URL), shared across every surface that connects to it.
        </p>
      </div>

      <div className="flex flex-col rounded-xl border border-border bg-card px-5 py-2 text-[13px]">
        {(apps.data ?? []).map((a) => {
          const used = a.references.appConnections + a.references.announce + a.references.download
          return (
            <div key={a.id} className="flex items-center gap-3 border-b border-border/60 py-2.5 last:border-b-0">
              <span className="flex flex-col">
                <span className="flex items-center gap-2 font-medium">
                  {a.name}
                  <Badge variant="secondary" className="px-1.5 py-0 text-[11px]">{a.kind}</Badge>
                </span>
                <span className="text-[12px] text-faint">
                  {hostname(a.baseUrl)} · used by {used} surface{used === 1 ? "" : "s"}
                </span>
              </span>
              <span className="ml-auto flex items-center gap-1">
                <UseAsMenu app={a} />
                <Button variant="ghost" size="icon" aria-label={`Edit ${a.name}`} onClick={() => setEditing(a)}>
                  <Pencil className="h-4 w-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`Delete ${a.name}`}
                  onClick={() => remove.mutate(a.id, { onSuccess: () => notifySuccess(`${a.name} deleted`) })}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </span>
            </div>
          )
        })}
        {apps.data?.length === 0 && (
          <p className="py-3 text-muted-foreground">No apps yet — one is created the first time you connect a surface.</p>
        )}
      </div>

      <Dialog open={editing !== null} onOpenChange={(open) => { if (!open) setEditing(null) }}>
        {editing !== null && (
          <DialogContent>
            <AppForm
              // Remount (fresh state seeded from props) per target.
              key={editing.id}
              app={editing}
              pending={update.isPending}
              error={update.error}
              onSubmit={(body) => update.mutate({ id: editing.id, body }, {
                onSuccess: () => {
                  notifySuccess("App updated")
                  setEditing(null)
                },
              })}
            />
          </DialogContent>
        )}
      </Dialog>
    </section>
  )
}

// The app → surface direction of ADR 0004's "any surface is one click from any app"
// (autobrr/harbrr#300 — #296 already gave the create dialogs the surface → app
// direction). Each item opens that surface's existing create dialog with this App
// pre-picked via a search-param deep link — pure navigation, no new endpoints.
// Kind-incompatible or already-unique-per-App surfaces are left off the menu instead
// of shown disabled; download has no uniqueness rule (multiple clients per App are
// legal), so it's never filtered by `used`.
function UseAsMenu({ app }: { app: App }) {
  const navigate = useNavigate()
  const offerSync = SYNC_KINDS.includes(app.kind) && app.references.appConnections === 0
  const offerAnnounce = ANNOUNCE_KINDS.includes(app.kind) && app.references.announce === 0
  const offerDownload = DOWNLOAD_KINDS.includes(app.kind)

  if (!offerSync && !offerAnnounce && !offerDownload) return null

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" aria-label={`Use ${app.name} as…`}>
          Use as… <ChevronDown className="h-3.5 w-3.5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {offerSync && (
          <DropdownMenuItem onClick={() => void navigate({ to: "/applications", search: { create: "sync", appId: app.id } })}>
            Sync target
          </DropdownMenuItem>
        )}
        {offerAnnounce && (
          <DropdownMenuItem onClick={() => void navigate({ to: "/applications", search: { create: "announce", appId: app.id } })}>
            Announce target
          </DropdownMenuItem>
        )}
        {offerDownload && (
          <DropdownMenuItem onClick={() => void navigate({ to: "/download-clients", search: { appId: app.id } })}>
            Download client
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// AppForm rotates the credential and/or edits the App's identity fields. The credential
// is stored encrypted and never read back: the key field starts empty and is only sent
// when the operator types a replacement (omit = keep, per the API).
function AppForm({ app, pending, error, onSubmit }: {
  app: App
  pending: boolean
  error: unknown
  onSubmit: (body: UpdateApp) => void
}) {
  // Most Apps store an absolute http(s) URL, which splits cleanly into scheme/host/port
  // for editing. A Deluge App's identity is a bare "host:port" daemon address — not a
  // URL — so it falls back to the raw single field: guessing a scheme to force the
  // split would corrupt the App identity on save (dedup is exact string equality).
  const split = decomposeHostURL(app.baseUrl)
  const [name, setName] = useState(app.name)
  const [scheme, setScheme] = useState<"http" | "https">(split?.scheme ?? "http")
  const [host, setHost] = useState(split?.host ?? "")
  const [port, setPort] = useState(split?.port ?? "")
  const [rawBaseUrl, setRawBaseUrl] = useState(app.baseUrl)
  const [username, setUsername] = useState(app.username)
  const [harbrrUrl, setHarbrrUrl] = useState(app.harbrrUrl)
  const [apiKey, setApiKey] = useState("")
  const [enabled, setEnabled] = useState(app.enabled)
  const message = error instanceof Error ? error.message : null

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        onSubmit({
          name, username, harbrrUrl, enabled,
          baseUrl: split ? composeHostURL(scheme, host, port) : rawBaseUrl,
          ...(apiKey !== "" ? { apiKey } : {}), // omit = keep the stored credential
        })
      }}
    >
      <DialogHeader>
        <DialogTitle>Edit {app.name}</DialogTitle>
        <DialogDescription>
          Rotating the credential here propagates to every surface that references this app.
        </DialogDescription>
      </DialogHeader>

      {message && (
        <p className="rounded-md border border-bad/40 bg-bad/10 px-3 py-2 text-[13px] text-bad">{message}</p>
      )}

      <span className="flex flex-col gap-1.5">
        <Label htmlFor="app-name">Name</Label>
        <Input id="app-name" value={name} onChange={(e) => setName(e.target.value)} />
      </span>

      {split ? (
        <HostPortFields idPrefix="app" scheme={scheme} host={host} port={port} onScheme={setScheme} onHost={setHost} onPort={setPort} />
      ) : (
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="app-baseurl">Base URL</Label>
          <Input id="app-baseurl" value={rawBaseUrl} onChange={(e) => setRawBaseUrl(e.target.value)} />
        </span>
      )}

      <span className="flex flex-col gap-1.5">
        <Label htmlFor="app-username">Username <span className="text-faint">(only for user+password apps)</span></Label>
        <Input id="app-username" autoComplete="off" value={username} onChange={(e) => setUsername(e.target.value)} />
      </span>

      <span className="flex flex-col gap-1.5">
        <Label htmlFor="app-key">Credential (leave blank to keep the stored one)</Label>
        <Input id="app-key" type="password" autoComplete="off" value={apiKey} onChange={(e) => setApiKey(e.target.value)} />
      </span>

      <span className="flex flex-col gap-1.5">
        <Label htmlFor="app-harbrr">harbrr URL as the app reaches it</Label>
        <Input id="app-harbrr" value={harbrrUrl} onChange={(e) => setHarbrrUrl(e.target.value)} />
      </span>

      <label className="flex items-center gap-2 text-[13px]">
        <Switch checked={enabled} onCheckedChange={setEnabled} />
        Enabled
      </label>

      <DialogFooter>
        <Button type="submit" disabled={pending || name === "" || (split ? host === "" : rawBaseUrl === "")}>
          {pending ? "Saving…" : "Save changes"}
        </Button>
      </DialogFooter>
    </form>
  )
}
