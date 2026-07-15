// Package core is the indexer serving contract: a searchable tracker as the rest of
// harbrr consumes it, independent of whether a Cardigann Definition or a native driver
// backs it. It defines Indexer/Provider (the interfaces the registry builds and the
// Torznab/JSON surfaces serve), IndexerInfo/CacheInfo (the DTOs those surfaces read),
// the cache-sink and cache/freeleech-bypass context plumbing, and the shared
// SearchReleases read pipeline (map -> search -> dedupe -> filter -> page) run by both
// the Torznab feed handler and the management JSON search API.
//
// core sits above the Cardigann engine (it imports cardigann/mapper, /normalizer,
// /search for the types the contract speaks, and internal/torznab for the guid
// derivation and request-mode helpers the read pipeline shares with the serializer)
// and below any transport: it knows nothing of HTTP. internal/indexer/registry (the
// producer of indexers) and internal/web/torznabhttp + internal/web/api (the
// consumers/servers) all depend on core; core depends on neither.
package core
