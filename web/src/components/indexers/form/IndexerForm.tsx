import { useState } from "react"
import { ChevronRight } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { SettingFieldInput } from "@/components/indexers/form/SettingFieldInput"
import { defaultValues, isInfoField, settingsPayload } from "@/components/indexers/form/settings-payload"
import { APIError } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { AddIndexer, DefinitionDetail, InstanceDetail, SettingField, UpdateIndexer } from "@/types/api"

// The engine's reserved settings (openapi.yaml ReservedSettings) — not part of
// any definition's schema, offered under "Advanced" on every indexer.
const RESERVED_FIELDS: SettingField[] = [
  { name: "proxy_type", label: "Proxy type", type: "select", secret: false, options: { "": "none", http: "http", https: "https", socks5: "socks5", socks5h: "socks5h" }, default: "" },
  { name: "proxy_url", label: "Proxy URL (may embed user:pass)", type: "password", secret: true },
  { name: "timeout", label: "Request timeout (Go duration, e.g. 30s)", type: "text", secret: false },
  { name: "solver_type", label: "Anti-bot solver", type: "select", secret: false, options: { "": "none", manual_cookie: "manual cookie", flaresolverr: "FlareSolverr" }, default: "" },
  { name: "flaresolverr_url", label: "FlareSolverr URL (base, e.g. http://host:8191 — no /v1)", type: "text", secret: false },
  { name: "flaresolverr_max_timeout", label: "FlareSolverr maxTimeout (seconds)", type: "text", secret: false },
]

export type IndexerFormSubmit =
  | { mode: "create", body: AddIndexer }
  | { mode: "edit", body: UpdateIndexer }

// Dual-use create/edit form: defaults come from the definition schema, with
// stored settings layered on top in edit mode (secrets prefilled with the
// <redacted> sentinel and PATCHed back verbatim when untouched).
export function IndexerForm({ definition, existing, pending, error, onSubmit }: {
  definition: DefinitionDetail
  existing?: InstanceDetail
  pending: boolean
  error: unknown
  onSubmit: (submit: IndexerFormSubmit) => void
}) {
  const mode = existing ? "edit" : "create"
  const [name, setName] = useState(existing?.name ?? definition.name)
  const [slug, setSlug] = useState(existing?.slug ?? definition.id)
  const [baseUrl, setBaseUrl] = useState(existing?.baseUrl ?? "")
  const [values, setValues] = useState<Record<string, string>>(() => {
    const reserved = defaultValues(RESERVED_FIELDS)
    return { ...reserved, ...defaultValues(definition.settings, existing?.settings) }
  })
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
        if (mode === "edit") {
          // Send baseUrl verbatim so clearing the field ("") actually clears the
          // stored override; `|| undefined` would drop it and keep the old value.
          onSubmit({ mode, body: { name, baseUrl, settings } })
        } else {
          onSubmit({ mode, body: { slug, definitionId: definition.id, name, baseUrl: baseUrl || undefined, settings } })
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
          {RESERVED_FIELDS.map((field) => {
            if (field.name.startsWith("flaresolverr") && values.solver_type !== "flaresolverr") return null
            if (field.name === "proxy_url" && values.proxy_type === "") return null
            return (
              <SettingFieldInput key={field.name} field={field} value={values[field.name] ?? ""} onChange={setValue(field.name)} />
            )
          })}
        </div>
      )}

      <Button type="submit" disabled={pending || name === "" || (mode === "create" && slug === "")}>
        {pending ? "Saving…" : mode === "edit" ? "Save changes" : "Add indexer"}
      </Button>
    </form>
  )
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

