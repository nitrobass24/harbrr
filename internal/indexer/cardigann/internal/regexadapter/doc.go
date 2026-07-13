// Package regexadapter routes patterns between RE2 (default, ReDoS-safe) and regexp2 (.NET semantics).
//
// One stage of the harbrr Cardigann engine pipeline. Keep it decoupled from
// the other stages and table-driven-tested with its own fixtures.
// See AGENTS.md and docs/architecture.md.
package regexadapter
