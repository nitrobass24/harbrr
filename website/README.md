# harbrr docs site

User-facing documentation for harbrr — written in plain English for people running it,
not for people hacking on it. (Design notes and the build plan live in the repo's
top-level `docs/` folder; this folder is the *product* documentation.)

## Structure

```
website/
├── mkdocs.yml              # optional site config (MkDocs + Material) — see "Previewing"
└── docs/
    ├── index.md            # landing page
    └── features/
        └── search-results-cache.md
```

The pages are plain Markdown. They render fine as a GitHub wiki, in a plain Markdown
viewer, or through a static-site generator. `mkdocs.yml` is provided as a ready-to-go
starting point but nothing here depends on it.

## Previewing (optional)

With [MkDocs](https://www.mkdocs.org/) + the Material theme installed:

```bash
pip install mkdocs-material
cd website
mkdocs serve        # live preview at http://127.0.0.1:8000
```

To use a different generator (Docusaurus, Astro Starlight, etc.), point it at the
`docs/` content tree — the Markdown is portable.

## Writing style

- Plain English. Explain the *why* and the *effect*, not the Go internals.
- Lead with what the reader gets, then how to control it.
- Use real, copy-pasteable config and concrete numbers.
