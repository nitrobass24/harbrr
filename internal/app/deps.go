package app

import (
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

	// DB is an already-open, migrated database to build on instead of letting New
	// open one from Config (New skips OpenDatabase entirely when set). Production
	// (cmd/harbrr) leaves it nil. Close ownership differs by outcome: on success,
	// App.Run closes it on the way out same as a New-opened database. On a New
	// error, the injector keeps ownership and must close it itself — New only
	// closes a database it opened.
	DB *database.DB
}
