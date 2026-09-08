package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"

	"github.com/autobrr/harbrr/internal/secrets"
)

const (
	// csrfHeaderName is the header a cookie-authenticated client echoes the CSRF
	// token in on a state-changing request.
	csrfHeaderName = "X-CSRF-Token"
	// csrfCookieName is the NON-HttpOnly companion cookie carrying the token so a
	// browser client can read it and echo it in csrfHeaderName. The session cookie
	// itself is HttpOnly and unreadable by JS, so it can't serve this role.
	csrfCookieName = "harbrr_csrf"
	// sessionCSRFToken is the SCS session key holding the authoritative token.
	sessionCSRFToken = "csrf_token"
)

// csrf enforces a session-bound CSRF token on cookie-authenticated, state-changing
// requests. It is a synchronizer token (not an Origin/Referer/Sec-Fetch check) on
// purpose: the token is bound to the session, so it is origin-agnostic (transparent
// to a reverse proxy that rewrites Host/Origin) and login-mechanism-agnostic (a
// password login or a future OIDC callback both just create a session). Requests
// authenticated by X-API-Key or the auth-disabled/trusted-proxy mode carry no
// forgeable ambient credential, so they are exempt.
func (rt *router) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) || authMethodFrom(r.Context()) != authSession {
			next.ServeHTTP(w, r)
			return
		}
		want := rt.Sessions.GetString(r.Context(), sessionCSRFToken)
		got := r.Header.Get(csrfHeaderName)
		if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
			writeError(w, http.StatusForbidden, "invalid or missing CSRF token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// issueCSRFToken mints a fresh token, binds it to the session, and writes the
// readable companion cookie. Every path that authenticates a session must call it
// right after RenewToken — login today, a future OIDC callback tomorrow.
func (rt *router) issueCSRFToken(ctx context.Context, w http.ResponseWriter) error {
	token, err := secrets.GenerateAPIKey() // a 256-bit URL-safe random token
	if err != nil {
		return fmt.Errorf("api: generate csrf token: %w", err)
	}
	rt.Sessions.Put(ctx, sessionCSRFToken, token)
	http.SetCookie(w, rt.csrfCookie(token, 0))
	return nil
}

// clearCSRFCookie expires the companion cookie on logout.
func (rt *router) clearCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, rt.csrfCookie("", -1))
}

// csrfCookie builds the companion cookie, mirroring the session cookie's Secure and
// Path (so it behaves identically behind a TLS-terminating proxy / at a base path).
// It is deliberately NOT HttpOnly so a browser client can read and echo it.
func (rt *router) csrfCookie(value string, maxAge int) *http.Cookie {
	//nolint:gosec // G124: this CSRF companion cookie is intentionally NOT HttpOnly so the
	// browser client can read and echo the token (double-submit); it holds an anti-CSRF
	// token, not a secret, and carries SameSite=Lax + Secure (when server.secure_cookie is set).
	return &http.Cookie{
		Name:     csrfCookieName,
		Value:    value,
		Path:     rt.Sessions.Cookie.Path,
		Secure:   rt.Sessions.Cookie.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}

// isSafeMethod reports whether m is a non-mutating (read-only) HTTP method.
func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}
