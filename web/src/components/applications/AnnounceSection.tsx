import { useState } from "react"
import { Pencil, Plus, Trash2 } from "lucide-react"
import { toast } from "sonner"
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
  useServerInfo,
  useSetAnnounceEnabled,
  useTestAnnounce,
  useUpdateAnnounce
} from "@/hooks/useAppConnections"
import { defaultHarbrrUrl, explicitUrlPort } from "@/lib/base-url"
import { hostname } from "@/lib/format"
import type { AnnounceConnection, AnnounceKind, CreateAnnounceConnection, UpdateAnnounceConnection } from "@/lib/api"

type DialogState =
  | { open: false }
  | { open: true, existing?: AnnounceConnection }

// Cross-seed push targets: harbrr announces newly-seen releases to qui's cross-seed
// webhook or cross-seed v6's /api/announce. Each target can be edited in place and
// tested (a non-mutating reachability probe — qui also validates the API key).
export function AnnounceSection() {
  const targets = useAnnounceConnections()
  const create = useCreateAnnounce()
  const update = useUpdateAnnounce()
  const remove = useDeleteAnnounce()
  const toggle = useSetAnnounceEnabled()
  const test = useTestAnnounce()
  const serverInfo = useServerInfo()
  const [dialog, setDialog] = useState<DialogState>({ open: false })

  const editing = dialog.open ? dialog.existing : undefined

  // Same stale-port advisory as ConnectionCard: only a harbrrUrl naming a port outright
  // is comparable to harbrr's listen port (a proxied URL has none). Badge-only — the
  // remedy is now an in-place edit of the target's harbrr URL.
  const stalePort = (harbrrUrl?: string): boolean => {
    const livePort = serverInfo.data?.port
    if (livePort === undefined || harbrrUrl === undefined) return false
    const storedPort = explicitUrlPort(harbrrUrl)
    return storedPort !== null && storedPort !== livePort
  }

  // A pass reports what was actually verified: qui's probe validates reachability AND the
  // API key; cross-seed v6 has no authed health endpoint, so it confirms reachability only.
  const runTest = (t: AnnounceConnection) => test.mutate(t.id, {
    onSuccess: (r) => {
      if (!r.ok) {
        toast.error(`Test failed — ${r.error ?? "unknown error"}`)
        return
      }
      const verified = t.kind === "qui" ? "qui accepted the API key" : "cross-seed v6 exposes no key check"
      toast.success(`Reachable — ${verified}`)
    },
    onError: () => toast.error("Test request failed"),
  })

  const closeDialog = () => {
    create.reset()
    update.reset()
    setDialog({ open: false })
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <div className="flex flex-col">
          <h2 className="text-[14px] font-semibold tracking-tight">Announce targets</h2>
          <p className="text-[12px] text-faint">
            New releases seen on polled feeds are pushed to cross-seed tools.
          </p>
        </div>
        <Button variant="outline" size="sm" className="ml-auto" onClick={() => setDialog({ open: true })}>
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
                  title={`This target's harbrr URL port doesn't match harbrr's configured port (${serverInfo.data?.port}). If it isn't a deliberate proxy/port mapping, edit the target to update it.`}
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
          <Button
            variant="outline"
            size="sm"
            disabled={test.isPending && test.variables === t.id}
            onClick={() => runTest(t)}
          >
            {test.isPending && test.variables === t.id ? "Testing…" : "Test"}
          </Button>
          <Button variant="ghost" size="icon" aria-label={`Edit ${t.name}`} onClick={() => setDialog({ open: true, existing: t })}>
            <Pencil className="h-4 w-4" />
          </Button>
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

      <Dialog open={dialog.open} onOpenChange={(open) => { if (!open) closeDialog() }}>
        <DialogContent>
          {dialog.open && (
            <AnnounceForm
              existing={dialog.existing}
              pending={create.isPending || update.isPending}
              error={editing ? update.error : create.error}
              onCreate={(body) => create.mutate(body, {
                onSuccess: () => {
                  toast.success(`${body.name} added`)
                  setDialog({ open: false })
                },
              })}
              onUpdate={(id, body) => update.mutate({ id, body }, {
                onSuccess: () => {
                  toast.success("Target updated")
                  setDialog({ open: false })
                },
              })}
            />
          )}
        </DialogContent>
      </Dialog>
    </section>
  )
}

// AnnounceForm creates or edits a target. The tool's API key is stored encrypted and
// never read back: on edit the field starts empty and is only sent when the operator
// types a replacement (omit = keep the stored key, per the API). Kind is fixed on edit.
function AnnounceForm({ existing, pending, error, onCreate, onUpdate }: {
  existing?: AnnounceConnection
  pending: boolean
  error: unknown
  onCreate: (body: CreateAnnounceConnection) => void
  onUpdate: (id: number, body: UpdateAnnounceConnection) => void
}) {
  const [name, setName] = useState(existing?.name ?? "")
  const [kind, setKind] = useState<AnnounceKind>(existing?.kind ?? "qui")
  const [baseUrl, setBaseUrl] = useState(existing?.baseUrl ?? "")
  const [apiKey, setApiKey] = useState("")
  const [harbrrUrl, setHarbrrUrl] = useState(existing?.harbrrUrl ?? defaultHarbrrUrl())

  const mode = existing ? "edit" : "create"
  const message = error instanceof Error ? error.message : null

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        if (mode === "edit" && existing) {
          onUpdate(existing.id, {
            name, baseUrl, harbrrUrl,
            ...(apiKey !== "" ? { apiKey } : {}), // omit = keep the stored key
          })
        } else {
          onCreate({ name, kind, baseUrl, apiKey, harbrrUrl })
        }
      }}
    >
      <DialogHeader>
        <DialogTitle>{mode === "edit" ? `Edit ${existing?.name}` : "Add announce target"}</DialogTitle>
        <DialogDescription>The tool&apos;s API key is stored encrypted and never shown again.</DialogDescription>
      </DialogHeader>
      {message && (
        <p className="rounded-md border border-bad/40 bg-bad/10 px-3 py-2 text-[13px] text-bad">{message}</p>
      )}
      <div className="grid grid-cols-2 gap-3">
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="ann-name">Name</Label>
          <Input id="ann-name" value={name} onChange={(e) => setName(e.target.value)} />
        </span>
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="ann-kind">Kind</Label>
          <NativeSelect id="ann-kind" value={kind} disabled={mode === "edit"} onChange={(e) => setKind(e.target.value as AnnounceKind)}>
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
        <Label htmlFor="ann-key">{mode === "edit" ? "Tool API key (leave blank to keep the stored key)" : "Tool API key"}</Label>
        <Input id="ann-key" type="password" autoComplete="off" value={apiKey} onChange={(e) => setApiKey(e.target.value)} />
      </span>
      <span className="flex flex-col gap-1.5">
        <Label htmlFor="ann-harbrr">harbrr URL as the tool reaches it</Label>
        <Input id="ann-harbrr" value={harbrrUrl} onChange={(e) => setHarbrrUrl(e.target.value)} />
      </span>
      <DialogFooter>
        <Button type="submit" disabled={pending || !name || !baseUrl || !harbrrUrl || (mode === "create" && !apiKey)}>
          {pending ? "Saving…" : mode === "edit" ? "Save changes" : "Add target"}
        </Button>
      </DialogFooter>
    </form>
  )
}
