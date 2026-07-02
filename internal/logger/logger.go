// Package logger builds the zerolog logger from harbrr's typed config. The level is
// controlled through the process-global zerolog level (SetLevel), NOT per-logger, so a
// single runtime change takes effect across every subsystem's copy of the logger —
// which is what lets the management API change the level live, without a restart.
package logger

import (
	"fmt"
	"io"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/config"
)

// New builds a zerolog.Logger from the log configuration, writing to w. A "console"
// format yields human-friendly colored output; "json" yields structured JSON.
//
// The returned logger is built at the most permissive level (Trace) on purpose: the
// EFFECTIVE threshold is the process-global level (see SetLevel), which every logger —
// including the copies handed to each subsystem — consults on every event. Building
// permissive makes the global level the single dial; a per-logger level would pin a
// floor the global knob could not lower, defeating a live "turn on debug" change.
func New(cfg config.LogConfig, w io.Writer) zerolog.Logger {
	out := w
	if cfg.Format == "console" {
		out = zerolog.ConsoleWriter{Out: w, TimeFormat: time.RFC3339}
	}
	return zerolog.New(out).Level(zerolog.TraceLevel).With().Timestamp().Logger()
}

// SetLevel applies level as the process-global logging threshold, taking effect
// immediately across every existing logger. It returns an error for an unparseable
// level (config validation rejects invalid levels at startup; this guards the runtime
// API path). Callers should pre-validate against config.ValidLogLevel to keep the
// accepted set aligned with the config enum.
func SetLevel(level string) error {
	lvl, err := zerolog.ParseLevel(level)
	if err != nil {
		return fmt.Errorf("logger: parse level %q: %w", level, err)
	}
	zerolog.SetGlobalLevel(lvl)
	return nil
}

// Level returns the current effective process-global level as its canonical string
// ("trace" | "debug" | "info" | "warn" | "error").
func Level() string {
	return zerolog.GlobalLevel().String()
}
