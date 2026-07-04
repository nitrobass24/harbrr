import { useState } from "react"
import { ChevronRight } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { NativeSelect } from "@/components/ui/native-select"
import { SettingFieldInput } from "@/components/indexers/form/SettingFieldInput"
import { defaultValues, isInfoField, settingsPayload } from "@/components/indexers/form/settings-payload"
import { useProxies, useSolvers } from "@/hooks/useResources"
import { APIError } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { AddIndexer, DefinitionDetail, InstanceDetail, SettingField, UpdateIndexer } from "@/types/api"

// The one reserved engine setting still entered inline. Proxy + FlareSolverr are
// now global resources referenced by id (proxy_type/proxy_url, solver_type=
// flaresolverr/flaresolverr_url are managed on the Proxies & Solvers page), and
// the manual-cookie solver's cookie is entered under the solver control below.
const TIMEOUT_FIELD: SettingField = {
  name: "timeout", label: "Request timeout (Go duration, e.g. 30s)", type: "text", secret: false,
}

// Inline settings the proxy/solver controls own — stripped from the schema-driven
// map so they are never double-submitted (proxy rides proxyId, FlareSolverr rides
// solverId, solver_type is set explicitly on submit). NOTE: `cookie` is NOT here —
// a cookie-login definition declares its own `cookie` credential field, which must
// render/submit normally; the manual-cookie SOLVER only manages `cookie` for a
// definition that does not (see managesCookie below).
const MANAGED_KEYS = ["proxy_type", "proxy_url", "solver_type", "flaresolverr_url", "flaresolverr_max_timeout"]

export type IndexerFormSubmit =
  | { mode: "create", body: AddIndexer }
  | { mode: "edit", body: UpdateIndexer }

// none = no solver; cookie = inline manual-cookie; a number = a global solver id.
type SolverChoice = "none" | "cookie" | number

