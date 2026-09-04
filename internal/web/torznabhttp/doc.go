// Package torznabhttp serves harbrr's *arr-facing Torznab/Newznab HTTP endpoint:
// it parses Sonarr/Radarr requests (t=caps|search|tvsearch|movie|music|book),
// resolves the target indexer through a Provider, drives the Cardigann engine,
// and serializes capabilities / results / errors with internal/torznab.
//
// It is one of harbrr's two independent HTTP surfaces. It MUST NOT import
// internal/web/swagger (the management OpenAPI surface): the two contracts
// evolve independently (architecture invariant #3). It depends only on the
// internal/torznab serializer, the engine's data-model packages (mapper,
// normalizer, search) via the Provider interface, internal/indexer/grab for the
// download seam, and internal/http for redaction — never the concrete engine, so
// it stays testable with a fake Provider.
//
// The /dl route it serves is a thin adapter: it authenticates the apikey, resolves
// the slug, and hands off to grab.ServeGrab with a Torznab-XML ErrorWriter. Token
// sealing, resolution and streaming all live in internal/indexer/grab (ADR-0002).
package torznabhttp
