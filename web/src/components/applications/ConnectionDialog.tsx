import { useState } from "react"
import { useInitialAppPick } from "@/hooks/useInitialAppPick"
import { ConfiguredAppsBlock, ReusingAppHint } from "@/components/applications/ConfiguredApps"
import { HostPortFields } from "@/components/forms/HostPortFields"
import { ManagedByAppHint } from "@/components/applications/ManagedByAppHint"
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
import { useSyncProfiles } from "@/hooks/useAppConnections"
import { useApps } from "@/hooks/useApps"
import { defaultHarbrrUrl } from "@/lib/base-url"
import { hostname, kindLabel } from "@/lib/format"
import { composeHostURL, DEFAULT_PORTS } from "@/lib/hosturl"
import type { App, AppConnection, ConnectionKind, CreateConnection, UpdateConnection } from "@/lib/api"

// Sentinel select value for "no existing App picked, use the inline fields below" — the
// create-time fallback when no App of this kind exists yet, or the operator wants a
// fresh one.
const NEW_APP = "new"

const KINDS: ConnectionKind[] = ["sonarr", "radarr", "lidarr", "readarr", "whisparr", "qui"]

// Mirrors the server's create-time materialization (internal/appsync/validate.go
// defaultFreeleechMode): the "default by kind" choice resolves to bypass for qui — which
// drives cross-seed off the full catalog — and honor for every *arr. On edit the PATCH is
// pointer-semantics and applied only for non-nil fields, so we resolve the concrete value
// client-side; sending an omitted/empty freeleechMode would silently keep the prior mode.
function defaultFreeleechMode(kind: ConnectionKind): "honor" | "bypass" {
  return kind === "qui" ? "bypass" : "honor"
}

export type ConnectionDialogState =
  | { open: false }
  | { open: true, existing?: AppConnection, initialAppId?: number }

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
            initialAppId={state.initialAppId}
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

