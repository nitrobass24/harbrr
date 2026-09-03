import { useEffect, useState } from "react"
import { useInitialAppPick } from "@/hooks/useInitialAppPick"
import { Pencil, Trash2 } from "lucide-react"
import { ConfiguredAppsBlock, ReusingAppHint } from "@/components/applications/ConfiguredApps"
import { HostPortFields } from "@/components/forms/HostPortFields"
import { ManagedByAppHint } from "@/components/applications/ManagedByAppHint"
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
import { useApps } from "@/hooks/useApps"
import { defaultHarbrrUrl, explicitUrlPort } from "@/lib/base-url"
import { hostname, kindLabel } from "@/lib/format"
import { composeHostURL, DEFAULT_PORTS } from "@/lib/hosturl"
import { notifyError, notifySuccess } from "@/lib/notify"
import type { AnnounceConnection, AnnounceKind, App, CreateAnnounceConnection, UpdateAnnounceConnection } from "@/lib/api"

// Sentinel select value for "no existing App picked, use the inline fields below" —
// mirrors ConnectionDialog's create-time App picker.
const NEW_APP = "new"

// Cross-seed push targets: harbrr announces newly-seen releases to qui's cross-seed
// webhook or cross-seed v6's /api/announce. Each target can be edited in place and
// tested (a non-mutating reachability probe — qui also validates the API key).
export function AnnounceSection({ initialCreate }: { initialCreate?: { appId: number } } = {}) {
  const targets = useAnnounceConnections()
  const create = useCreateAnnounce()
  const update = useUpdateAnnounce()
  const remove = useDeleteAnnounce()
  const toggle = useSetAnnounceEnabled()
  const test = useTestAnnounce()
  const serverInfo = useServerInfo()

  // "Use as…" deep-link (autobrr/harbrr#300): the applications route owns the search
  // params and hands the pick down as a prop; the shell opens the add dialog
  // (addRequest) while this section remembers which App to pre-pick — cleared on any
  // dialog close so a later manual add starts clean.
  const [pendingAppId, setPendingAppId] = useState<number | undefined>()
  useEffect(() => {
    if (initialCreate) setPendingAppId(initialCreate.appId)
  }, [initialCreate])

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
        notifyError(`Test failed — ${r.error ?? "unknown error"}`)
        return
      }
      const verified = t.kind === "qui" ? "qui accepted the API key" : "cross-seed v6 exposes no key check"
      notifySuccess(`Reachable — ${verified}`)
    },
    onError: (err) => notifyError("Test request failed", err),
  })

  return (
    <ResourceSection<AnnounceConnection>
      title="Announce targets"
      subtitle="New releases seen on polled feeds are pushed to cross-seed tools."
      addLabel="Add target"
      query={targets}
      empty="No announce targets. cross-seed v6 users can also grab a per-indexer config snippet from the Indexers table's kebab menu."
      addRequest={initialCreate}
      onDialogClose={() => {
        // Clear any failed-mutation error so it can't resurface the next time the
        // dialog opens (the form remounts, but the mutation error persists).
        create.reset()
        update.reset()
        setPendingAppId(undefined)
      }}
      row={(t, { edit }) => (
        <>
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
          <Button variant="ghost" size="icon" aria-label={`Edit ${t.name}`} onClick={edit}>
            <Pencil className="h-4 w-4" />
          </Button>
          {/* Delete stays section-owned: announce delete has never toasted, and the
              shell's onDelete would introduce new user-visible strings. */}
          <Button variant="ghost" size="icon" aria-label={`Delete ${t.name}`} onClick={() => remove.mutate(t.id)}>
            <Trash2 className="h-4 w-4" />
          </Button>
        </>
      )}
      form={(target, done) => (
        <AnnounceForm
          existing={target ?? undefined}
          initialAppId={target === null ? pendingAppId : undefined}
          pending={create.isPending || update.isPending}
          error={target ? update.error : create.error}
          onCreate={(body) => create.mutate(body, {
            onSuccess: () => {
              notifySuccess(`${body.name} added`)
              done.onSuccess()
            },
          })}
          onUpdate={(id, body) => update.mutate({ id, body }, {
            onSuccess: () => {
              notifySuccess("Target updated")
              done.onSuccess()
            },
          })}
        />
      )}
    />
  )
}

