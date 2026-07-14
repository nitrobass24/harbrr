package app

import (
	"net/http"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/config"
	"github.com/autobrr/harbrr/internal/database"
)

// Deps are the inputs New cannot construct itself: the resolved config and the
// process logger. Every other node in the dependency graph is built by New, in
// the fixed order documented there.
type Deps struct {
	Config *config.Config
	Logger zerolog.Logger
}

// Option widens New for tests: inject an already-open database or a non-default
// outbound HTTP client instead of letting New build its own. Production callers
// (cmd/harbrr) pass none.
type Option func(*options)

type options struct {
	db         *database.DB
	httpClient *http.Client
}

// WithDatabase injects an already-open, migrated database instead of letting
// New open one from Deps.Config (New skips openDatabase entirely when set).
// Close ownership differs by outcome: on success, App.Run closes it on the way
// out same as a New-opened database. On a New error, the injector keeps
// ownership and must close it itself — New only closes a database it opened.
func WithDatabase(db *database.DB) Option {
	return func(o *options) { o.db = db }
}

// WithHTTPClient overrides the outbound HTTP client shared by app-sync,
// announce targets, and the notification sink (default: appSyncClient, a
// bounded 30s client).
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) { o.httpClient = c }
}
