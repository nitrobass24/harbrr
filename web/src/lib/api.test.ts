import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { ApiClient, APIError } from "./api"

function jsonResponse(status: number, body?: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
  })
}

describe("ApiClient", () => {
  let client: ApiClient
  let fetchMock: ReturnType<typeof vi.fn>
  let unauthorized: ReturnType<typeof vi.fn<() => void>>

  beforeEach(() => {
    unauthorized = vi.fn<() => void>()
    fetchMock = vi.fn().mockResolvedValue(jsonResponse(204))
    // Stub the global before constructing the client: the client's fetch indirection
    // reads globalThis.fetch at call time, but stubbing first keeps this in step with
    // how the app actually boots (module load happens after index.html sets up fetch).
    vi.stubGlobal("fetch", fetchMock)
    client = new ApiClient()
    client.onUnauthorized = unauthorized
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    document.cookie = "harbrr_csrf=; expires=Thu, 01 Jan 1970 00:00:00 GMT"
  })

  // openapi-fetch dispatches `fetch(request: Request, requestInitExt?)`, not
  // `fetch(url, init)`, so assertions read the sent Request's own headers/method/url.
  function sentRequest(callIndex = 0): Request {
    return fetchMock.mock.calls[callIndex][0] as Request
  }

  it("sends X-CSRF-Token on mutations from the companion cookie", async () => {
    document.cookie = "harbrr_csrf=tok-from-cookie"
    await client.logout()
    expect(sentRequest().headers.get("X-CSRF-Token")).toBe("tok-from-cookie")
  })

  it("falls back to the me-payload token when the cookie is absent", async () => {
    client.setCsrfToken("tok-from-me")
    await client.logout()
    expect(sentRequest().headers.get("X-CSRF-Token")).toBe("tok-from-me")
  })

  it("omits the CSRF header entirely when no token exists (auth disabled)", async () => {
    await client.logout()
    expect(sentRequest().headers.get("X-CSRF-Token")).toBeNull()
  })

  it("never sends the CSRF header on reads", async () => {
    document.cookie = "harbrr_csrf=tok-from-cookie"
    fetchMock.mockResolvedValue(jsonResponse(200, { setupComplete: true }))
    await client.getSetup()
    expect(sentRequest().headers.get("X-CSRF-Token")).toBeNull()
  })

  it("parses the error envelope into APIError with the machine code", async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, { error: "wrong credentials", code: "invalid_credentials" }))
    const err = await client.login({ username: "a", password: "b" }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(APIError)
    expect((err as APIError).code).toBe("invalid_credentials")
    expect((err as APIError).status).toBe(401)
  })

  it("does not redirect on a 401 from an auth endpoint", async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, { error: "no session", code: "unauthorized" }))
    await client.getMe().catch(() => undefined)
    expect(unauthorized).not.toHaveBeenCalled()
  })

  it("redirects on a 401 from a non-auth endpoint", async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, { error: "no session", code: "unauthorized" }))
    await client
      .logout()
      .catch(() => undefined)
    expect(unauthorized).not.toHaveBeenCalled() // /auth/logout is an auth endpoint

    fetchMock.mockResolvedValue(jsonResponse(401, { error: "no session", code: "unauthorized" }))
    // Any non-auth resource call goes through the same 401 handling.
    const err = await client.listIndexers().catch((e: unknown) => e)
    expect(err).toBeInstanceOf(APIError)
    expect(unauthorized).toHaveBeenCalledTimes(1)
  })

  it("stores the CSRF token from the me payload", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { username: "admin", authMethod: "password", csrfToken: "tok-me" }))
    await client.getMe()
    fetchMock.mockResolvedValueOnce(jsonResponse(204))
    await client.logout()
    expect(sentRequest(1).headers.get("X-CSRF-Token")).toBe("tok-me")
  })
})
