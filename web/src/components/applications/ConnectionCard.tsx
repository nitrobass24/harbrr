import { ListChecks, MoreVertical, Pencil, RefreshCcw, RefreshCw, Trash2 } from "lucide-react"
import { SyncError } from "@/components/applications/SyncError"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu"
import { Switch } from "@/components/ui/switch"
import { useServerInfo } from "@/hooks/useAppConnections"
import { explicitUrlPort, withPort } from "@/lib/base-url"
import { hostname, relativeTime, syncStatusClass } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { AppConnection } from "@/types/api"

export type ConnectionActions = {
  onToggle: (id: number, enabled: boolean) => void
  onTest: (id: number) => void
  onSync: (id: number) => void
  onEdit: (conn: AppConnection) => void
  onDelete: (conn: AppConnection) => void
  onStatus: (id: number) => void
  onSelectIndexers: (conn: AppConnection) => void
  onFixPort: (conn: AppConnection, harbrrUrl: string) => void
}

export function ConnectionCard({ conn, syncing, actions }: {
  conn: AppConnection
  syncing?: boolean
  actions: ConnectionActions
}) {
  const serverInfo = useServerInfo()
  const livePort = serverInfo.data?.port
  const storedPort = explicitUrlPort(conn.harbrrUrl)
  // Only a URL that names a port outright is comparable against harbrr's own
  // listen port: a reverse-proxied URL (the common case defaultHarbrrUrl's
  // window.location.origin prefill produces behind a TLS-terminating proxy)
  // has no explicit port and explicitUrlPort returns null for it. Even an
  // explicit differing port can be a deliberate Docker port mapping, so the
  // badge is advisory and the fix goes through a confirm dialog upstream.
  const stalePort = livePort !== undefined && storedPort !== null && storedPort !== livePort ? livePort : null

  return (
    <div className="flex items-center gap-4 rounded-xl border border-border bg-card px-5 py-4">
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex items-center gap-2">
          <span className={cn("text-[14px] font-medium", !conn.enabled && "text-muted-foreground")}>{conn.name}</span>
          <Badge variant="secondary" className="px-1.5 py-0 text-[11px] uppercase">{conn.kind}</Badge>
          <Badge variant="outline" className={cn("px-1.5 py-0 text-[11px]", conn.freeleechMode === "bypass" ? "border-warn/40 bg-warn/10 text-warn" : "border-ok/40 bg-ok/10 text-ok")}>
            {conn.freeleechMode === "bypass" ? "FL bypass" : "FL honor"}
          </Badge>
          <Badge variant="outline" className={cn("px-1.5 py-0 text-[11px]", conn.syncLevel === "full" && "border-brand/40 bg-brand/10 text-brand")}>
            {conn.syncLevel === "full" ? "full sync" : "add/update"}
          </Badge>
          <Badge variant="outline" className={cn("px-1.5 py-0 text-[11px]", conn.indexScope === "all" && "border-brand/40 bg-brand/10 text-brand")}>
            {conn.indexScope === "all" ? "all indexers" : "selected"}
          </Badge>
          {stalePort !== null && (
            <button
              type="button"
              aria-label={`Update ${conn.name}'s harbrr URL port to ${stalePort}`}
              title={`URL uses port ${storedPort}; harbrr's configured port is ${stalePort} — review and update`}
              className="cursor-pointer"
              onClick={() => actions.onFixPort(conn, withPort(conn.harbrrUrl, stalePort))}
            >
              <Badge variant="outline" className="flex items-center gap-1 px-1.5 py-0 text-[11px] text-warn hover:bg-warn/10">
                port may be outdated
                <RefreshCcw className="h-3 w-3" />
              </Badge>
            </button>
          )}
        </div>
        <div className="flex items-center gap-2 text-[12px] text-faint">
          <span>{hostname(conn.baseUrl)}</span>
          {conn.lastSyncAt && (
            <span>
              · last sync{" "}
              <span className={syncStatusClass(conn.lastSyncStatus)}>{conn.lastSyncStatus}</span>{" "}
              {relativeTime(conn.lastSyncAt)}
            </span>
          )}
        </div>
        {conn.lastSyncError && <SyncError error={conn.lastSyncError} />}
      </div>

      <Switch
        aria-label={`${conn.enabled ? "Disable" : "Enable"} ${conn.name}`}
        checked={conn.enabled}
        onCheckedChange={(checked) => actions.onToggle(conn.id, checked)}
      />
      <Button variant="outline" size="sm" onClick={() => actions.onTest(conn.id)}>Test</Button>
      <Button variant="outline" size="sm" disabled={syncing} onClick={() => actions.onSync(conn.id)}>
        <RefreshCw className={cn("h-3.5 w-3.5", syncing && "animate-spin")} />
        {syncing ? "Syncing…" : "Sync now"}
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" aria-label={`More actions for ${conn.name}`}>
            <MoreVertical className="h-4 w-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={() => actions.onEdit(conn)}>
            <Pencil className="h-4 w-4" /> Edit
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => actions.onStatus(conn.id)}>
            <ListChecks className="h-4 w-4" /> Sync ledger
          </DropdownMenuItem>
          {conn.indexScope === "selected" && (
            <DropdownMenuItem onClick={() => actions.onSelectIndexers(conn)}>
              <ListChecks className="h-4 w-4" /> Select indexers
            </DropdownMenuItem>
          )}
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive" onClick={() => actions.onDelete(conn)}>
            <Trash2 className="h-4 w-4" /> Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
