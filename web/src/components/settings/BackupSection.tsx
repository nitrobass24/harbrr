import { useRef, useState } from "react"
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
import { useExportBackup, useImportBackup } from "@/hooks/useSettings"
import { APIError } from "@/lib/api"
import { notifyError, notifySuccess } from "@/lib/notify"

// triggerDownload saves a Blob to disk via a throwaway <a download> — there is
// no other browser API for "save this in-memory data as a file".
function triggerDownload(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

// BackupSection covers export (encrypt config + DB to a downloadable bundle)
// and import (restore from one, wipe-and-load) — see issue #239 / #98's API.
export function BackupSection() {
  return (
    <section id="backup" className="flex flex-col gap-3">
      <h2 className="text-[14px] font-semibold tracking-tight">Backup</h2>
      <p className="text-[12px] text-faint">
        The bundle contains your encrypted credentials (indexers, connections, proxies, keys).
        The passphrase seals it and is never stored — if you lose it, the backup is useless.
      </p>
      <div className="flex flex-col gap-6 rounded-xl border border-border bg-card px-5 py-4 text-[13px] sm:flex-row sm:gap-4">
        <ExportBlock />
        <div className="w-px self-stretch bg-border/60 sm:block hidden" />
        <ImportBlock />
      </div>
    </section>
  )
}

function ExportBlock() {
  const exportBackup = useExportBackup()
  const [passphrase, setPassphrase] = useState("")
  const [confirm, setConfirm] = useState("")
  const mismatch = confirm !== "" && confirm !== passphrase

  return (
    <form
      className="flex flex-1 flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault()
        exportBackup.mutate(passphrase, {
          onSuccess: ({ blob, filename }) => {
            triggerDownload(blob, filename)
            notifySuccess("Backup downloaded")
            setPassphrase("")
            setConfirm("")
          },
          onError: (err) => notifyError("Export failed", err),
        })
      }}
    >
      <h3 className="text-[13px] font-medium">Export</h3>
      <span className="flex flex-col gap-1.5">
        <Label htmlFor="export-passphrase">Passphrase</Label>
        <Input
          id="export-passphrase"
          type="password"
          autoComplete="off"
          value={passphrase}
          onChange={(e) => setPassphrase(e.target.value)}
        />
      </span>
      <span className="flex flex-col gap-1.5">
        <Label htmlFor="export-passphrase-confirm">Confirm passphrase</Label>
        <Input
          id="export-passphrase-confirm"
          type="password"
          autoComplete="off"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />
        {mismatch && <p className="text-[12px] text-bad">Passphrases do not match.</p>}
      </span>
      <div>
        <Button
          type="submit"
          size="sm"
          disabled={exportBackup.isPending || !passphrase || passphrase !== confirm}
        >
          {exportBackup.isPending ? "Exporting…" : "Export backup"}
        </Button>
      </div>
    </form>
  )
}

function ImportBlock() {
  const importBackup = useImportBackup()
  const fileRef = useRef<HTMLInputElement>(null)
  const [passphrase, setPassphrase] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [conflictPayload, setConflictPayload] = useState<string | null>(null)

  function runImport(payload: string, force: boolean) {
    setError(null)
    importBackup.mutate({ payload, passphrase, force }, {
      onSuccess: () => {
        notifySuccess("Backup restored — reloading…")
        window.location.reload()
      },
      onError: (err) => {
        if (err instanceof APIError && err.status === 409) {
          setConflictPayload(payload)
          return
        }
        setError(err instanceof APIError ? err.message : "Import failed")
      },
    })
  }

  function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    const file = fileRef.current?.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      // readAsDataURL yields "data:<mime>;base64,<payload>" — the API wants
      // just the base64 payload.
      const dataUrl = reader.result as string
      runImport(dataUrl.slice(dataUrl.indexOf(",") + 1), false)
    }
    reader.readAsDataURL(file)
  }

  return (
    <>
      <form className="flex flex-1 flex-col gap-3" onSubmit={onSubmit}>
        <h3 className="text-[13px] font-medium">Import</h3>
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="import-file">Backup file</Label>
          <Input id="import-file" ref={fileRef} type="file" accept=".json,application/json" />
        </span>
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="import-passphrase">Passphrase</Label>
          <Input
            id="import-passphrase"
            type="password"
            autoComplete="off"
            value={passphrase}
            onChange={(e) => setPassphrase(e.target.value)}
          />
        </span>
        {error && <p className="text-[12px] text-bad">{error}</p>}
        <div>
          <Button type="submit" size="sm" disabled={importBackup.isPending || !passphrase}>
            {importBackup.isPending ? "Importing…" : "Import backup"}
          </Button>
        </div>
      </form>

      <Dialog open={conflictPayload !== null} onOpenChange={(open) => { if (!open) setConflictPayload(null) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Replace everything?</DialogTitle>
            <DialogDescription>
              This instance already has configuration. Importing wipes and replaces all of it —
              indexers, connections, proxies, keys, and the admin account — with the contents of
              the backup. This cannot be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConflictPayload(null)}>Cancel</Button>
            <Button
              variant="destructive"
              disabled={importBackup.isPending}
              onClick={() => {
                const payload = conflictPayload
                setConflictPayload(null)
                if (payload) runImport(payload, true)
              }}
            >
              Replace everything
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
