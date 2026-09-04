import { onTestFinished } from "vitest"
import { api, createApi, type ApiClient } from "@/lib/api"
import { getBaseUrl } from "@/lib/base-url"

// A stub value is either the JSON body the endpoint answers with (as a 200), or a
// function building the full Response (for status codes / error envelopes):
//   stubApi({ "GET /api/apps": [app], "POST /api/import": () => Response.json({ error: "x", code: "y" }, { status: 409 }) })
type StubResponder = (request: Request) => Response | Promise<Response>

// UnstubbedRequestError makes a request nothing stubbed FAIL the test instead of
// silently falling through — the failure mode url.includes-routing suffered from.
export class UnstubbedRequestError extends Error {
  constructor(request: Request, path: string, keys: string[]) {
    super(`unstubbed request: ${request.method} ${path} — stubbed: ${keys.join(", ")}`)
    this.name = "UnstubbedRequestError"
  }
}

// stubApi builds an ApiClient whose transport answers from `stubs`, keyed by
// "VERB /api/schema/{path}" in the OpenAPI schemaPath spelling ({param} segments
// match any value). The stub injects at the fetch level, NOT via an onRequest
// short-circuit, so the whole middleware pipeline still runs: CSRF injection and
// the 401 hard-redirect behave exactly as in production. Sent requests are
// recorded per key for body/header assertions via `calls`.
export function stubApi(stubs: Record<string, unknown>): ApiClient & { calls: (key: string) => Request[] } {
  const routes = Object.entries(stubs).map(([key, value]) => {
    const [method, path] = key.split(" ")
    return { key, method, pattern: new RegExp(`^${path.replace(/\{[^}]+\}/g, "[^/]+")}$`), value }
  })
  const recorded: { key: string, request: Request }[] = []
  const fetchStub = async (request: Request): Promise<Response> => {
    const path = new URL(request.url).pathname.slice(getBaseUrl().length)
    const route = routes.find((r) => r.method === request.method && r.pattern.test(path))
    if (!route) throw new UnstubbedRequestError(request, path, Object.keys(stubs))
    recorded.push({ key: route.key, request })
    if (typeof route.value === "function") return (route.value as StubResponder)(request)
    return Response.json(route.value)
  }
  // Component tests reach the API through the module singleton (the hooks import
  // `api` directly), so the stub transport is swapped onto it for the duration of
  // the current test and restored automatically. The returned fresh client shares
  // the same transport, for tests that call methods directly.
  const previous = api.fetchFn
  api.fetchFn = fetchStub
  onTestFinished(() => {
    api.fetchFn = previous
  })
  return Object.assign(createApi(fetchStub), {
    calls: (key: string) => recorded.filter((r) => r.key === key).map((r) => r.request),
  })
}
