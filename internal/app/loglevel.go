package app

import (
	"context"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/config"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/logger"
)

// logLevelSetting is the operator's runtime log-level override. A stored value beats
// the config-file/env/flag seed (it is applied at boot and by the management API), so
// a UI change survives a restart. Parse rejects anything outside the config enum,
// which is what keeps a level that is no longer valid from wedging boot: the seed
// simply stays.
var logLevelSetting = database.Setting[string]{
	Key:     "log.level",
	Default: "",
	Parse:   parseLogLevel,
	Format:  func(level string) string { return level },
}

func parseLogLevel(level string) (string, error) {
	if !config.ValidLogLevel(level) {
		return "", fmt.Errorf("%q is not a log level", level)
	}
	return level, nil
}

// applyPersistedLogLevel applies the stored override over the config seed at boot.
// The empty default means "nothing stored" — the seed stays in effect.
func (a *App) applyPersistedLogLevel(ctx context.Context) {
	level := logLevelSetting.Read(ctx, a.db, a.log)
	if level == "" {
		return
	}
	if err := logger.SetLevel(level); err != nil {
		a.log.Warn().Err(err).Msg("serve: applying persisted log level failed; using configured level")
		return
	}
	a.log.Info().Str("level", logger.Level()).Msg("serve: applied persisted log-level override")
}

// setLogLevel persists the operator's choice and then applies it process-wide. It
// persists FIRST so a failed write leaves the running level untouched: runtime and
// stored state never disagree. The API handler validates the level before calling
// (so it can answer 400); logger.SetLevel is the backstop.
func (a *App) setLogLevel(ctx context.Context, level string) error {
	if err := logLevelSetting.Write(ctx, a.db, level, time.Now()); err != nil {
		return fmt.Errorf("serve: persist log level: %w", err)
	}
	if err := logger.SetLevel(level); err != nil {
		return fmt.Errorf("serve: apply log level: %w", err)
	}
	return nil
}
