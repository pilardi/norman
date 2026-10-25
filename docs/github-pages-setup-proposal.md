---
title: "GitHub Pages Setup Proposal"
type: source
updated: 2026-10-25
tags: [github-pages, ci-cd, docs, scalar]
workspace: norman
---

# GitHub Pages Setup Proposal

## Problem

The `docs/` folder in the `master` branch is pointed to by GitHub Pages but produces nothing useful:
- `.md` files are served as raw text (no markdown→HTML rendering).
- `api-reference.html` exists but may not render if the Scalar CDN script fails or if Jekyll mode interferes.
- No CI workflow regenerates the HTML on every push.

## Goals

1. Serve rendered API reference HTML from `docs/api-reference.html`.
2. Pre-render `.md` files to `.html` so they appear as proper pages.
3. Apply a custom color palette (`#000000`, `#D4AF37`, `#A52A2A`, `#E5E5E5`, `#1C1C1C`).
4. Link markdown content to the API reference page and vice versa.
5. Automate regeneration via CI on every push to `master`.

## Changes Required

### 1. Add `.nojekyll` to `docs/`

Prevents GitHub Pages from attempting Jekyll processing, ensuring static files are served as-is.

### 2. Inject Custom CSS into Scalar Output

Modify `pkg/docs/generator.go` and `cmd/docs-gen/main.go` to accept a custom CSS string via `CustomOptions.CustomCSS`. The palette:

```css
:root {
    --bg-color: #000000;
    --color-gold: #D4AF37;
    --color-red: #A52A2A;
    --color-silver: #E5E5E5;
    --color-charcoal: #1C1C1C;
}
```

### 3. Add GitHub Pages CI Workflow

`.github/workflows/pages.yml` — on push to `master`:
- Run `make generate-docs` (regenerates `docs/api-reference.html`).
- Convert all `.md` files in `docs/` → `.html` (using a Go-based or CLI tool).
- Deploy the `docs/` folder via `peaceiris/actions-gh-pages`.

### 4. Link Markdown Files to API Reference

Add a link at the top of each `.md` file:
```markdown
```

And add an `index.md` / `index.html` landing page that lists all docs with links.

### 5. Update `Makefile`

Add a `generate-docs-html` target that converts `.md` → `.html` alongside the existing `generate-docs`.

## Order of Execution

1. Add `.nojekyll`
2. Modify generator to accept custom CSS
3. Write the Pages workflow YAML
4. Add markdown-to-html conversion (Go tool or CLI)
5. Update Makefile targets
6. Add links in existing `.md` files and create `index.md`

## Files Affected

| File | Change |
|------|--------|
| `docs/.nojekyll` | New file |
| `pkg/docs/generator.go` | Add `CustomCSS` field to `Options` |
| `cmd/docs-gen/main.go` | Add `--css` flag |
| `.github/workflows/pages.yml` | New file (CI pipeline) |
| `Makefile` | Add `generate-docs-html` target |
| `docs/index.md` | New landing page |
| All existing `.md` in `docs/` | Add API reference link header |
