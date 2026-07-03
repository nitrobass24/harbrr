// Hand-written mirrors of the management API components (openapi.yaml).

export type Instance = {
  slug: string
  definitionId: string
  name: string
  baseUrl?: string
  enabled: boolean
  createdAt: string
  updatedAt: string
}

export type Setting = {
  name: string
  value: string // secret values are the <redacted> sentinel
  secret: boolean
}

export type InstanceDetail = Instance & {
  settings: Setting[]
}

export type DefinitionSummary = {
  id: string
  name: string
  description?: string
  type?: string // private | public | semi-private
  language?: string
}

export type SettingField = {
  name: string
  label?: string
  type: string // text | password | checkbox | select | multi-select | info*
  default?: string
  options?: Record<string, string>
  secret: boolean
}

export type Category = {
  id: number
  name: string
  isCustom: boolean
  isParent: boolean
  parent?: string
}

export type Capabilities = {
  modes: Record<string, string[]>
  allowRawSearch?: boolean
  allowTVSearchIMDB?: boolean
  categories?: Category[]
  defaultCategories?: string[]
}

export type DefinitionDetail = DefinitionSummary & {
  settings: SettingField[]
  caps: Capabilities
}

export type AddIndexer = {
  slug?: string
  definitionId: string
  name?: string
  baseUrl?: string
  settings?: Record<string, string>
}

export type UpdateIndexer = {
  name?: string
  baseUrl?: string
  settings?: Record<string, string>
}

export type HealthEvent = {
  kind: "auth_failure" | "rate_limited" | "parse_error" | "anti_bot"
  detail?: string
  occurred_at: string
}

export type IndexerStatus = {
  slug: string
  status: "healthy" | "unhealthy"
  events: HealthEvent[]
}

export type IndexerStats = {
  slug: string
  queries: number
  grabs: number
  avgResponseMs?: number
  failures?: number
  lastQueryAt?: string
  lastFailureAt?: string
}

export type TestResult = {
  ok: boolean
  error?: string
}

export type CrossSeedSnippet = {
  indexer: string
  feedUrl: string
  configJs: string
}

// The keep-stored sentinel for secret settings (see openapi.yaml Setting).
export const REDACTED = "<redacted>"
