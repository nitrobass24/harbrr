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
import { useProxies, useProxyMutations } from "@/hooks/useResources"
import type { Proxy, ProxyType } from "@/types/api"

const PROXY_TYPES: ProxyType[] = ["http", "https", "socks5", "socks5h"]

// `null` = closed; `{ proxy: null }` = add; `{ proxy }` = edit that proxy.
type Editing = { proxy: Proxy | null } | null

// Global proxy resources indexers reference by id. The URL (may embed user:pass)
// is stored encrypted, reads back redacted, and rotates only when a new one is typed.
export function ProxiesSection() {
  const proxies = useProxies()
  const { create, update, remove } = useProxyMutations()
  const [editing, setEditing] = useState<Editing>(null)

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <h2 className="text-[14px] font-semibold tracking-tight">Proxies</h2>
        <Button variant="outline" size="sm" className="ml-auto" onClick={() => setEditing({ proxy: null })}>
          <Plus className="h-3.5 w-3.5" /> Add proxy
        </Button>
      </div>

      <div className="flex flex-col rounded-xl border border-border bg-card px-5 py-2 text-[13px]">
        {(proxies.data ?? []).map((p) => (
          <div key={p.id} className="flex items-center gap-3 border-b border-border/60 py-2.5 last:border-b-0">
            <span className="font-medium">{p.name}</span>
            <Badge variant="secondary" className="px-1.5 py-0 text-[11px]">{p.type}</Badge>
            <span className="ml-auto flex items-center gap-1">
              <Button variant="ghost" size="icon" aria-label={`Edit ${p.name}`} onClick={() => setEditing({ proxy: p })}>
                <Pencil className="h-4 w-4" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label={`Delete ${p.name}`}
                onClick={() => remove.mutate(p.id, {
                  onSuccess: () => toast.success(`${p.name} deleted`),
                  onError: () => toast.error(`Deleting ${p.name} failed`),
                })}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </span>
          </div>
        ))}
        {proxies.data?.length === 0 && <p className="py-3 text-muted-foreground">No proxies. Add one to route indexers through it.</p>}
      </div>

      <Dialog open={editing !== null} onOpenChange={(open) => { if (!open) setEditing(null) }}>
        {editing !== null && (
          <DialogContent>
            <ProxyForm
              // Remount (fresh state seeded from props) per target.
              key={editing.proxy?.id ?? "new"}
              proxy={editing.proxy}
              pending={create.isPending || update.isPending}
              onSubmit={(id, body) => {
                const done = { onSuccess: () => setEditing(null), onError: (err: Error) => toast.error(`Save failed: ${err.message}`) }
                if (id === null) create.mutate({ name: body.name, type: body.type, url: body.url ?? "" }, done)
                else update.mutate({ id, body }, done)
              }}
            />
          </DialogContent>
        )}
      </Dialog>
    </section>
  )
}

function ProxyForm({ proxy, pending, onSubmit }: {
  proxy: Proxy | null
  pending: boolean
  onSubmit: (id: number | null, body: { name: string, type: ProxyType, url?: string }) => void
}) {
  const isEdit = proxy !== null
  const [name, setName] = useState(proxy?.name ?? "")
  const [type, setType] = useState<ProxyType>(proxy?.type ?? "http")
  const [url, setUrl] = useState("")

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        // On edit, an empty URL keeps the stored one (only a typed value rotates).
        onSubmit(proxy?.id ?? null, { name, type, url: isEdit ? (url || undefined) : url })
      }}
    >
      <DialogHeader>
        <DialogTitle>{isEdit ? "Edit proxy" : "Add proxy"}</DialogTitle>
        <DialogDescription>The URL is stored encrypted and never shown again.</DialogDescription>
      </DialogHeader>
      <div className="grid grid-cols-2 gap-3">
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="proxy-name">Name</Label>
          <Input id="proxy-name" value={name} onChange={(e) => setName(e.target.value)} />
        </span>
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="proxy-type">Type</Label>
          <NativeSelect id="proxy-type" value={type} onChange={(e) => setType(e.target.value as ProxyType)}>
            {PROXY_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
          </NativeSelect>
        </span>
      </div>
      <span className="flex flex-col gap-1.5">
        <Label htmlFor="proxy-url">URL {isEdit && <span className="text-faint">(leave blank to keep)</span>}</Label>
        <Input
          id="proxy-url"
          type="password"
          autoComplete="off"
          placeholder="socks5://user:pass@host:1080"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
        />
      </span>
      <DialogFooter>
        <Button type="submit" disabled={pending || !name || (!isEdit && !url)}>
          {pending ? "Saving…" : isEdit ? "Save changes" : "Add proxy"}
        </Button>
      </DialogFooter>
    </form>
  )
}
