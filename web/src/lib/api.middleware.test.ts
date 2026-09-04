import { afterEach, describe, expect, it, vi } from "vitest"
import { APIError, unwrap } from "./api"
import { stubApi, UnstubbedRequestError } from "@/test/stubApi"

// The #557 PR1 acceptance suite: the middleware seam exercised end-to-end through
// stubApi's injected transport, so what passes here is exactly what runs in
// production — CSRF onRequest, the 401 onResponse hard-redirect with its
// AUTH_BOOTSTRAP exemption, and the {error, code} envelope mapping.

function errorResponse(status: number, code: string, error: string): Response {
  return Response.json({ error, code }, { status })
}

describe("stubApi through the real middleware pipeline", () => {
  afterEach(() => {
    document.cookie = "harbrr_csrf=; expires=Thu, 01 Jan 1970 00:00:00 GMT"
  })

  it("injects the CSRF header on mutating verbs and not on reads", async () => {
    const api = stubApi({
      "GET /api/apps": [],
      "POST /api/auth/logout": null,
    })
    api.setCsrfToken("tok-from-me")
    await unwrap(api.http.GET("/api/apps"))
    await unwrap(api.http.POST("/api/auth/logout"))
    expect(api.calls("GET /api/apps")[0].headers.get("X-CSRF-Token")).toBeNull()
    expect(api.calls("POST /api/auth/logout")[0].headers.get("X-CSRF-Token")).toBe("tok-from-me")
  })

  it("the companion cookie's CSRF token takes precedence over the me-payload token", async () => {
    const api = stubApi({ "POST /api/auth/logout": null })
    document.cookie = "harbrr_csrf=tok-from-cookie"
    api.setCsrfToken("tok-from-me")
    await unwrap(api.http.POST("/api/auth/logout"))
    expect(api.calls("POST /api/auth/logout")[0].headers.get("X-CSRF-Token")).toBe("tok-from-cookie")
  })

  it("omits the CSRF header entirely when no token exists (auth disabled)", async () => {
    const api = stubApi({ "POST /api/auth/logout": null })
    await unwrap(api.http.POST("/api/auth/logout"))
    expect(api.calls("POST /api/auth/logout")[0].headers.get("X-CSRF-Token")).toBeNull()
  })

  it("stores the CSRF token from the me payload", async () => {
    const api = stubApi({
      "GET /api/auth/me": { username: "admin", authMethod: "password", csrfToken: "tok-me" },
      "POST /api/auth/logout": null,
    })
    await api.getMe()
    await unwrap(api.http.POST("/api/auth/logout"))
    expect(api.calls("POST /api/auth/logout")[0].headers.get("X-CSRF-Token")).toBe("tok-me")
  })

  it("calls onUnauthorized on a 401 from a non-exempt path", async () => {
    const api = stubApi({ "GET /api/apps": () => errorResponse(401, "unauthorized", "no session") })
    const unauthorized = vi.fn()
    api.onUnauthorized = unauthorized
    await expect(unwrap(api.http.GET("/api/apps"))).rejects.toBeInstanceOf(APIError)
    expect(unauthorized).toHaveBeenCalledTimes(1)
  })

  it("throws APIError without bouncing on a 401 from each AUTH_BOOTSTRAP path", async () => {
    const api = stubApi({
      "GET /api/auth/me": () => errorResponse(401, "unauthorized", "no session"),
      "POST /api/auth/login": () => errorResponse(401, "invalid_credentials", "wrong credentials"),
      "POST /api/auth/setup": () => errorResponse(401, "unauthorized", "already set up"),
      "POST /api/auth/logout": () => errorResponse(401, "unauthorized", "no session"),
    })
    const unauthorized = vi.fn()
    api.onUnauthorized = unauthorized
    await expect(api.getMe()).rejects.toBeInstanceOf(APIError)
    await expect(unwrap(api.http.POST("/api/auth/login", { body: { username: "a", password: "b" } }))).rejects.toBeInstanceOf(APIError)
    await expect(unwrap(api.http.POST("/api/auth/setup", { body: { username: "a", password: "b" } }))).rejects.toBeInstanceOf(APIError)
    await expect(unwrap(api.http.POST("/api/auth/logout"))).rejects.toBeInstanceOf(APIError)
    expect(unauthorized).not.toHaveBeenCalled()
  })

  it("parses the error envelope into APIError, matching {param} path segments", async () => {
    const api = stubApi({ "DELETE /api/apikeys/{id}": () => errorResponse(409, "conflict", "still in use") })
    const err = await unwrap(api.http.DELETE("/api/apikeys/{id}", { params: { path: { id: 7 } } })).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(APIError)
    expect((err as APIError).status).toBe(409)
    expect((err as APIError).code).toBe("conflict")
    expect((err as APIError).message).toBe("still in use")
  })

  it("records sent requests so tests can assert bodies", async () => {
    const api = stubApi({ "POST /api/apikeys": { id: 1, name: "deploy", key: "k" } })
    await unwrap(api.http.POST("/api/apikeys", { body: { name: "deploy" } }))
    expect(await api.calls("POST /api/apikeys")[0].json()).toEqual({ name: "deploy" })
  })

  it("fails loudly on a request nothing stubbed, naming the stubbed keys", async () => {
    const api = stubApi({ "GET /api/apps": [] })
    await expect(unwrap(api.http.GET("/api/indexers"))).rejects.toThrow(UnstubbedRequestError)
    await expect(unwrap(api.http.GET("/api/indexers"))).rejects.toThrow("GET /api/apps")
  })
})
