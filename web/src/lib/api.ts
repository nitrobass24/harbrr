import { getApiBaseUrl, getBaseUrl } from "@/lib/base-url"
import type {
  AddIndexer,
  AnnounceConnection,
  ApiKey,
  AppConnection,
  CacheConfig,
  CacheConfigUpdate,
  CacheStats,
  Capabilities,
  ConnectionStatus,
  ConnectionSyncResult,
  CreateAnnounceConnection,
  CreateConnection,
  CreateNotification,
  CreateProxy,
  CreateSolver,
  CreateSyncProfile,
  CrossSeedSnippet,
  DefinitionDetail,
  DefinitionSummary,
  Health,
  Instance,
  InstanceDetail,
  IndexerStats,
  IndexerStatus,
  LogLevel,
  MintedApiKey,
  Notification,
  Proxy,
  SearchParams,
  SearchResults,
  Solver,
  SyncProfile,
  SyncReport,
  TestResult,
  UpdateConnection,
  UpdateIndexer,
  UpdateNotification,
  UpdateProxy,
  UpdateSolver,
  UpdateSyncProfile
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

// AUTH_BOOTSTRAP endpoints handle their own 401 inline (session probe, login,
// setup, logout), so they are exempt from the 401 hard-redirect that every other
// endpoint triggers. A protected action (e.g. change-password) is NOT here — its
// 401 means the session expired and should redirect.
const AUTH_BOOTSTRAP = new Set(["/auth/me", "/auth/login", "/auth/setup", "/auth/logout"])

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

  // requestAbsolute fetches a read-only path mounted beside /api (e.g. /healthz).
  private async requestAbsolute<T>(path: string): Promise<T> {
    const res = await fetch(`${getBaseUrl()}${path}`, { credentials: "same-origin" })
    if (!res.ok) throw await this.toError(res, path)
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
    // Exempt only the bootstrap/auth-screen probes from the 401 hard-redirect (their
    // 401 is handled inline, and redirecting would loop). A protected action like
    // change-password SHOULD redirect on a 401 — the session has expired.
    if (res.status === 401 && !AUTH_BOOTSTRAP.has(endpoint) && !onAuthScreen) {
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

  // --- settings surfaces ---

  getHealth(): Promise<Health> {
    // /healthz lives beside /api, not under it.
    return this.requestAbsolute("/healthz")
  }

  changePassword(currentPassword: string, newPassword: string): Promise<void> {
    return this.request("/auth/change-password", { method: "POST", body: { currentPassword, newPassword } })
  }

  listApiKeys(): Promise<ApiKey[]> {
    return this.request("/apikeys")
  }

  mintApiKey(name: string): Promise<MintedApiKey> {
    return this.request("/apikeys", { method: "POST", body: { name } })
  }

  revokeApiKey(id: number): Promise<void> {
    return this.request(`/apikeys/${id}`, { method: "DELETE" })
  }

  listNotifications(): Promise<Notification[]> {
    return this.request("/notifications")
  }

  createNotification(body: CreateNotification): Promise<Notification> {
    return this.request("/notifications", { method: "POST", body })
  }

  updateNotification(id: number, body: UpdateNotification): Promise<Notification> {
    return this.request(`/notifications/${id}`, { method: "PATCH", body })
  }

  deleteNotification(id: number): Promise<void> {
    return this.request(`/notifications/${id}`, { method: "DELETE" })
  }

  setNotificationEnabled(id: number, enabled: boolean): Promise<void> {
    return this.request(`/notifications/${id}/${enabled ? "enable" : "disable"}`, { method: "POST" })
  }

  testNotification(id: number): Promise<TestResult> {
    return this.request(`/notifications/${id}/test`, { method: "POST" })
  }

  // --- proxies ---

  listProxies(): Promise<Proxy[]> {
    return this.request("/proxies")
  }

  createProxy(body: CreateProxy): Promise<Proxy> {
    return this.request("/proxies", { method: "POST", body })
  }

  updateProxy(id: number, body: UpdateProxy): Promise<void> {
    return this.request(`/proxies/${id}`, { method: "PATCH", body })
  }

  deleteProxy(id: number): Promise<void> {
    return this.request(`/proxies/${id}`, { method: "DELETE" })
  }

  // --- solvers ---

  listSolvers(): Promise<Solver[]> {
    return this.request("/solvers")
  }

  createSolver(body: CreateSolver): Promise<Solver> {
    return this.request("/solvers", { method: "POST", body })
  }

  updateSolver(id: number, body: UpdateSolver): Promise<void> {
    return this.request(`/solvers/${id}`, { method: "PATCH", body })
  }

  deleteSolver(id: number): Promise<void> {
    return this.request(`/solvers/${id}`, { method: "DELETE" })
  }

  getCacheStats(): Promise<CacheStats> {
    return this.request("/cache/stats")
  }

  flushCache(): Promise<{ flushed: number }> {
    return this.request("/cache/flush", { method: "POST" })
  }

  getCacheConfig(): Promise<CacheConfig> {
    return this.request("/cache/config")
  }

  updateCacheConfig(body: CacheConfigUpdate): Promise<CacheConfig> {
    return this.request("/cache/config", { method: "PUT", body })
  }

  getLogLevel(): Promise<{ level: LogLevel }> {
    return this.request("/config/log-level")
  }

  setLogLevel(level: LogLevel): Promise<{ level: LogLevel }> {
    return this.request("/config/log-level", { method: "PUT", body: { level } })
  }

  listAllIndexerStats(): Promise<IndexerStats[]> {
    return this.request("/indexers/stats")
  }

  // --- app connections (sync targets) ---

  listConnections(): Promise<AppConnection[]> {
    return this.request("/app-connections")
  }

  createConnection(body: CreateConnection): Promise<AppConnection> {
    return this.request("/app-connections", { method: "POST", body })
  }

  getConnection(id: number): Promise<AppConnection> {
    return this.request(`/app-connections/${id}`)
  }

  updateConnection(id: number, body: UpdateConnection): Promise<AppConnection> {
    return this.request(`/app-connections/${id}`, { method: "PATCH", body })
  }

  deleteConnection(id: number): Promise<void> {
    return this.request(`/app-connections/${id}`, { method: "DELETE" })
  }

  setConnectionEnabled(id: number, enabled: boolean): Promise<void> {
    return this.request(`/app-connections/${id}/${enabled ? "enable" : "disable"}`, { method: "POST" })
  }

  testConnection(id: number): Promise<TestResult> {
    return this.request(`/app-connections/${id}/test`, { method: "POST" })
  }

  syncConnection(id: number): Promise<SyncReport> {
    return this.request(`/app-connections/${id}/sync`, { method: "POST" })
  }

  syncAllConnections(): Promise<ConnectionSyncResult[]> {
    return this.request("/app-connections/sync", { method: "POST" })
  }

  getConnectionStatus(id: number): Promise<ConnectionStatus> {
    return this.request(`/app-connections/${id}/status`)
  }

  setSelectedIndexers(id: number, instanceIds: number[]): Promise<void> {
    return this.request(`/app-connections/${id}/indexers`, { method: "PUT", body: { instanceIds } })
  }

  // --- sync profiles (per-connection category/toggle overrides) ---

  listSyncProfiles(): Promise<SyncProfile[]> {
    return this.request("/sync-profiles")
  }

  createSyncProfile(body: CreateSyncProfile): Promise<SyncProfile> {
    return this.request("/sync-profiles", { method: "POST", body })
  }

  updateSyncProfile(id: number, body: UpdateSyncProfile): Promise<SyncProfile> {
    return this.request(`/sync-profiles/${id}`, { method: "PATCH", body })
  }

  deleteSyncProfile(id: number): Promise<void> {
    return this.request(`/sync-profiles/${id}`, { method: "DELETE" })
  }

  // --- announce connections (cross-seed push targets) ---

  listAnnounceConnections(): Promise<AnnounceConnection[]> {
    return this.request("/announce-connections")
  }

  createAnnounceConnection(body: CreateAnnounceConnection): Promise<AnnounceConnection> {
    return this.request("/announce-connections", { method: "POST", body })
  }

  deleteAnnounceConnection(id: number): Promise<void> {
    return this.request(`/announce-connections/${id}`, { method: "DELETE" })
  }

  setAnnounceEnabled(id: number, enabled: boolean): Promise<void> {
    return this.request(`/announce-connections/${id}/${enabled ? "enable" : "disable"}`, { method: "POST" })
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
