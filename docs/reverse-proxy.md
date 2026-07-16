# Running harbrr behind a reverse proxy

harbrr is designed to sit behind a TLS-terminating reverse proxy (Traefik, nginx, Caddy):
it never needs its own TLS certificate, and the proxy is where you'd typically also apply
rate limiting, an auth gateway, or a shared domain/subpath layout.

## Quick checklist

- Point the proxy at harbrr's listen address (`server.host`/`server.port`).
- Forward `Host`, `X-Forwarded-For`, and `X-Forwarded-Proto`.
- Set `server.external_url` to harbrr's externally-visible URL (recommended — see below).
- Set `auth.trusted_proxies` to the proxy's address so harbrr trusts its forwarded headers.
- If harbrr is mounted under a subpath, set `server.base_url` too, and make sure
  `server.external_url`'s path (if any) matches it exactly.

## `server.external_url`

```toml
[server]
external_url = "https://harbrr.example.com"
# or, mounted under a subpath:
# base_url     = "/harbrr"
# external_url = "https://example.com/harbrr"
```

When set, `external_url` is **authoritative** for every absolute link harbrr serves: the
Torznab feed's `<link>` self-URL, the `/dl` grab-proxy base, and the cross-seed/announce
`/dl` links pushed to configured apps. It also **implies a Secure session cookie** when its
scheme is `https` — you don't need `secure_cookie` too. It also prefills the "harbrr URL"
field when adding an app/announce connection in the web UI, instead of guessing from
`window.location.origin`.

Leave it empty and harbrr falls back to deriving links from the request: `Host` header +
`X-Forwarded-Proto` (honored only from a trusted proxy — see below). This works for a
single reverse proxy in front of harbrr with `Host` preserved, but `external_url` is more
robust (e.g. when harbrr sits behind a proxy chain, or Docker rewrites `Host`) and is the
one thing you need to set the OIDC redirect URI correctly once #9 ships.

## `secure_cookie`

Set `server.secure_cookie = true` if you terminate TLS on the proxy but don't want to (or
can't) set `external_url` — e.g. a `http` internal `external_url` behind a TLS-terminating
edge that doesn't forward `X-Forwarded-Proto` at all. Either `secure_cookie = true` or an
`https` `external_url` marks the session cookie `Secure`; the `Secure` flag is computed
once at startup, not derived per-request.

## `auth.trusted_proxies`

```toml
[auth]
trusted_proxies = ["172.16.0.0/12"]  # your proxy's network (Docker bridge, etc.)
```

`X-Forwarded-For` (for the IP allowlist in `auth.mode = "disabled"`) and
`X-Forwarded-Proto` (for the request-derived URL fallback above, when `external_url` is
unset) are honored **only** from a peer in `auth.trusted_proxies` — an internet client
sitting in front of an untrusted hop can't forge either header to bypass the allowlist or
force an `https` self-URL. Leave it unset if you're not running `auth.mode = "disabled"`
and you've set `external_url` (nothing depends on the header then).

## Subpath mounting (`base_url`)

```toml
[server]
base_url = "/harbrr"
```

harbrr strips the configured prefix before routing and re-adds it to every served URL.
Make sure your proxy forwards the request path unmodified (don't rewrite `/harbrr/api/...`
down to `/api/...` — harbrr does that stripping itself). A request to the bare subpath
without a trailing slash may 30x-redirect to add one; a proxy that itself redirects can
create a double-redirect loop, so prefer a proxy config that just proxies the path through.

## `X-Forwarded-Host` / `X-Forwarded-Prefix` are NOT supported

harbrr deliberately does not read `X-Forwarded-Host` or auto-learn a mount prefix from a
header. Configure `external_url` (and `base_url` for a subpath) explicitly instead — with
`external_url` set, self-URLs no longer depend on `Host` at all; without it, make sure your
proxy preserves the original `Host` header. This keeps harbrr's URL derivation
predictable and out of a client-influenced header's control.

## nginx

```nginx
location / {
    proxy_pass http://127.0.0.1:7478;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

## Traefik (docker labels)

```yaml
labels:
  - traefik.http.routers.harbrr.rule=Host(`harbrr.example.com`)
  - traefik.http.routers.harbrr.tls=true
  - traefik.http.services.harbrr.loadbalancer.server.port=7478
```

Traefik sets `X-Forwarded-For`/`X-Forwarded-Proto`/`Host` correctly by default; add the
Docker bridge network (or Traefik's own address) to `auth.trusted_proxies` if you rely on
the request-derived fallback (no `external_url` set) or `auth.mode = "disabled"`.

## Caddy

```
harbrr.example.com {
    reverse_proxy 127.0.0.1:7478
}
```

Caddy sets the forwarded headers automatically.
