package api

import "net/http"

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
	w.WriteHeader(http.StatusNoContent)
}

// me returns the authenticated identity and how it was authenticated.
func (rt *router) me(w http.ResponseWriter, r *http.Request) {
	username := rt.sessions.GetString(r.Context(), sessionUsername)
	if username == "" {
		username = "admin" // API-key or auth-disabled mode has no session username
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"username":   username,
		"authMethod": methodName(authMethodFrom(r.Context())),
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
