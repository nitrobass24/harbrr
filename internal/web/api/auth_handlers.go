package api

import "net/http"

// oidcConfig answers the login screen's OIDC probe (public). It always
// returns 200: {enabled:false, ...} when OIDC is disabled or failed to
// initialize (mirrors qui's api.ts getOIDCConfig() default), never an error —
// a logged-out visitor has no session yet to fail on. When enabled, it clears
// any prior state/PKCE verifier, generates a fresh pair, and stashes them in
// the session for the callback to validate.
func (rt *router) oidcConfig(w http.ResponseWriter, r *http.Request) {
	rt.sessions.Remove(r.Context(), sessionOIDCState)
	rt.sessions.Remove(r.Context(), sessionOIDCPKCE)
	if rt.oidc == nil {
		writeJSON(w, http.StatusOK, oidcConfigResponse{})
		return
	}
	resp, state, verifier, err := rt.oidc.configResponse()
	if err != nil {
		rt.writeServiceError(w, "oidc config", err)
		return
	}
	rt.sessions.Put(r.Context(), sessionOIDCState, state)
	if verifier != "" {
		rt.sessions.Put(r.Context(), sessionOIDCPKCE, verifier)
	}
	writeJSON(w, http.StatusOK, resp)
}

// oidcCallback validates the IdP's redirect, exchanges the code, verifies the
// ID token, and mirrors login's session-establishment sequence (RenewToken →
// Put(authenticated) → Put(username) → issueCSRFToken) before redirecting
// into the app. Session-only gate: no local user row is created or matched
// (autobrr/harbrr#9) — the username comes straight from the verified claims.
func (rt *router) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if rt.oidc == nil {
		writeError(w, http.StatusNotFound, "OIDC is not configured")
		return
	}
	ctx := r.Context()

	expectedState := rt.sessions.GetString(ctx, sessionOIDCState)
	if expectedState == "" {
		writeError(w, http.StatusBadRequest, "invalid state: no state found in session")
		return
	}
	if r.URL.Query().Get("state") != expectedState {
		writeError(w, http.StatusBadRequest, "invalid state: state mismatch")
		return
	}
	rt.sessions.Remove(ctx, sessionOIDCState)
	verifier := rt.sessions.GetString(ctx, sessionOIDCPKCE)
	rt.sessions.Remove(ctx, sessionOIDCPKCE)

	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "authorization code is missing from callback request")
		return
	}

	token, err := rt.oidc.exchange(ctx, code, verifier)
	if err != nil {
		rt.writeServiceError(w, "oidc callback exchange", err)
		return
	}
	username, err := rt.oidc.verifyAndDeriveUsername(ctx, token)
	if err != nil {
		rt.writeServiceError(w, "oidc callback verify", err)
		return
	}

	// Renew the token on privilege change (session-fixation guard), then mark the
	// session authenticated — same sequence as login (auth_handlers.go).
	if err := rt.sessions.RenewToken(ctx); err != nil {
		rt.writeServiceError(w, "oidc callback session", err)
		return
	}
	rt.sessions.Put(ctx, sessionAuthenticated, true)
	rt.sessions.Put(ctx, sessionUsername, username)
	if err := rt.issueCSRFToken(ctx, w); err != nil {
		rt.writeServiceError(w, "oidc callback session", err)
		return
	}

	redirect := oidcPostLoginRedirect(rt.oidc.cfg.RedirectURL, rt.urlCfg.BasePath)
	http.Redirect(w, r, redirect, http.StatusFound)
}

// getSetup reports whether first-run setup is complete (public).
func (rt *router) getSetup(w http.ResponseWriter, r *http.Request) {
	done, err := rt.auth.SetupComplete(r.Context())
	if err != nil {
		rt.writeServiceError(w, "setup status", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"setupComplete": done})
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// postSetup creates the single admin (public; fails once setup is complete).
func (rt *router) postSetup(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := rt.auth.Setup(r.Context(), req.Username, req.Password)
	if err != nil {
		rt.writeServiceError(w, "setup", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"username": u.Username})
}

// login verifies credentials and establishes a session (public).
func (rt *router) login(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := rt.auth.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		rt.writeServiceError(w, "login", err)
		return
	}
	// Renew the token on privilege change (session-fixation guard), then mark the
	// session authenticated.
	if err := rt.sessions.RenewToken(r.Context()); err != nil {
		rt.writeServiceError(w, "login session", err)
		return
	}
	rt.sessions.Put(r.Context(), sessionAuthenticated, true)
	rt.sessions.Put(r.Context(), sessionUsername, u.Username)
	// Bind a CSRF token to the new session and hand it to the client (companion
	// cookie). A future OIDC callback must do the same after RenewToken.
	if err := rt.issueCSRFToken(r.Context(), w); err != nil {
		rt.writeServiceError(w, "login session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// changePassword verifies the current admin password and sets a new one (auth
// required). The session token is renewed afterwards (session-fixation guard),
// mirroring login. 400 on a weak new password, 401 on a wrong current password;
// neither value is logged.
func (rt *router) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.auth.ChangePassword(r.Context(), req.CurrentPassword, req.NewPassword); err != nil {
		rt.writeServiceError(w, "change password", err)
		return
	}
	// The password is now persisted. Renew the session token (session-fixation guard)
	// best-effort for a session caller only — never report the change as failed after
	// it has succeeded, which would invite a retry with the now-wrong old password (and
	// an API-key caller has no session to rotate).
	if authMethodFrom(r.Context()) == authSession {
		if err := rt.sessions.RenewToken(r.Context()); err != nil {
			rt.log.Warn().Str("op", "change password session").Err(err).Msg("api: session renew after password change failed")
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// logout destroys the session.
func (rt *router) logout(w http.ResponseWriter, r *http.Request) {
	if err := rt.sessions.Destroy(r.Context()); err != nil {
		rt.writeServiceError(w, "logout", err)
		return
	}
	rt.clearCSRFCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// me returns the authenticated identity and how it was authenticated.
func (rt *router) me(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	username := rt.sessions.GetString(ctx, sessionUsername)
	if username == "" {
		username = "admin" // API-key or auth-disabled mode has no session username
	}
	// csrfToken is the session's current token (empty for an apikey/auth-disabled
	// caller, which needs no CSRF token) so a browser client can bootstrap it here as
	// well as from the companion cookie.
	csrfToken := rt.sessions.GetString(ctx, sessionCSRFToken)
	// Backfill a token for a session-authenticated caller that predates CSRF binding
	// (sessions persist 30 days): without this it would 403 on every mutation with no
	// recovery but a manual re-login. /me is the bootstrap the client always calls on
	// load, so minting here self-heals the session on the next page load. Gated on
	// session auth so an apikey/auth-disabled caller never materializes a session; the
	// cookie must be written before the body.
	if csrfToken == "" && authMethodFrom(ctx) == authSession {
		if err := rt.issueCSRFToken(ctx, w); err != nil {
			rt.writeServiceError(w, "issue csrf token", err)
			return
		}
		csrfToken = rt.sessions.GetString(ctx, sessionCSRFToken)
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"username":   username,
		"authMethod": methodName(authMethodFrom(ctx)),
		"csrfToken":  csrfToken,
	})
}

// methodName renders an authMethod for the API.
func methodName(m authMethod) string {
	switch m {
	case authNone:
		return "none"
	case authSession:
		return "session"
	case authAPIKey:
		return "apikey"
	case authDisabled:
		return "disabled"
	}
	return "none"
}
