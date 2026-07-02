// Package cardigann is harbrr's fresh, from-scratch implementation of the
// Cardigann tracker-definition engine. It is NOT the archived 2018
// cardigann/cardigann project; it shares only the definition format, which is
// kept as the interoperability contract with the existing community corpus.
//
// The engine is built as a compiler-style pipeline of small, independently
// testable stages, each in its own subpackage:
//
//	loader        parse + schema-validate a definition
//	mapper        capabilities + category mapping
//	template      Go text/template evaluation (.NET-equivalent truthiness)
//	filter        the bounded Cardigann filter registry
//	selector      HTML (cascadia/goquery) + JSON selection
//	dateparse     .NET date-format strings -> Go layout (tz, relative, localized)
//	regexadapter  RE2 by default, regexp2 (.NET semantics) on demand
//	login         the login/session executor (form/post/get/cookie)
//	search        execute a search, page, collect rows
//	normalizer    produce normalized release objects
//
// The Torznab/Newznab serializer lives in internal/torznab.
//
// Definitions are consumed byte-for-byte; ALL behavioral differences live in
// these stages, never in the def files. The correctness target is behavioral
// parity with Jackett's engine on the same input, pinned by the parity suite in
// the parity subpackage. See docs/architecture.md and AGENTS.md.
package cardigann
