import { useState } from "react"
import { Pencil, Trash2 } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { NativeSelect } from "@/components/ui/native-select"
import { ResourceSection } from "@/components/ui/resource-section"
import { useProxies, useProxyMutations } from "@/hooks/useResources"
import type { Proxy, ProxyType } from "@/lib/api"

const PROXY_TYPES: ProxyType[] = ["http", "https", "socks5", "socks5h"]

// Global proxy resources indexers reference by id. host/port/username are plain
// (visible on read); only the password is stored encrypted and rotates only when
// a new one is typed.
export function ProxiesSection() {
  const proxies = useProxies()
  const { create, update, remove } = useProxyMutations()

  return (
    <ResourceSection<Proxy>
      title="Proxies"
      addLabel="Add proxy"
      query={proxies}
      empty="No proxies. Add one to route indexers through it."
      row={(p, actions) => (
        <>
          <span className="font-medium">{p.name}</span>
          <Badge variant="secondary" className="px-1.5 py-0 text-[11px]">{p.type}</Badge>
          <span className="text-muted-foreground">{p.host}:{p.port}</span>
          <span className="ml-auto flex items-center gap-1">
            <Button variant="ghost" size="icon" aria-label={`Edit ${p.name}`} onClick={actions.edit}>
              <Pencil className="h-4 w-4" />
            </Button>
            <Button variant="ghost" size="icon" aria-label={`Delete ${p.name}`} onClick={actions.remove}>
              <Trash2 className="h-4 w-4" />
            </Button>
          </span>
        </>
      )}
      onDelete={(p) => remove.mutateAsync(p.id)}
      form={(target, done) => (
        <ProxyForm
          proxy={target}
          pending={create.isPending || update.isPending}
          onSubmit={(id, body) => {
            if (id === null) create.mutate(body, done)
            else update.mutate({ id, body }, done)
          }}
        />
      )}
    />
  )
}

function ProxyForm({ proxy, pending, onSubmit }: {
  proxy: Proxy | null
  pending: boolean
  onSubmit: (id: number | null, body: { name: string, type: ProxyType, host: string, port: number, username: string, password?: string }) => void
}) {
  const isEdit = proxy !== null
  const [name, setName] = useState(proxy?.name ?? "")
  const [type, setType] = useState<ProxyType>(proxy?.type ?? "http")
  const [host, setHost] = useState(proxy?.host ?? "")
  const [port, setPort] = useState(proxy ? String(proxy.port) : "")
  const [username, setUsername] = useState(proxy?.username ?? "")
  const [password, setPassword] = useState("")

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        // On edit, an empty password keeps the stored one (only a typed value rotates).
        onSubmit(proxy?.id ?? null, {
          name, type, host, port: Number(port), username,
          password: isEdit ? (password || undefined) : password,
        })
      }}
    >
      <DialogHeader>
        <DialogTitle>{isEdit ? "Edit proxy" : "Add proxy"}</DialogTitle>
        <DialogDescription>Host, port, and username are visible; the password is stored encrypted and never shown again.</DialogDescription>
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
      <div className="grid grid-cols-[2fr_1fr] gap-3">
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="proxy-host">Host</Label>
          <Input id="proxy-host" placeholder="proxy.example.com" value={host} onChange={(e) => setHost(e.target.value)} />
        </span>
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="proxy-port">Port</Label>
          <Input id="proxy-port" type="number" min={1} max={65535} placeholder="1080" value={port} onChange={(e) => setPort(e.target.value)} />
        </span>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="proxy-username">Username <span className="text-faint">(optional)</span></Label>
          <Input id="proxy-username" autoComplete="off" value={username} onChange={(e) => setUsername(e.target.value)} />
        </span>
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="proxy-password">Password {isEdit && <span className="text-faint">(leave blank to keep)</span>}</Label>
          <Input
            id="proxy-password"
            type="password"
            autoComplete="off"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </span>
      </div>
      <DialogFooter>
        <Button type="submit" disabled={pending || !name || !host || !port}>
          {pending ? "Saving…" : isEdit ? "Save changes" : "Add proxy"}
        </Button>
      </DialogFooter>
    </form>
  )
}