// Dual-use create/edit form: defaults come from the definition schema, with
// stored settings layered on top in edit mode (secrets prefilled with the
// <redacted> sentinel and PATCHed back verbatim when untouched). Proxy + solver
// are chosen from the global resources; a manual cookie stays inline.
export function IndexerForm({ definition, existing, pending, error, onSubmit }: {
  definition: DefinitionDetail
  existing?: InstanceDetail
  pending: boolean
  error: unknown
  onSubmit: (submit: IndexerFormSubmit) => void
}) {
  const mode = existing ? "edit" : "create"
  const proxies = useProxies()
  const solvers = useSolvers()

  // When the definition declares its own `cookie` field (cookie-login trackers),
  // that field owns the cookie; the manual-cookie solver only manages `cookie`
  // for definitions that do not, so the two never fight over the key.
  const definesCookie = definition.settings.some((f) => f.name === "cookie")

  const [name, setName] = useState(existing?.name ?? definition.name)
  const [slug, setSlug] = useState(existing?.slug ?? definition.id)
  const [baseUrl, setBaseUrl] = useState(existing?.baseUrl ?? "")
  const [values, setValues] = useState<Record<string, string>>(() => {
    const seeded = { ...defaultValues([TIMEOUT_FIELD]), ...defaultValues(definition.settings, existing?.settings) }
    for (const k of MANAGED_KEYS) delete seeded[k]
    if (!definesCookie) delete seeded.cookie // solver-managed manual cookie, controlled below
    return seeded
  })
  const [proxyId, setProxyId] = useState<number | null>(existing?.proxyId ?? null)
  const [solver, setSolver] = useState<SolverChoice>(() => initialSolver(existing))
  const [cookie, setCookie] = useState(definesCookie ? "" : (existing?.settings.find((s) => s.name === "cookie")?.value ?? ""))
  const [showAdvanced, setShowAdvanced] = useState(false)

  const setValue = (fieldName: string) => (value: string) =>
    setValues((prev) => ({ ...prev, [fieldName]: value }))

  const slugConflict = error instanceof APIError && error.code === "conflict"
  const message = slugConflict ? null : error instanceof Error ? error.message : null

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        const settings = settingsPayload(values, mode)
        if (solver === "cookie") {
          settings.solver_type = "manual_cookie"
          if (!definesCookie) settings.cookie = cookie
        }
        // On edit, mergeSettings keeps any inline setting we omit — so a control
        // that now owns a key must send "" to actually clear a stale stored value
        // (proxy is via proxyId, FlareSolverr via solverId; switching the solver to
        // None/FlareSolverr must turn off an old manual cookie).
        if (mode === "edit") {
          settings.proxy_type = ""
          settings.proxy_url = ""
          settings.flaresolverr_url = ""
          settings.flaresolverr_max_timeout = ""
          if (solver !== "cookie") {
            settings.solver_type = ""
            if (!definesCookie) settings.cookie = ""
          }
        }
        const solverId = typeof solver === "number" ? solver : null
        if (mode === "edit") {
          // baseUrl verbatim so clearing it ("") clears the stored override.
          onSubmit({ mode, body: { name, baseUrl, settings, proxyId, solverId } })
        } else {
          onSubmit({ mode, body: { slug, definitionId: definition.id, name, baseUrl: baseUrl || undefined, settings, proxyId, solverId } })
        }
      }}
    >
      {message && (
        <p className="rounded-md border border-bad/40 bg-bad/10 px-3 py-2 text-[13px] text-bad">{message}</p>
      )}

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="ix-name">Name</Label>
        <Input id="ix-name" value={name} onChange={(e) => setName(e.target.value)} />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="ix-slug">Slug</Label>
        <Input
          id="ix-slug"
          value={slug}
          disabled={mode === "edit"}
          aria-invalid={slugConflict}
          onChange={(e) => setSlug(e.target.value)}
        />
        {slugConflict && <p className="text-[12px] text-bad">An indexer with this slug already exists — pick another.</p>}
        {mode === "create" && !slugConflict && (
          <p className="text-[12px] text-faint">Feed URLs embed the slug; it cannot change later.</p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="ix-baseurl">Base URL (optional override)</Label>
        <Input id="ix-baseurl" placeholder="https://…" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
      </div>

      {definition.settings.map((field) => (
        <SettingFieldInput
          key={field.name}
          field={field}
          value={isInfoField(field) ? "" : values[field.name] ?? ""}
          onChange={setValue(field.name)}
        />
      ))}

      <button
        type="button"
        className="flex cursor-pointer items-center gap-1 text-[13px] font-medium text-muted-foreground transition hover:text-foreground"
        onClick={() => setShowAdvanced((v) => !v)}
      >
        <ChevronRight className={cn("h-3.5 w-3.5 transition-transform", showAdvanced && "rotate-90")} />
        Advanced (proxy, timeout, anti-bot solver)
      </button>
      {showAdvanced && (
        <div className="flex flex-col gap-4 rounded-md border border-border p-3">
          <span className="flex flex-col gap-1.5">
            <Label htmlFor="ix-proxy">Proxy</Label>
            <NativeSelect id="ix-proxy" value={proxyId === null ? "" : String(proxyId)} onChange={(e) => setProxyId(e.target.value === "" ? null : Number(e.target.value))}>
              <option value="">No proxy</option>
              {(proxies.data ?? []).map((p) => <option key={p.id} value={p.id}>{p.name} ({p.type})</option>)}
            </NativeSelect>
          </span>

          <SettingFieldInput field={TIMEOUT_FIELD} value={values.timeout ?? ""} onChange={setValue("timeout")} />

          <span className="flex flex-col gap-1.5">
            <Label htmlFor="ix-solver">Anti-bot solver</Label>
            <NativeSelect id="ix-solver" value={solverValue(solver)} onChange={(e) => setSolver(parseSolver(e.target.value))}>
              <option value="none">None</option>
              <option value="cookie">Manual cookie</option>
              {(solvers.data ?? []).map((s) => <option key={s.id} value={s.id}>{s.name} (FlareSolverr)</option>)}
            </NativeSelect>
            <p className="text-[12px] text-faint">Manage FlareSolverr endpoints on the Proxies &amp; Solvers page.</p>
          </span>

          {solver === "cookie" && !definesCookie && (
            <span className="flex flex-col gap-1.5">
              <Label htmlFor="ix-cookie">Cookie</Label>
              <Input id="ix-cookie" type="password" autoComplete="off" placeholder="cf_clearance=…; other=…" value={cookie} onChange={(e) => setCookie(e.target.value)} />
            </span>
          )}
        </div>
      )}

      <Button type="submit" disabled={pending || name === "" || (mode === "create" && slug === "")}>
        {pending ? "Saving…" : mode === "edit" ? "Save changes" : "Add indexer"}
      </Button>
    </form>
  )
}

// initialSolver derives the solver control's value from the stored instance: a
// referenced global solver wins, else an inline manual_cookie, else none.
function initialSolver(existing?: InstanceDetail): SolverChoice {
  if (existing?.solverId != null) return existing.solverId
  if (existing?.settings.some((s) => s.name === "solver_type" && s.value === "manual_cookie")) return "cookie"
  return "none"
}

function solverValue(choice: SolverChoice): string {
  return typeof choice === "number" ? String(choice) : choice
}

function parseSolver(value: string): SolverChoice {
  if (value === "none" || value === "cookie") return value
  return Number(value)
}

// exported for the picker step
export function DefinitionOption({ id, name, type, description, onPick }: {
  id: string
  name: string
  type?: string
  description?: string
  onPick: (id: string) => void
}) {
  return (
    <button
      type="button"
      onClick={() => onPick(id)}
      className="flex w-full cursor-pointer flex-col gap-0.5 rounded-md px-3 py-2 text-left transition hover:bg-accent"
    >
      <span className="flex items-center gap-2 text-[13px] font-medium">
        {name}
        {type && <span className="text-[11px] text-faint">{type}</span>}
      </span>
      {description && <span className="line-clamp-1 text-[12px] text-muted-foreground">{description}</span>}
    </button>
  )
}
