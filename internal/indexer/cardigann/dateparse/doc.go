// Package dateparse translates .NET date-format strings to Go layouts (timezones, relative dates, localized names).
//
// One stage of the harbrr Cardigann engine pipeline. Keep it decoupled from
// the other stages and table-driven-tested with its own fixtures.
// See AGENTS.md and docs/architecture.md.
package dateparse
