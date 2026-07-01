package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/autobrr/harbrr/internal/config"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/logger"
)

// logLevelKey is the app_settings key holding the operator's runtime log-level override.
// A stored value takes precedence over the config-file/env/flag seed (it is applied at
// startup and by the management API), mirroring the cache-config "DB row overrides the
// seed" model so a UI change survives a restart.
const logLevelKey = "log.level"

// errInvalidLogLevel is returned by Set for a level outside the accepted enum, so the
// handler can map it to 400 rather than a generic 500.
var errInvalidLogLevel = errors.New("invalid log level")

// LogLevelStore reads and writes the runtime log level: it drives the process-global
// logger threshold (logger.SetLevel — one dial every subsystem's logger consults, so the
// change is live and needs no restart) and persists the operator's choice to app_settings.
// It is the backend the management log-level endpoints call.
type LogLevelStore struct {
	db  dbinterface.Execer
	now func() time.Time
}

// NewLogLevelStore builds a store over db. now supplies the persisted-at timestamp
// (injectable for tests); nil uses time.Now.
func NewLogLevelStore(db dbinterface.Execer, now func() time.Time) *LogLevelStore {
	if now == nil {
		now = time.Now
	}
	return &LogLevelStore{db: db, now: now}
}

// Current returns the effective level (the process-global threshold).
func (s *LogLevelStore) Current() string { return logger.Level() }

// Set validates level, applies it globally, then persists it. An unknown level is
// rejected without changing anything.
func (s *LogLevelStore) Set(ctx context.Context, level string) error {
	if !config.ValidLogLevel(level) {
		return fmt.Errorf("%w: %q", errInvalidLogLevel, level)
	}
	if err := logger.SetLevel(level); err != nil {
		return fmt.Errorf("api: apply log level: %w", err)
	}
	if err := (database.AppSettings{}).Set(ctx, s.db, logLevelKey, level, s.now()); err != nil {
		return fmt.Errorf("api: persist log level: %w", err)
	}
	return nil
}

// ApplyPersisted applies a stored override (if present and valid) over the config seed
// at startup. It returns whether an override was applied. A stored value that is no
// longer a valid level is ignored — the seed stays — so a stale row can never wedge boot.
func (s *LogLevelStore) ApplyPersisted(ctx context.Context) (applied bool, err error) {
	stored, found, err := database.AppSettings{}.Get(ctx, s.db, logLevelKey)
	if err != nil {
		return false, fmt.Errorf("api: read persisted log level: %w", err)
	}
	if !found || !config.ValidLogLevel(stored) {
		return false, nil
	}
	if err := logger.SetLevel(stored); err != nil {
		return false, fmt.Errorf("api: apply persisted log level: %w", err)
	}
	return true, nil
}

// logLevelBody is the shared request/response shape for the log-level endpoints.
type logLevelBody struct {
	Level string `json:"level"`
}

// getLogLevel returns the effective runtime log level.
func (rt *router) getLogLevel(w http.ResponseWriter, _ *http.Request) {
	if rt.logLevel == nil {
		writeError(w, http.StatusServiceUnavailable, "log level control is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, logLevelBody{Level: rt.logLevel.Current()})
}

// putLogLevel changes the runtime log level and persists it. The change takes effect
// immediately across every subsystem and survives a restart.
func (rt *router) putLogLevel(w http.ResponseWriter, r *http.Request) {
	if rt.logLevel == nil {
		writeError(w, http.StatusServiceUnavailable, "log level control is unavailable")
		return
	}
	var req logLevelBody
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.logLevel.Set(r.Context(), req.Level); err != nil {
		if errors.Is(err, errInvalidLogLevel) {
			writeError(w, http.StatusBadRequest, "log level must be one of: trace, debug, info, warn, error")
			return
		}
		rt.writeServiceError(w, "set log level", err)
		return
	}
	rt.log.Info().Str("level", req.Level).Msg("api: log level changed")
	writeJSON(w, http.StatusOK, logLevelBody{Level: rt.logLevel.Current()})
}
