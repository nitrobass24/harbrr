package api

import (
	"fmt"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/domain"
)

// frontendLogMaxMessageBytes/frontendLogMaxContextBytes cap POST /api/logs/frontend so a
// misbehaving client can't use the log sink to exhaust the daemon's log storage. message
// is a short human-readable summary (toast copy is always well under 1KB); context is at
// most an error's .message (never a whole response body), so 4KB is generous without
// admitting arbitrary blobs.
const (
	frontendLogMaxMessageBytes = 1024
	frontendLogMaxContextBytes = 4096
)

// frontendLogBody is the request shape for POST /api/logs/frontend: a toast the web UI
// showed the operator, relayed so it lands in the one log a single-user self-hosted
// install actually has (see web/src/lib/notify.ts).
type frontendLogBody struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Context string `json:"context,omitempty"`
}

// validate rejects an unrecognized level, an empty/oversize message, or an oversize
// context. Every failure wraps domain.ErrInvalid, so writeServiceError maps it to 400.
func (b frontendLogBody) validate() error {
	switch b.Level {
	case "error", "warn", "info":
	default:
		return fmt.Errorf("%w: level must be one of: error, warn, info", domain.ErrInvalid)
	}
	if b.Message == "" {
		return fmt.Errorf("%w: message is required", domain.ErrInvalid)
	}
	if len(b.Message) > frontendLogMaxMessageBytes {
		return fmt.Errorf("%w: message exceeds %d bytes", domain.ErrInvalid, frontendLogMaxMessageBytes)
	}
	if len(b.Context) > frontendLogMaxContextBytes {
		return fmt.Errorf("%w: context exceeds %d bytes", domain.ErrInvalid, frontendLogMaxContextBytes)
	}
	return nil
}

// zerologLevel maps the validated three-value enum to a zerolog level. validate has
// already rejected anything else, so the default only ever covers "info".
func zerologLevel(level string) zerolog.Level {
	switch level {
	case "error":
		return zerolog.ErrorLevel
	case "warn":
		return zerolog.WarnLevel
	default:
		return zerolog.InfoLevel
	}
}

// postFrontendLog relays a web-UI toast into the daemon's own zerolog stream: harbrr is
// single-user self-hosted software and THE log is the daemon's stream, but many error
// toasts describe client-only events (a fetch that never reached the server, a client-
// side validation rejection) the server never otherwise observes. The content is opaque
// — never parsed or expanded — and logged alone, without request headers or cookies.
func (rt *router) postFrontendLog(w http.ResponseWriter, r *http.Request) {
	var req frontendLogBody
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := req.validate(); err != nil {
		rt.writeServiceError(w, "post frontend log", err)
		return
	}

	ev := rt.log.WithLevel(zerologLevel(req.Level)).Str("component", "webui")
	if req.Context != "" {
		ev = ev.Str("context", req.Context)
	}
	ev.Msg(req.Message)

	w.WriteHeader(http.StatusNoContent)
}
