import { useEffect, useState } from "react"
import { useInitialAppPick } from "@/hooks/useInitialAppPick"
import { Pencil, Trash2 } from "lucide-react"
import { ConfiguredAppsBlock, ReusingAppHint } from "@/components/applications/ConfiguredApps"
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
import { HostPortFields } from "@/components/forms/HostPortFields"
import {
  useCreateDownloadClient,
  useDeleteDownloadClient,
  useDownloadClients,
  useSetDownloadClientEnabled,
  useTestDownloadClient,
  useUpdateDownloadClient
} from "@/hooks/useDownloadClients"
import { useApps, useQuiInstances } from "@/hooks/useApps"
import { hostname, kindLabel } from "@/lib/format"
import { composeHostPort, composeHostURL, DEFAULT_PORTS } from "@/lib/hosturl"
import { notifyError, notifySuccess } from "@/lib/notify"
import type { App, CreateDownloadClient, DownloadClient, DownloadClientKind, UpdateDownloadClient } from "@/lib/api"
import { KIND_SPEC } from "./kind-spec"
import type { AnyKindSpec, AnySettingsForm, SettingsForm } from "./kind-spec"

// Only kinds with a registered driver work today (autobrr/harbrr#240, #241,
// #242, #243, #244); the rest are seeded server-side but rejected on create
// until their own driver lands (autobrr/harbrr#8). Keep the picker limited to
// what actually works.
const DOWNLOAD_CLIENT_KINDS: DownloadClientKind[] = ["qbittorrent", "blackhole", "sabnzbd", "nzbget", "qui", "flood", "download-station", "transmission", "deluge", "rtorrent"]

// Sentinel select value for "no existing qui App picked, use the inline host/API key
// fields below" — the create-time fallback for the very first qui app.
const NEW_APP = "new"

// The form always produces a full CreateDownloadClient shape; kind is immutable
// on edit so an update just drops it before the PATCH goes out (kind isn't even
// a field UpdateDownloadClient accepts).
type FormBody = CreateDownloadClient

// Configured download clients harbrr can hand a grabbed release to. Host/username
// are plain (visible on read); only the secret (password/API key, depending on
// kind) is stored encrypted and rotates only when a new one is typed.
export function DownloadClientsSection({ initialCreate }: { initialCreate?: { appId: number } } = {}) {
  const clients = useDownloadClients()
  const create = useCreateDownloadClient()
  const update = useUpdateDownloadClient()
  const remove = useDeleteDownloadClient()
  const toggle = useSetDownloadClientEnabled()
  const test = useTestDownloadClient()
  // "Use as…" deep-link (autobrr/harbrr#300): the route hands the pick down as a prop;
  // the shell opens the add dialog (addRequest) while this section remembers which App
  // to pre-pick — cleared on any dialog close so a later manual add starts clean.
  const [pendingAppId, setPendingAppId] = useState<number | undefined>()
  useEffect(() => {
    if (initialCreate) setPendingAppId(initialCreate.appId)
  }, [initialCreate])

  return (
    <ResourceSection<DownloadClient>
      title="Download clients"
      addLabel="Add download client"
      query={clients}
      empty="No download clients. Add one to hand off grabbed releases."
      addRequest={initialCreate}
      onDialogClose={() => setPendingAppId(undefined)}
      row={(c, actions) => (
        <>
          <span className="font-medium">{c.name}</span>
          <Badge variant="secondary" className="px-1.5 py-0 text-[11px]">{c.kind}</Badge>
          <span className="text-muted-foreground">{c.host}</span>
          <span className="ml-auto flex items-center gap-1">
            <Switch
              aria-label={`${c.enabled ? "Disable" : "Enable"} ${c.name}`}
              checked={c.enabled}
              onCheckedChange={(checked) => toggle.mutate({ id: c.id, enabled: checked })}
            />
            <Button
              variant="outline"
              size="sm"
              disabled={test.isPending && test.variables === c.id}
              onClick={() => test.mutate(c.id, {
                onSuccess: (r) => r.ok ? notifySuccess("Connection OK") : notifyError(`Test failed — ${r.error ?? "unknown error"}`),
                onError: (err) => notifyError("Test request failed", err),
              })}
            >
              Test
            </Button>
            <Button variant="ghost" size="icon" aria-label={`Edit ${c.name}`} onClick={actions.edit}>
              <Pencil className="h-4 w-4" />
            </Button>
            <Button variant="ghost" size="icon" aria-label={`Delete ${c.name}`} onClick={actions.remove}>
              <Trash2 className="h-4 w-4" />
            </Button>
          </span>
        </>
      )}
      onDelete={(c) => remove.mutateAsync(c.id)}
      form={(target, done) => (
        <DownloadClientForm
          client={target}
          initialAppId={target === null ? pendingAppId : undefined}
          pending={create.isPending || update.isPending}
          onSubmit={(id, body) => {
            if (id === null) create.mutate(body, done)
            else {
              // Identity/credential (host, username, secret) are App-level now
              // (ADR 0004) — the update surface is name + settings only.
              const patch: UpdateDownloadClient = { name: body.name, settings: body.settings }
              update.mutate({ id, body: patch }, done)
            }
          }}
        />
      )}
    />
  )
}

