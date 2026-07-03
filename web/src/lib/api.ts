import { getApiBaseUrl, getBaseUrl } from "@/lib/base-url"
import type {
  AddIndexer,
  Capabilities,
  CrossSeedSnippet,
  DefinitionDetail,
  DefinitionSummary,
  Instance,
  InstanceDetail,
  IndexerStats,
  IndexerStatus,
  SearchParams,
  SearchResults,
  TestResult,
  UpdateIndexer
} from "@/types/api"

// APIError carries the server's error envelope ({error, code}) plus the HTTP
// status, so callers branch on `code` (e.g. "invalid_credentials"), never on
// message text.
export class APIError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = "APIError"
    this.status = status
    this.code = code
  }
}

export type Me = {
  username: string
  authMethod: string
  csrfToken?: string
}

export type SetupState = {
  setupComplete: boolean
}

export type Credentials = {
  username: string
  password: string
}

type RequestOptions = {
  method?: string
  body?: unknown
}

const MUTATING = new Set(["POST", "PUT", "PATCH", "DELETE"])

// readCsrfCookie returns the non-HttpOnly CSRF companion cookie the server
// sets at login (internal/web/api/csrf.go), or "" when absent.
function readCsrfCookie(): string {
  const match = document.cookie.match(/(?:^|;\s*)harbrr_csrf=([^;]*)/)
  return match ? decodeURIComponent(match[1]) : ""
}

// ApiClient is the single choke point every management call goes through:
// base-path prefixing, CSRF header injection on mutations, the {error, code}
// envelope parsed into APIError, and the 401 hard-redirect to /login (skipped
// for auth endpoints and on the login/setup screens, so bootstrap probes and
// failed logins cannot loop). NEVER log request or response payloads here —
// settings bodies carry tracker credentials.
export class ApiClient {
  private csrfToken = ""

  // onUnauthorized is replaceable for tests; the default is a full-page
  // navigation so all client state resets.
  onUnauthorized: () => void = () => {
    window.location.assign(`${getBaseUrl()}/login`)
  }

  setCsrfToken(token: string | undefined) {
    this.csrfToken = token ?? ""
  }

  private csrf(): string {
    return readCsrfCookie() || this.csrfToken
  }

  private async request<T>(endpoint: string, options: RequestOptions = {}): Promise<T> {
    const method = options.method ?? "GET"
    const headers: Record<string, string> = {}
    if (options.body !== undefined) headers["Content-Type"] = "application/json"
    if (MUTATING.has(method)) {
      const token = this.csrf()
      if (token !== "") headers["X-CSRF-Token"] = token
    }

    const res = await fetch(`${getApiBaseUrl()}${endpoint}`, {
      method,
      headers,
      credentials: "same-origin",
      body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
    })

    if (!res.ok) {
      throw await this.toError(res, endpoint)
    }
    if (res.status === 204) return undefined as T
    return res.json() as Promise<T>
  }

  private async toError(res: Response, endpoint: string): Promise<APIError> {
    let code = "internal"
    let message = res.statusText
    try {
      const body = (await res.json()) as { error?: string, code?: string }
      if (body.code) code = body.code
      if (body.error) message = body.error
    } catch {
      // non-JSON error body: keep the status text
    }
    const onAuthScreen = ["/login", "/setup"].includes(window.location.pathname.replace(getBaseUrl(), ""))
    if (res.status === 401 && !endpoint.startsWith("/auth/") && !onAuthScreen) {
      this.onUnauthorized()
    }
    return new APIError(res.status, code, message)
  }

  // --- auth ---

  async getMe(): Promise<Me> {
    const me = await this.request<Me>("/auth/me")
    this.setCsrfToken(me.csrfToken)
    return me
  }

  getSetup(): Promise<SetupState> {
    return this.request("/auth/setup")
  }

  setup(creds: Credentials): Promise<{ username: string }> {
    return this.request("/auth/setup", { method: "POST", body: creds })
  }

  login(creds: Credentials): Promise<void> {
    return this.request("/auth/login", { method: "POST", body: creds })
  }

  logout(): Promise<void> {
    return this.request("/auth/logout", { method: "POST" })
  }

  // --- definitions ---

  listDefinitions(): Promise<DefinitionSummary[]> {
    return this.request("/definitions")
  }

  getDefinition(id: string): Promise<DefinitionDetail> {
    return this.request(`/definitions/${encodeURIComponent(id)}`)
  }

  // --- indexers ---

  listIndexers(): Promise<Instance[]> {
    return this.request("/indexers")
  }

  addIndexer(body: AddIndexer): Promise<Instance> {
    return this.request("/indexers", { method: "POST", body })
  }

  getIndexer(slug: string): Promise<InstanceDetail> {
    return this.request(`/indexers/${encodeURIComponent(slug)}`)
  }

  updateIndexer(slug: string, body: UpdateIndexer): Promise<Instance> {
    return this.request(`/indexers/${encodeURIComponent(slug)}`, { method: "PATCH", body })
  }

  deleteIndexer(slug: string): Promise<void> {
    return this.request(`/indexers/${encodeURIComponent(slug)}`, { method: "DELETE" })
  }

  setIndexerEnabled(slug: string, enabled: boolean): Promise<void> {
    return this.request(`/indexers/${encodeURIComponent(slug)}/${enabled ? "enable" : "disable"}`, { method: "POST" })
  }

  testIndexer(slug: string): Promise<TestResult> {
    return this.request(`/indexers/${encodeURIComponent(slug)}/test`, { method: "POST" })
  }

  getIndexerStatus(slug: string): Promise<IndexerStatus> {
    return this.request(`/indexers/${encodeURIComponent(slug)}/status`)
  }

  getIndexerStats(slug: string): Promise<IndexerStats> {
    return this.request(`/indexers/${encodeURIComponent(slug)}/stats`)
  }

  getIndexerCapabilities(slug: string): Promise<Capabilities> {
    return this.request(`/indexers/${encodeURIComponent(slug)}/capabilities`)
  }

  getCrossseedSnippet(slug: string): Promise<CrossSeedSnippet> {
    return this.request(`/indexers/${encodeURIComponent(slug)}/crossseed-snippet`)
  }

  // --- search ---

  searchIndexer(slug: string, params: SearchParams): Promise<SearchResults> {
    const qs = new URLSearchParams()
    for (const [key, value] of Object.entries(params)) {
      if (value !== undefined && value !== "") qs.set(key, String(value))
    }
    const suffix = qs.size > 0 ? `?${qs.toString()}` : ""
    return this.request(`/indexers/${encodeURIComponent(slug)}/search${suffix}`)
  }
}

export const api = new ApiClient()