function ConnectionForm({ existing, initialAppId, pending, error, onCreate, onUpdate }: {
  existing?: AppConnection
  initialAppId?: number
  pending: boolean
  error: unknown
  onCreate: (body: CreateConnection) => void
  onUpdate: (id: number, body: UpdateConnection) => void
}) {
  const apps = useApps()

  const [name, setName] = useState(existing?.name ?? "")
  const [kind, setKind] = useState<ConnectionKind>(existing?.kind ?? "sonarr")
  // Create-only: which App backs this connection. `null` means the operator hasn't
  // chosen yet, so the picker defaults to the first App of this kind once apps arrive
  // (effectiveAppSel below) rather than forcing "New app…" while the list is still
  // loading. NEW_APP reveals the inline baseUrl/apiKey/harbrrUrl fields; anything else
  // reuses that App's identity.
  const [appSel, setAppSel] = useState<string | null>(null)
  const [scheme, setScheme] = useState<"http" | "https">("http")
  const [host, setHost] = useState("")
  const [port, setPort] = useState(String(DEFAULT_PORTS[kind] ?? ""))
  const [apiKey, setApiKey] = useState("")
  const [harbrrUrl, setHarbrrUrl] = useState(defaultHarbrrUrl())
  const [syncLevel, setSyncLevel] = useState(existing?.syncLevel ?? "full")
  const [indexScope, setIndexScope] = useState(existing?.indexScope ?? "all")
  const [freeleechMode, setFreeleechMode] = useState<"" | "honor" | "bypass">(existing?.freeleechMode ?? "")
  const [syncProfileId, setSyncProfileId] = useState<number | null>(existing?.syncProfileId ?? null)

  // "Use as…" deep-link (autobrr/harbrr#300): pre-pick the App the same way
  // ConfiguredAppsBlock's onPick below does (kind + app selected, name defaulted if
  // blank).
  useInitialAppPick(initialAppId, apps.data, (app) => {
    setKind(app.kind as ConnectionKind)
    setAppSel(String(app.id))
    setName((prev) => (prev === "" ? app.name : prev))
    setPort(String(DEFAULT_PORTS[app.kind] ?? ""))
  })

  const profiles = useSyncProfiles()
  const mode = existing ? "edit" : "create"
  const message = error instanceof Error ? error.message : null
  // Profiles never apply to qui — it has no per-content-type category concept.
  const showProfilePicker = kind !== "qui"
  const appsOfKind = (apps.data ?? []).filter((a) => a.kind === kind)
  // App-sync is one-row-per-App, so a used app is not offerable: the default skips it
  // and its picker option is disabled — otherwise it pre-selects a guaranteed 409.
  const isUsed = (a: App) => a.references.appConnections > 0
  const firstFree = appsOfKind.find((a) => !isUsed(a))
  const effectiveAppSel = appSel ?? (firstFree ? String(firstFree.id) : NEW_APP)
  const usingNewApp = effectiveAppSel === NEW_APP
  const configuredApps = (apps.data ?? []).filter((a) => (KINDS as string[]).includes(a.kind))

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        // Resolve "default by kind" to the kind's concrete default so the edit is
        // honored: the PATCH omits an undefined field, which would silently keep the
        // prior mode.
        const resolvedFreeleechMode = freeleechMode || defaultFreeleechMode(kind)
        if (mode === "edit" && existing) {
          onUpdate(existing.id, {
            name, syncLevel, indexScope,
            freeleechMode: resolvedFreeleechMode,
            // Always send for non-qui edits (number or null) so clearing works; omit entirely for qui.
            ...(showProfilePicker ? { syncProfileId } : {}),
          })
        } else {
          onCreate({
            name, kind, syncLevel, indexScope,
            freeleechMode: freeleechMode || undefined,
            ...(showProfilePicker && syncProfileId !== null ? { syncProfileId } : {}),
            ...(usingNewApp ? { baseUrl: composeHostURL(scheme, host, port), apiKey, harbrrUrl } : { appId: Number(effectiveAppSel) }),
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

      {mode === "create" && (
        <ConfiguredAppsBlock
          apps={configuredApps}
          isUsed={isUsed}
          onPick={(a: App) => {
            setKind(a.kind as ConnectionKind)
            setAppSel(String(a.id))
            if (name === "") setName(a.name)
          }}
        />
      )}

      <div className="grid grid-cols-2 gap-3">
        <FieldWrap id="conn-name" label="Name">
          <Input id="conn-name" value={name} onChange={(e) => setName(e.target.value)} />
        </FieldWrap>
        <FieldWrap id="conn-kind" label="Kind">
          <NativeSelect
            id="conn-kind"
            value={kind}
            disabled={mode === "edit"}
            onChange={(e) => {
              const next = e.target.value as ConnectionKind
              setKind(next)
              setAppSel(null) // the app list for the new kind is different; re-default.
              // A typed port for the OLD kind isn't meaningful for the new one.
              setPort(String(DEFAULT_PORTS[next] ?? ""))
            }}
          >
            {KINDS.map((k) => <option key={k} value={k}>{kindLabel(k)}</option>)}
          </NativeSelect>
        </FieldWrap>
      </div>

      {mode === "create" && (
        <FieldWrap id="conn-app" label="App">
          <NativeSelect id="conn-app" value={effectiveAppSel} onChange={(e) => setAppSel(e.target.value)}>
            {appsOfKind.map((a) => (
              <option key={a.id} value={a.id} disabled={isUsed(a)}>
                {a.name} ({hostname(a.baseUrl)}){isUsed(a) ? " — already added" : ""}
              </option>
            ))}
            <option value={NEW_APP}>New app…</option>
          </NativeSelect>
        </FieldWrap>
      )}

      {mode === "create" && !usingNewApp && (
        <ReusingAppHint app={appsOfKind.find((a) => String(a.id) === effectiveAppSel)} />
      )}

      {mode === "edit" && <ManagedByAppHint appId={existing?.appId} />}

      {mode === "create" && usingNewApp && (
        <>
          <HostPortFields idPrefix="conn" scheme={scheme} host={host} port={port} onScheme={setScheme} onHost={setHost} onPort={setPort} />

          <FieldWrap id="conn-apikey" label="App API key">
            <Input id="conn-apikey" type="password" autoComplete="off" value={apiKey} onChange={(e) => setApiKey(e.target.value)} />
          </FieldWrap>

          <FieldWrap id="conn-harbrr" label="harbrr URL as the app reaches it">
            <Input id="conn-harbrr" placeholder="http://harbrr:7478" value={harbrrUrl} onChange={(e) => setHarbrrUrl(e.target.value)} />
          </FieldWrap>
        </>
      )}

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

      {showProfilePicker && (
        <FieldWrap id="conn-profile" label="Sync profile">
          <NativeSelect
            id="conn-profile"
            value={syncProfileId === null ? "" : String(syncProfileId)}
            onChange={(e) => setSyncProfileId(e.target.value === "" ? null : Number(e.target.value))}
          >
            <option value="">None</option>
            {(profiles.data ?? []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
          </NativeSelect>
        </FieldWrap>
      )}

      <DialogFooter>
        <Button
          type="submit"
          disabled={
            pending || name === "" ||
            (mode === "create" && usingNewApp && (host === "" || harbrrUrl === "" || apiKey === ""))
          }
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
