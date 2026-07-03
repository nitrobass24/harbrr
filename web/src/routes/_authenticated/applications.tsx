import { useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { Plus, RefreshCw } from "lucide-react"
import { toast } from "sonner"
import { AnnounceSection } from "@/components/applications/AnnounceSection"
import { ConnectionCard } from "@/components/applications/ConnectionCard"
import { ConnectionDialog, type ConnectionDialogState } from "@/components/applications/ConnectionDialog"
import { SelectIndexersDialog } from "@/components/applications/SelectIndexersDialog"
import { StatusDrawer } from "@/components/applications/StatusDrawer"
import { SyncReportView } from "@/components/applications/SyncReportView"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog"
import {
  useAppConnections,
  useCreateConnection,
  useDeleteConnection,
  useSetConnectionEnabled,
  useSetSelectedIndexers,
  useSyncAll,
  useSyncConnection,
  useTestConnection,
  useUpdateConnection
} from "@/hooks/useAppConnections"
import type { AppConnection, ConnectionSyncResult, SyncReport } from "@/types/api"

export const Route = createFileRoute("/_authenticated/applications")({
  component: ApplicationsPage,
})

function ApplicationsPage() {
  const connections = useAppConnections()
  const toggle = useSetConnectionEnabled()
  const test = useTestConnection()
  const sync = useSyncConnection()
  const syncAll = useSyncAll()
  const remove = useDeleteConnection()

  const [dialog, setDialog] = useState<ConnectionDialogState>({ open: false })
  const [statusFor, setStatusFor] = useState<number | null>(null)
  const [selectFor, setSelectFor] = useState<AppConnection | null>(null)
  const [report, setReport] = useState<{ title: string, report: SyncReport } | null>(null)
  const [allReports, setAllReports] = useState<ConnectionSyncResult[] | null>(null)

  const editing = dialog.open ? dialog.existing : undefined
  const create = useCreateConnection()
  const update = useUpdateConnection(editing?.id ?? 0)
  const select = useSetSelectedIndexers(selectFor?.id ?? 0)

  const total = connections.data?.length ?? 0

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center gap-4 border-b border-border px-7">
        <div className="flex flex-col">
          <h1 className="text-[15px] font-semibold leading-tight tracking-tight">Applications</h1>
          <p className="text-[12px] text-faint">{total} connected · indexers sync into each app automatically</p>
        </div>
        <div className="ml-auto flex items-center gap-2.5">
          <Button
            variant="outline"
            disabled={syncAll.isPending || total === 0}
            onClick={() => syncAll.mutate(undefined, {
              onSuccess: setAllReports,
              onError: () => toast.error("Sync all failed"),
            })}
          >
            <RefreshCw className={syncAll.isPending ? "h-4 w-4 animate-spin" : "h-4 w-4"} />
            {syncAll.isPending ? "Syncing…" : "Sync all"}
          </Button>
          <Button onClick={() => setDialog({ open: true })}>
            <Plus className="h-4 w-4" /> Add application
          </Button>
        </div>
      </header>

      <div className="flex min-h-0 flex-1 flex-col gap-8 overflow-auto px-7 py-6">
        <section className="flex flex-col gap-3">
          {(connections.data ?? []).map((conn) => (
            <ConnectionCard
              key={conn.id}
              conn={conn}
              syncing={sync.isPending && sync.variables === conn.id}
              actions={{
                onToggle: (id, enabled) => toggle.mutate({ id, enabled }),
                onTest: (id) => test.mutate(id, {
                  onSuccess: (r) => r.ok ? toast.success("Connection OK") : toast.error(`Test failed — ${r.error ?? "unknown error"}`),
                  onError: () => toast.error("Test request failed"),
                }),
                onSync: (id) => sync.mutate(id, {
                  onSuccess: (rep) => setReport({ title: connections.data?.find((c) => c.id === id)?.name ?? "", report: rep }),
                  onError: () => toast.error("Sync failed"),
                }),
                onEdit: (conn) => setDialog({ open: true, existing: conn }),
                onDelete: (conn) => remove.mutate(conn.id, {
                  onSuccess: () => toast.success(`${conn.name} deleted`),
                  onError: () => toast.error(`Deleting ${conn.name} failed`),
                }),
                onStatus: setStatusFor,
                onSelectIndexers: setSelectFor,
              }}
            />
          ))}
          {connections.isSuccess && total === 0 && (
            <div className="grid place-items-center rounded-xl border border-dashed border-border py-16 text-center">
              <div>
                <p className="text-[14px] font-medium">No applications connected</p>
                <p className="mt-1 text-[13px] text-muted-foreground">
                  Connect Sonarr, Radarr, or qui and harbrr keeps their indexers in sync.
                </p>
                <Button className="mt-4" onClick={() => setDialog({ open: true })}>
                  <Plus className="h-4 w-4" /> Add application
                </Button>
              </div>
            </div>
          )}
        </section>

        <AnnounceSection />
      </div>

      <ConnectionDialog
        state={dialog}
        pending={create.isPending || update.isPending}
        error={editing ? update.error : create.error}
        onClose={() => setDialog({ open: false })}
        onCreate={(body) => create.mutate(body, {
          onSuccess: () => {
            toast.success(`${body.name} connected`)
            setDialog({ open: false })
          },
        })}
        onUpdate={(_id, body) => update.mutate(body, {
          onSuccess: () => {
            toast.success("Connection updated")
            setDialog({ open: false })
          },
        })}
      />

      <StatusDrawer connectionId={statusFor} onClose={() => setStatusFor(null)} />

      <SelectIndexersDialog
        conn={selectFor}
        pending={select.isPending}
        onClose={() => setSelectFor(null)}
        onSave={(_id, instanceIds) => select.mutate(instanceIds, {
          onSuccess: () => {
            toast.success("Selection saved")
            setSelectFor(null)
          },
          onError: () => toast.error("Saving the selection failed"),
        })}
      />

      <Dialog open={report !== null} onOpenChange={(open) => { if (!open) setReport(null) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Sync — {report?.title}</DialogTitle>
            <DialogDescription>Per-indexer outcome of this run.</DialogDescription>
          </DialogHeader>
          {report && <SyncReportView report={report.report} />}
        </DialogContent>
      </Dialog>

      <Dialog open={allReports !== null} onOpenChange={(open) => { if (!open) setAllReports(null) }}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>Sync all</DialogTitle>
            <DialogDescription>One report per enabled connection.</DialogDescription>
          </DialogHeader>
          <div className="flex max-h-96 flex-col gap-4 overflow-auto">
            {(allReports ?? []).map((r) => (
              <div key={r.connectionId} className="flex flex-col gap-1">
                <p className="text-[13px] font-semibold">{r.name}</p>
                {r.error ? <p className="text-[13px] text-bad">{r.error}</p> : <SyncReportView report={r.report} />}
              </div>
            ))}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
