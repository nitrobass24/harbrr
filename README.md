# Harbrr

> The tracker and indexer fabric for the autobrr ecosystem.

Harbrr is a modern tracker and indexer management platform built for autobrr, qui, cross-seed, and private tracker power users.

Manage your trackers once. Connect everything. Search less. Cache more.

Harbrr provides a centralized intelligence layer between your trackers and automation applications, reducing duplicate requests, aggregating RSS feeds, optimizing search traffic, and exposing a unified Torznab interface to your entire automation stack.

While Harbrr is built with the autobrr ecosystem in mind, it remains fully compatible with Sonarr, Radarr, Lidarr, Readarr, Mylar, Whisparr, and any Torznab-compatible application.

---

## Why Harbrr?

Most indexer managers were designed around the *arr ecosystem and later integrated with autobrr.

Harbrr takes the opposite approach.

Harbrr is being built for modern private tracker workflows from day one, with native consideration for autobrr, qui, cross-seed, and tracker-friendly automation practices while maintaining seamless compatibility with existing *arr applications.

The goal isn't simply to proxy searches.

The goal is to become the intelligent layer between automation applications and trackers.

---

## Architecture

mermaid flowchart TD     A[Private Trackers & Indexers] --> B[Harbrr]      B --> C[autobrr]     B --> D[qui]     B --> E[cross-seed]     B --> F[*arr Apps]      F --> G[Sonarr]     F --> H[Radarr]     F --> I[Lidarr]     F --> J[Readarr]     F --> K[Mylar]     F --> L[Whisparr]

---

## Key Features

### Centralized Tracker Management

Configure your trackers once.

Harbrr provides a single source of truth for tracker configuration, authentication, capabilities, categories, and search behavior across your automation stack.

No more duplicating tracker setup across multiple applications.

---

### Shared RSS Feed Caching

Private trackers are a shared resource.

Today, multiple applications often poll the same tracker for nearly identical information.

Without Harbrr:

mermaid flowchart LR     A[Sonarr] --> E[Tracker]     B[Radarr] --> E     C[autobrr] --> E     D[cross-seed] --> E

With Harbrr:

mermaid flowchart LR     A[Sonarr] --> H[Harbrr]     B[Radarr] --> H     C[autobrr] --> H     D[cross-seed] --> H     H --> E[Tracker]

One request. Multiple consumers.

Benefits include:

- Reduced tracker load
- Fewer duplicate requests
- Lower API utilization
- Faster downstream processing
- Better private tracker citizenship
- Improved scalability for larger automation stacks

---

### Intelligent Search Deduplication

A single release can trigger multiple searches from multiple applications.

Instead of repeatedly sending identical searches to the same tracker, Harbrr is designed to intelligently reuse cached results whenever possible.

This reduces:

- Duplicate tracker queries
- Search latency
- Tracker load
- API consumption
- Unnecessary traffic

---

### Built for autobrr

Harbrr is designed to complement autobrr workflows rather than simply coexist with them.

Planned optimizations include:

- Shared tracker intelligence
- Smarter RSS processing
- Release reuse across applications
- Reduced tracker load
- Improved automation efficiency
- Tracker-friendly polling strategies

---

### Built for qui

Harbrr is designed to work alongside qui as part of a modern automation ecosystem.

By centralizing tracker access and search intelligence, Harbrr can provide a common foundation that multiple services can leverage without independently hammering the same trackers.

---

### Cross-Seed Aware

Cross-seeding has unique requirements that traditional indexer managers often overlook.

Harbrr is being designed with cross-seed workflows in mind, including:

- Smarter release matching
- Search reuse and aggregation
- Reduced duplicate tracker activity
- Freeleech-aware matching
- Optional freeleech bypass logic
- Better reuse of existing tracker results

---

### Cardigann Compatibility

Harbrr is built around Cardigann-compatible indexer definitions.

The project aims to leverage the extensive tracker definition ecosystem established by Cardigann, Jackett, and Prowlarr while modernizing the execution engine and overall architecture.

This allows users to benefit from a mature tracker ecosystem without requiring an entirely new definition format.

---

### Full Torznab Compatibility

Harbrr works with:

- autobrr
- qui
- cross-seed
- Sonarr
- Radarr
- Lidarr
- Readarr
- Mylar
- Whisparr
- Any Torznab-compatible application

Existing workflows continue to work while benefiting from Harbrr's centralized intelligence and optimization layer.

---

### Modern Go Architecture

Harbrr is written entirely in Go.

Benefits include:

- Lightweight deployment
- Fast startup times
- Low memory footprint
- Single binary distribution
- Docker-first deployments
- Cross-platform support
- Built-in interactive API docs (Swagger UI at `/api/docs`)

---

## Philosophy

Private trackers should be treated as a shared resource.

Many automation stacks unintentionally generate excessive duplicate traffic through repeated RSS polling, repeated searches, and disconnected application behavior.

Harbrr exists to solve that problem.

Rather than every application independently talking to every tracker, Harbrr provides a centralized intelligence layer that can aggregate, cache, optimize, and distribute tracker data throughout your automation ecosystem.

The result is a cleaner architecture, lower tracker load, and a better experience for both users and tracker operators.

---

## Roadmap

### Phase 1 - Foundation

- Cardigann compatibility
- Tracker authentication
- Search execution engine
- Torznab support
- SQLite backend
- Docker support
- Prowlarr migration tooling

### Phase 2 - Tracker Intelligence

- Shared RSS caching
- Search deduplication
- Tracker request optimization
- Advanced caching strategies
- Improved autobrr workflows

### Phase 3 - Ecosystem Integration

- Deep autobrr integration
- qui integration enhancements
- Cross-seed optimization
- Native tracker implementations
- Advanced release intelligence

### Phase 4 - Future

- Distributed architectures
- Enhanced metadata correlation
- Additional automation integrations
- Expanded ecosystem tooling

---

## Current Status

⚠️ Early Development

Harbrr is currently under active development.

The immediate focus is achieving robust Cardigann compatibility and Torznab interoperability while laying the groundwork for tracker intelligence, RSS aggregation, request deduplication, and deeper autobrr ecosystem integration.

---

## Contributing

Contributions, testing, feedback, and ideas are welcome.

Particularly helpful areas include:

- Cardigann definitions
- Tracker testing
- Torznab interoperability
- autobrr workflows
- qui integration
- cross-seed workflows
- Go development
- Private tracker automation

---

## Vision

Harbrr aims to become the central tracker and indexer fabric for the autobrr ecosystem.

Configure trackers once.

Connect everything.

Search less.

Cache more.

Be kinder to your trackers.

---

## License

harbrr is free software: you can redistribute it and/or modify it under the terms
of the GNU General Public License as published by the Free Software Foundation,
**either version 2 of the License, or (at your option) any later version**
(GPL-2.0-or-later). The full GPL v2 text is in [LICENSE](LICENSE).

---

## Keywords

autobrr, qui, cross-seed, Torznab, Cardigann, Prowlarr alternative, Jackett alternative, private trackers, torrent trackers, RSS caching, search deduplication, tracker intelligence, tracker middleware, indexer manager, indexer proxy, release automation, media automation, Sonarr, Radarr, Lidarr, Readarr, Mylar, Whisparr, Go, Golang, Docker
