package logger_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/config"
	"github.com/autobrr/harbrr/internal/logger"
)

// These tests mutate the process-global zerolog level (the runtime knob), so they are
// intentionally NOT parallel and each restores a permissive level for the next.

func TestNewFormatsAndEmits(t *testing.T) {
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	for _, format := range []string{"json", "console"} {
		var buf bytes.Buffer
		lg := logger.New(config.LogConfig{Format: format}, &buf)
		lg.Info().Msg("hello")
		if !strings.Contains(buf.String(), "hello") {
			t.Errorf("%s: output %q missing message", format, buf.String())
		}
	}
}

func TestSetLevelGatesGlobally(t *testing.T) {
	defer zerolog.SetGlobalLevel(zerolog.TraceLevel)

	var buf bytes.Buffer
	lg := logger.New(config.LogConfig{Format: "json"}, &buf)

	if err := logger.SetLevel("warn"); err != nil {
		t.Fatalf("SetLevel: %v", err)
	}
	if got := logger.Level(); got != "warn" {
		t.Errorf("Level() = %q, want warn", got)
	}
	lg.Info().Msg("filtered")
	lg.Warn().Msg("kept")

	out := buf.String()
	if strings.Contains(out, "filtered") {
		t.Errorf("info must be filtered at warn level: %q", out)
	}
	if !strings.Contains(out, "kept") {
		t.Errorf("warn must pass at warn level: %q", out)
	}
}

func TestSetLevelRejectsUnknown(t *testing.T) {
	defer zerolog.SetGlobalLevel(zerolog.TraceLevel)
	if err := logger.SetLevel("loud"); err == nil {
		t.Fatal("SetLevel(loud) = nil, want an error")
	}
}