// AnnounceForm creates or edits a target. The tool's API key is stored encrypted and
// never read back: on edit the field starts empty and is only sent when the operator
// types a replacement (omit = keep the stored key, per the API). Kind is fixed on edit.
function AnnounceForm({ existing, initialAppId, pending, error, onCreate, onUpdate }: {
  existing?: AnnounceConnection
  initialAppId?: number
  pending: boolean
  error: unknown
  onCreate: (body: CreateAnnounceConnection) => void
  onUpdate: (id: number, body: UpdateAnnounceConnection) => void
}) {
  const apps = useApps()

  const [name, setName] = useState(existing?.name ?? "")
  const [kind, setKind] = useState<AnnounceKind>(existing?.kind ?? "qui")
  // Create-only: which App backs this target. `null` means the operator hasn't chosen
  // yet, so the picker defaults to the first App of this kind once apps arrive
  // (effectiveAppSel below). NEW_APP reveals the inline baseUrl/apiKey/harbrrUrl fields;
  // anything else reuses that App's identity.
  const [appSel, setAppSel] = useState<string | null>(null)
  const [scheme, setScheme] = useState<"http" | "https">("http")
  const [host, setHost] = useState("")
  const [port, setPort] = useState(String(DEFAULT_PORTS[kind] ?? ""))
  const [apiKey, setApiKey] = useState("")
  const [harbrrUrl, setHarbrrUrl] = useState(defaultHarbrrUrl())

  // "Use as…" deep-link (autobrr/harbrr#300): pre-pick the App the same way
  // ConfiguredAppsBlock's onPick below does.
  useInitialAppPick(initialAppId, apps.data, (app) => {
    setKind(app.kind as AnnounceKind)
    setAppSel(String(app.id))
    setName((prev) => (prev === "" ? app.name : prev))
    setPort(String(DEFAULT_PORTS[app.kind] ?? ""))
  })

  const mode = existing ? "edit" : "create"
  const message = error instanceof Error ? error.message : null
  const appsOfKind = (apps.data ?? []).filter((a) => a.kind === kind)
  // Announce is one-row-per-App, so a used app is not offerable: the default skips it
  // and its picker option is disabled — otherwise it pre-selects a guaranteed 409.
  const isUsed = (a: App) => a.references.announce > 0
  const firstFree = appsOfKind.find((a) => !isUsed(a))
  const effectiveAppSel = appSel ?? (firstFree ? String(firstFree.id) : NEW_APP)
  const usingNewApp = effectiveAppSel === NEW_APP
  const configuredApps = (apps.data ?? []).filter((a) => a.kind === "qui" || a.kind === "crossseed-v6")

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        if (mode === "edit" && existing) {
          onUpdate(existing.id, { name })
        } else {
          onCreate({
            name, kind,
            ...(usingNewApp ? { baseUrl: composeHostURL(scheme, host, port), apiKey, harbrrUrl } : { appId: Number(effectiveAppSel) }),
          })
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

      {mode === "create" && (
        <ConfiguredAppsBlock
          apps={configuredApps}
          isUsed={isUsed}
          onPick={(a: App) => {
            setKind(a.kind as AnnounceKind)
            setAppSel(String(a.id))
            if (name === "") setName(a.name)
          }}
        />
      )}

      <div className="grid grid-cols-2 gap-3">
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="ann-name">Name</Label>
          <Input id="ann-name" value={name} onChange={(e) => setName(e.target.value)} />
        </span>
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="ann-kind">Kind</Label>
          <NativeSelect
            id="ann-kind"
            value={kind}
            disabled={mode === "edit"}
            onChange={(e) => {
              const next = e.target.value as AnnounceKind
              setKind(next)
              setAppSel(null) // the app list for the new kind is different; re-default.
              // A typed port for the OLD kind isn't meaningful for the new one.
              setPort(String(DEFAULT_PORTS[next] ?? ""))
            }}
          >
            <option value="qui">{kindLabel("qui")}</option>
            <option value="crossseed-v6">{kindLabel("crossseed-v6")}</option>
          </NativeSelect>
        </span>
      </div>

      {mode === "create" && (
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="ann-app">App</Label>
          <NativeSelect id="ann-app" value={effectiveAppSel} onChange={(e) => setAppSel(e.target.value)}>
            {appsOfKind.map((a) => (
              <option key={a.id} value={a.id} disabled={isUsed(a)}>
                {a.name} ({hostname(a.baseUrl)}){isUsed(a) ? " — already added" : ""}
              </option>
            ))}
            <option value={NEW_APP}>New app…</option>
          </NativeSelect>
        </span>
      )}

      {mode === "create" && !usingNewApp && (
        <ReusingAppHint app={appsOfKind.find((a) => String(a.id) === effectiveAppSel)} />
      )}

      {mode === "edit" && <ManagedByAppHint appId={existing?.appId} />}

      {mode === "create" && usingNewApp && (
        <>
          <HostPortFields idPrefix="ann" scheme={scheme} host={host} port={port} onScheme={setScheme} onHost={setHost} onPort={setPort} />
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="ann-key">Tool API key</Label>
            <Input id="ann-key" type="password" autoComplete="off" value={apiKey} onChange={(e) => setApiKey(e.target.value)} />
          </span>
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="ann-harbrr">harbrr URL as the tool reaches it</Label>
            <Input id="ann-harbrr" value={harbrrUrl} onChange={(e) => setHarbrrUrl(e.target.value)} />
          </span>
        </>
      )}

      <DialogFooter>
        <Button
          type="submit"
          disabled={
            pending || !name ||
            (mode === "create" && usingNewApp && (!host || !harbrrUrl || !apiKey))
          }
        >
          {pending ? "Saving…" : mode === "edit" ? "Save changes" : "Add target"}
        </Button>
      </DialogFooter>
    </form>
  )
}
