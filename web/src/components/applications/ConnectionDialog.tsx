import { useState } from "react"
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
import { getBaseUrl } from "@/lib/base-url"
import type { AppConnection, ConnectionKind, CreateConnection, UpdateConnection } from "@/types/api"

const KINDS: ConnectionKind[] = ["sonarr", "radarr", "lidarr", "readarr", "whisparr", "qui"]

export type ConnectionDialogState =
  | { open: false }
  | { open: true, existing?: AppConnection }

// Create/edit dialog for an app-sync target. The app's API key is stored
// encrypted and never read back: on edit the field starts empty and is only
// sent when the operator types a replacement (omit = keep, per the API).
export function ConnectionDialog({ state, pending, error, onClose, onCreate, onUpdate }: {
  state: ConnectionDialogState
  pending: boolean
  error: unknown
  onClose: () => void
  onCreate: (body: CreateConnection) => void
  onUpdate: (id: number, body: UpdateConnection) => void
}) {
  return (
    <Dialog open={state.open} onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="sm:max-w-lg">
        {state.open && (
          <ConnectionForm
            existing={state.existing}
            pending={pending}
            error={error}
            onCreate={onCreate}
            onUpdate={onUpdate}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

function ConnectionForm({ existing, pending, error, onCreate, onUpdate }: {
  existing?: AppConnection
  pending: boolean
  error: unknown
  onCreate: (body: CreateConnection) => void
  onUpdate: (id: number, body: UpdateConnection) => void
}) {
  const [name, setName] = useState(existing?.name ?? "")
  const [kind, setKind] = useState<ConnectionKind>(existing?.kind ?? "sonarr")
  const [baseUrl, setBaseUrl] = useState(existing?.baseUrl ?? "")
  const [apiKey, setApiKey] = useState("")
  const [harbrrUrl, setHarbrrUrl] = useState(existing?.harbrrUrl ?? defaultHarbrrUrl())
  const [syncLevel, setSyncLevel] = useState(existing?.syncLevel ?? "full")
  const [indexScope, setIndexScope] = useState(existing?.indexScope ?? "all")
  const [freeleechMode, setFreeleechMode] = useState<"" | "honor" | "bypass">(existing?.freeleechMode ?? "")

  const mode = existing ? "edit" : "create"
  const message = error instanceof Error ? error.message : null

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        if (mode === "edit" && existing) {
          onUpdate(existing.id, {
            name, baseUrl, harbrrUrl, syncLevel, indexScope,
            freeleechMode: freeleechMode || undefined,
            ...(apiKey !== "" ? { apiKey } : {}), // omit = keep the stored key
          })
        } else {
          onCreate({
            name, kind, baseUrl, apiKey, harbrrUrl, syncLevel, indexScope,
            freeleechMode: freeleechMode || undefined,
          })
        }
      }}
    >
      <DialogHeader>
        <DialogTitle>{mode === "edit" ? `Edit ${existing?.name}` : "Add application"}</DialogTitle>
        <DialogDescription>
          harbrr pushes its indexers into the app; the app then searches through harbrr&apos;s feed.
        </DialogDescription>
      </DialogHeader>

      {message && (
        <p className="rounded-md border border-bad/40 bg-bad/10 px-3 py-2 text-[13px] text-bad">{message}</p>
      )}

      <div className="grid grid-cols-2 gap-3">
        <FieldWrap id="conn-name" label="Name">
          <Input id="conn-name" value={name} onChange={(e) => setName(e.target.value)} />
        </FieldWrap>
        <FieldWrap id="conn-kind" label="Kind">
          <NativeSelect id="conn-kind" value={kind} disabled={mode === "edit"} onChange={(e) => setKind(e.target.value as ConnectionKind)}>
            {KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
          </NativeSelect>
        </FieldWrap>
      </div>

      <FieldWrap id="conn-baseurl" label="App base URL">
        <Input id="conn-baseurl" placeholder="http://sonarr:8989" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
      </FieldWrap>

      <FieldWrap id="conn-apikey" label={mode === "edit" ? "App API key (leave blank to keep the stored key)" : "App API key"}>
        <Input id="conn-apikey" type="password" autoComplete="off" value={apiKey} onChange={(e) => setApiKey(e.target.value)} />
      </FieldWrap>

      <FieldWrap id="conn-harbrr" label="harbrr URL as the app reaches it">
        <Input id="conn-harbrr" placeholder="http://harbrr:7478" value={harbrrUrl} onChange={(e) => setHarbrrUrl(e.target.value)} />
      </FieldWrap>

      <div className="grid grid-cols-3 gap-3">
        <FieldWrap id="conn-level" label="Sync level">
          <NativeSelect id="conn-level" value={syncLevel} onChange={(e) => setSyncLevel(e.target.value as "full" | "add_update")}>
            <option value="full">full</option>
            <option value="add_update">add/update</option>
          </NativeSelect>
        </FieldWrap>
        <FieldWrap id="conn-scope" label="Indexers">
          <NativeSelect id="conn-scope" value={indexScope} onChange={(e) => setIndexScope(e.target.value as "all" | "selected")}>
            <option value="all">all</option>
            <option value="selected">selected</option>
          </NativeSelect>
        </FieldWrap>
        <FieldWrap id="conn-fl" label="Freeleech feed">
          <NativeSelect id="conn-fl" value={freeleechMode} onChange={(e) => setFreeleechMode(e.target.value as "" | "honor" | "bypass")}>
            <option value="">default by kind</option>
            <option value="honor">honor</option>
            <option value="bypass">bypass (full catalog)</option>
          </NativeSelect>
        </FieldWrap>
      </div>

      <DialogFooter>
        <Button
          type="submit"
          disabled={pending || name === "" || baseUrl === "" || harbrrUrl === "" || (mode === "create" && apiKey === "")}
        >
          {pending ? "Saving…" : mode === "edit" ? "Save changes" : "Add application"}
        </Button>
      </DialogFooter>
    </form>
  )
}

function FieldWrap({ id, label, children }: { id: string, label: string, children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
    </div>
  )
}

// Best-effort prefill: how this browser reaches harbrr is usually how the app
// can too (the operator adjusts for container-network names as needed).
function defaultHarbrrUrl(): string {
  return `${window.location.origin}${getBaseUrl()}`
}