function DownloadClientForm({ client, initialAppId, pending, onSubmit }: {
  client: DownloadClient | null
  initialAppId?: number
  pending: boolean
  onSubmit: (id: number | null, body: FormBody) => void
}) {
  const isEdit = client !== null
  const apps = useApps()
  const initialKind = client?.kind ?? "qbittorrent"

  const [name, setName] = useState(client?.name ?? "")
  const [kind, setKind] = useState<DownloadClientKind>(initialKind)
  // Create-only: which qui App backs this client. `null` means the operator hasn't
  // chosen yet, so the picker defaults to the first qui App once apps arrive
  // (effectiveAppSel below). NEW_APP reveals the inline host/API key fields (the
  // fallback for the very first qui app); anything else reuses that App's identity and
  // drives the instance dropdown instead of a typed id.
  const [appSel, setAppSel] = useState<string | null>(null)
  // Where the client lives + its credential, one object per hostMode's needs.
  const [identity, setIdentity] = useState({
    scheme: "http" as "http" | "https",
    host: client?.host ?? "",
    port: String(DEFAULT_PORTS[initialKind] ?? ""),
    username: client?.username ?? "",
    secret: "",
  })
  // The current kind's settings in form shape (KIND_SPEC.decode), reset to the new
  // kind's defaults on every kind switch so it always matches `kind`.
  const [settings, setSettings] = useState<AnySettingsForm>(() => KIND_SPEC[initialKind].decode(client?.settings))

  // `settings` tracks `kind` (see above) — a correlation TS can't carry through the
  // table lookup, so widen the spec and narrow the two settings shapes the identity
  // logic reads (blackhole's dirs, qui's instance) once here.
  const spec = KIND_SPEC[kind] as unknown as AnyKindSpec
  // qui and sabnzbd authenticate with a bare API key — no username. This drives the
  // field's visibility, the secret's label/width, AND what submit sends: deriving them
  // from one predicate is what keeps a username typed under another kind from riding
  // along on a switch to sabnzbd (identity survives a kind change; only port resets).
  const usesUsername = kind !== "qui" && kind !== "sabnzbd"
  const quiSettings = spec.hostMode === "app" ? (settings as SettingsForm<"qui">) : null
  const bhSettings = spec.hostMode === "none" ? (settings as SettingsForm<"blackhole">) : null

  const quiApps = (apps.data ?? []).filter((a) => a.kind === "qui")

  // "Use as…" deep-link (autobrr/harbrr#300): pre-pick the App the same way
  // ConfiguredAppsBlock's onPick below does. Download's reuse path only exists for
  // kind "qui" (AppsSection only offers this action for qui Apps), so kind is fixed.
  useInitialAppPick(initialAppId, quiApps, (app) => {
    setKind("qui")
    setAppSel(String(app.id))
    setSettings(quiSettings ? { ...quiSettings, instanceId: "" } : KIND_SPEC.qui.decode(undefined))
    setName((prev) => (prev === "" ? app.name : prev))
    setIdentity((i) => ({ ...i, port: String(DEFAULT_PORTS.qui ?? "") }))
  })

  // Defaults to the first qui App once apps arrive; NEW_APP outside kind "qui" (there's
  // no reuse path for the other kinds today).
  const effectiveAppSel = spec.hostMode === "app" ? (appSel ?? (quiApps[0] ? String(quiApps[0].id) : NEW_APP)) : NEW_APP
  const usingQuiApp = spec.hostMode === "app" && !isEdit && effectiveAppSel !== NEW_APP
  const quiInstances = useQuiInstances(usingQuiApp ? Number(effectiveAppSel) : null)
  // Edit never touches identity (host/instance are fixed or App-level now); create
  // needs a watch folder (blackhole), a picked instance (qui via an App), or a host.
  const identityValid = bhSettings
    ? bhSettings.torrentDir !== "" || bhSettings.nzbDir !== ""
    : isEdit || (usingQuiApp && quiSettings ? quiSettings.instanceId !== "" : identity.host !== "")

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        // On edit, an empty secret keeps the stored one (only a typed value rotates).
        // A "none" host kind (blackhole) has no network endpoint of its own — its host
        // must always be empty. "hostport" (Deluge's daemon RPC) is a bare "host:port"
        // address, not a URL; every other kind composes an absolute http(s) URL.
        // Picking an existing qui App reuses its identity — no host/username/secret.
        const composedHost = spec.hostMode === "hostport" ? composeHostPort(identity.host, identity.port) : composeHostURL(identity.scheme, identity.host, identity.port)
        const identityBody = usingQuiApp
          ? { appId: Number(effectiveAppSel) }
          : { host: spec.hostMode === "none" ? "" : composedHost, username: usesUsername ? identity.username : "", secret: isEdit ? (identity.secret || undefined) : identity.secret }
        onSubmit(client?.id ?? null, { name, kind, settings: spec.encode(settings), ...identityBody })
      }}
    >
      <DialogHeader>
        <DialogTitle>{isEdit ? "Edit download client" : "Add download client"}</DialogTitle>
        <DialogDescription>Host and username are visible; the secret is stored encrypted and never shown again.</DialogDescription>
      </DialogHeader>

      {!isEdit && (
        <ConfiguredAppsBlock
          apps={quiApps}
          onPick={(a: App) => {
            setKind("qui")
            setAppSel(String(a.id))
            // The re-default can land on a different qui app, so a kept instance id
            // could pair with an app it doesn't belong to — clear it (keeping any
            // typed qui settings; a pick from another kind starts at qui defaults).
            setSettings(quiSettings ? { ...quiSettings, instanceId: "" } : KIND_SPEC.qui.decode(undefined))
          }}
        />
      )}

      <div className="grid grid-cols-2 gap-3">
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="dlc-name">Name</Label>
          <Input id="dlc-name" value={name} onChange={(e) => setName(e.target.value)} />
        </span>
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="dlc-kind">Kind</Label>
          <NativeSelect
            id="dlc-kind"
            value={kind}
            disabled={isEdit}
            onChange={(e) => {
              const next = e.target.value as DownloadClientKind
              setKind(next)
              setAppSel(null) // the app list for the new kind is different; re-default.
              // Settings are per-kind: the new kind starts at its own defaults. This
              // also clears a picked instance id, which could otherwise pair with a
              // re-defaulted app it doesn't belong to.
              setSettings(KIND_SPEC[next].decode(undefined))
              // A typed port for the OLD kind isn't meaningful for the new one.
              setIdentity((i) => ({ ...i, port: String(DEFAULT_PORTS[next] ?? "") }))
            }}
          >
            {DOWNLOAD_CLIENT_KINDS.map((k) => <option key={k} value={k}>{kindLabel(k)}</option>)}
          </NativeSelect>
        </span>
      </div>
      {spec.hostMode === "app" && !isEdit && (
        <span className="flex flex-col gap-1.5">
          <Label htmlFor="dlc-qui-app">qui app</Label>
          <NativeSelect id="dlc-qui-app" value={effectiveAppSel} onChange={(e) => { setAppSel(e.target.value); setSettings(quiSettings ? { ...quiSettings, instanceId: "" } : KIND_SPEC.qui.decode(undefined)) }}>
            {quiApps.map((a) => <option key={a.id} value={a.id}>{a.name} ({hostname(a.baseUrl)})</option>)}
            <option value={NEW_APP}>New app…</option>
          </NativeSelect>
        </span>
      )}
      {usingQuiApp && (
        <ReusingAppHint
          app={quiApps.find((a) => String(a.id) === effectiveAppSel)}
          tail="pick an instance below"
        />
      )}
      {isEdit && spec.hostMode !== "none" && <ManagedByAppHint appId={client?.appId} />}
      {!isEdit && spec.hostMode !== "none" && !usingQuiApp && (
        <>
          <HostPortFields
            idPrefix="dlc"
            scheme={identity.scheme}
            host={identity.host}
            port={identity.port}
            onScheme={(scheme) => setIdentity((i) => ({ ...i, scheme }))}
            onHost={(host) => setIdentity((i) => ({ ...i, host }))}
            onPort={(port) => setIdentity((i) => ({ ...i, port }))}
            showScheme={spec.hostMode !== "hostport"}
          />
          <div className="grid grid-cols-2 gap-3">
            {usesUsername && (
              <span className="flex flex-col gap-1.5">
                <Label htmlFor="dlc-username">Username <span className="text-faint">(optional)</span></Label>
                <Input id="dlc-username" autoComplete="off" value={identity.username} onChange={(e) => setIdentity((i) => ({ ...i, username: e.target.value }))} />
              </span>
            )}
            <span className={`flex flex-col gap-1.5 ${usesUsername ? "" : "col-span-2"}`}>
              <Label htmlFor="dlc-secret">{usesUsername ? "Password" : "API key"}</Label>
              <Input id="dlc-secret" type="password" autoComplete="off" value={identity.secret} onChange={(e) => setIdentity((i) => ({ ...i, secret: e.target.value }))} />
            </span>
          </div>
        </>
      )}
      <spec.Fields s={settings} set={setSettings}>
        {quiSettings && (usingQuiApp ? (
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-instance-select">Instance</Label>
            <NativeSelect
              id="dlc-instance-select"
              value={quiSettings.instanceId}
              onChange={(e) => {
                setSettings({ ...quiSettings, instanceId: e.target.value })
                const picked = quiInstances.data?.instances?.find((i) => String(i.id) === e.target.value)
                if (picked) setName(picked.name)
              }}
            >
              <option value="">Select an instance…</option>
              {(quiInstances.data?.instances ?? []).map((i) => <option key={i.id} value={i.id}>{i.name}</option>)}
            </NativeSelect>
            {quiInstances.data && !quiInstances.data.ok && (
              <p className="text-[12px] text-bad">{quiInstances.data.error ?? "Couldn't reach qui"}</p>
            )}
          </span>
        ) : (
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="dlc-instance-id">Instance ID</Label>
            <Input id="dlc-instance-id" type="number" min={1} value={quiSettings.instanceId} onChange={(e) => setSettings({ ...quiSettings, instanceId: e.target.value })} />
          </span>
        ))}
      </spec.Fields>
      <DialogFooter>
        <Button type="submit" disabled={pending || !name || !identityValid}>
          {pending ? "Saving…" : isEdit ? "Save changes" : "Add download client"}
        </Button>
      </DialogFooter>
    </form>
  )
}
