import { useState } from "react"
import { Plus, Trash2 } from "lucide-react"
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
  useAnnounceConnections,
  useCreateAnnounce,
  useDeleteAnnounce,
  useSetAnnounceEnabled,
  useServerInfo
} from "@/hooks/useAppConnections"
import { defaultHarbrrUrl, explicitUrlPort } from "@/lib/base-url"
import { hostname } from "@/lib/format"
import type { AnnounceKind } from "@/lib/api"

// Cross-seed push targets: harbrr announces newly-seen releases to qui's
// cross-seed webhook or cross-seed v6's /api/announce. The API has no PATCH or
// test for these — editing is delete + recreate, stated inline.
export function AnnounceSection() {
  const targets = useAnnounceConnections()
  const create = useCreateAnnounce()
  const remove = useDeleteAnnounce()
  const toggle = useSetAnnounceEnabled()
  const serverInfo = useServerInfo()
  const [adding, setAdding] = useState(false)

  // Same stale-port advisory as ConnectionCard: only a harbrrUrl naming a port
  // outright is comparable to harbrr's listen port (a proxied URL has none).
  // Badge-only here — announce targets have no update API, so the remedy is
  // delete + re-add, per the section's standing note.
  const stalePort = (harbrrUrl?: string): boolean => {
    const livePort = serverInfo.data?.port
    if (livePort === undefined || harbrrUrl === undefined) return false
    const storedPort = explicitUrlPort(harbrrUrl)
    return storedPort !== null && storedPort !== livePort
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <div className="flex flex-col">
          <h2 className="text-[14px] font-semibold tracking-tight">Announce targets</h2>
          <p className="text-[12px] text-faint">
            New releases seen on polled feeds are pushed to cross-seed tools. No edit — delete and
            re-add to change one.
          </p>
        </div>
        <Button variant="outline" size="sm" className="ml-auto" onClick={() => setAdding(true)}>
          <Plus className="h-3.5 w-3.5" /> Add target
        </Button>
      </div>

      {(targets.data ?? []).map((t) => (
        <div key={t.id} className="flex items-center gap-4 rounded-xl border border-border bg-card px-5 py-3.5">
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <span className="flex items-center gap-2 text-[14px] font-medium">
              {t.name}
              <Badge variant="secondary" className="px-1.5 py-0 text-[11px]">{t.kind}</Badge>
              {stalePort(t.harbrrUrl) && (
                <Badge
                  variant="outline"
                  className="px-1.5 py-0 text-[11px] text-warn"
                  title={`This target's harbrr URL port doesn't match harbrr's configured port (${serverInfo.data?.port}). If it isn't a deliberate proxy/port mapping, delete and re-add the target.`}
                >
                  port may be outdated
                </Badge>
              )}
            </span>
            <span className="text-[12px] text-faint">{hostname(t.baseUrl)}</span>
          </div>
          <Switch
            aria-label={`${t.enabled ? "Disable" : "Enable"} ${t.name}`}
            checked={t.enabled}
            onCheckedChange={(checked) => toggle.mutate({ id: t.id, enabled: checked })}
          />
          <Button variant="ghost" size="icon" aria-label={`Delete ${t.name}`} onClick={() => remove.mutate(t.id)}>
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      ))}
      {targets.data?.length === 0 && (
        <p className="rounded-xl border border-dashed border-border px-5 py-6 text-center text-[13px] text-muted-foreground">
          No announce targets. cross-seed v6 users can also grab a per-indexer config snippet from
          the Indexers table&apos;s kebab menu.
        </p>
      )}

      <Dialog open={adding} onOpenChange={setAdding}>
        <DialogContent>
          <AddAnnounceForm
            pending={create.isPending}
            error={create.error}
            onSubmit={(body) => create.mutate(body, { onSuccess: () => setAdding(false) })}
          />
        </DialogContent>
      </Dialog>
    </section>
  )
}

function AddAnnounceForm({ pending, error, onSubmit }: {
  pending: boolean
  error: unknown
  onSubmit: (body: { name: string, kind: AnnounceKind, baseUrl: string, apiKey: string, harbrrUrl: string }) => void
}) {
  const [name, setName] = useState("")
  const [kind, setKind] = useState<AnnounceKind>("qui")
  const [baseUrl, setBaseUrl] = useState("")
  const [apiKey, setApiKey] = useState("")
  const [harbrrUrl, setHarbrrUrl] = useState(defaultHarbrrUrl())

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        onSubmit({ name, kind, baseUrl, apiKey, harbrrUrl })
      }}
    >
      <DialogHeader>
        <DialogTitle>Add announce target</DialogTitle>
        <DialogDescription>The tool&apos;s API key is stored encrypted and never shown again.</DialogDescription>
      </DialogHeader>
      {error instanceof Error && (
        <p className="rounded-md border border-bad/40 bg-bad/10 px-3 py-2 text-[13px] text-bad">{error.message}</p>
      )}
      <div className="grid grid-cols-2 gap-3">
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="ann-name">Name</Label>
          <Input id="ann-name" value={name} onChange={(e) => setName(e.target.value)} />
        </span>
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="ann-kind">Kind</Label>
          <NativeSelect id="ann-kind" value={kind} onChange={(e) => setKind(e.target.value as AnnounceKind)}>
            <option value="qui">qui</option>
            <option value="crossseed-v6">cross-seed v6</option>
          </NativeSelect>
        </span>
      </div>
      <span className="flex flex-col gap-1.5">
        <Label htmlFor="ann-base">Tool base URL</Label>
        <Input id="ann-base" placeholder="http://cross-seed:2468" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
      </span>
      <span className="flex flex-col gap-1.5">
        <Label htmlFor="ann-key">Tool API key</Label>
        <Input id="ann-key" type="password" autoComplete="off" value={apiKey} onChange={(e) => setApiKey(e.target.value)} />
      </span>
      <span className="flex flex-col gap-1.5">
        <Label htmlFor="ann-harbrr">harbrr URL as the tool reaches it</Label>
        <Input id="ann-harbrr" value={harbrrUrl} onChange={(e) => setHarbrrUrl(e.target.value)} />
      </span>
      <DialogFooter>
        <Button type="submit" disabled={pending || !name || !baseUrl || !apiKey || !harbrrUrl}>
          {pending ? "Adding…" : "Add target"}
        </Button>
      </DialogFooter>
    </form>
  )
}
