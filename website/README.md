# harbrr docs site

User-facing documentation for harbrr — written in plain English for people running it,
not for people hacking on it. (Design notes and the build plan live in the repo's
top-level `docs/` folder; this folder is the *product* documentation.)

Built with [Docusaurus](https://docusaurus.io/). The marketing landing page lives at `/`;
the docs themselves are under `/docs/`.

## Structure

```text
website/
├── docusaurus.config.js    # site config: title, nav, branding, deploy target
├── sidebars.js             # docs sidebar — mirrors the old mkdocs.yml nav 1:1
├── src/
│   ├── pages/index.js      # landing page (hero + feature grid)
│   ├── components/         # HomepageFeatures (the feature-card grid)
│   └── css/custom.css      # brand colors (#0074ca light / #3b9cf6 dark)
├── static/img/              # logo (light/dark) + favicon
└── docs/
    ├── index.md            # docs home (served at /docs/)
    ├── getting-started.md
    ├── coverage.md
    ├── test-status.md
    ├── configuration.md
    ├── api.md
    ├── guides/
    └── features/
```

The pages are plain Markdown. Admonitions use Docusaurus `:::note` / `:::tip` /
`:::warning` blocks instead of MkDocs' `!!!` syntax.

## Previewing

```bash
cd website
npm install
npm start        # live preview at http://localhost:3000
```

Before committing, run a production build — it's the only mode that enforces broken
links/anchors (`onBrokenLinks` / `onBrokenMarkdownLinks` are both `'throw'`):

```bash
npm run build
```

## Writing style

- Plain English. Explain the *why* and the *effect*, not the Go internals.
- Lead with what the reader gets, then how to control it.
- Use real, copy-pasteable config and concrete numbers.
