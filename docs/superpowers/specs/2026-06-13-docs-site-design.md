# Docs Site Design

## Goal

Add a documentation website to the existing repository without using the root `docs/` directory as the site source. The site should be suitable for GitHub Pages publishing and should support documentation pages that include screenshots captured through Chrome MCP during writing and verification.

## Scope

The documentation site source will live in `docs-site/`. The existing root `docs/` directory remains available for temporary or miscellaneous project documents, except for this design/spec path created by the planning workflow.

The first implementation should create the site infrastructure, navigation, starter pages, and GitHub Pages workflow. It should not attempt to fully rewrite every README section into polished long-form documentation in one pass.

## Recommended Approach

Use VitePress for the documentation site.

VitePress is a good fit because this repository already uses the Vue ecosystem for the panel frontend, the target style is close to projects like Xray docs, and the content model is Markdown-first. It also keeps the documentation build separate from both the Go control plane and the Vue management panel.

## Repository Layout

```text
docs-site/
  package.json
  index.md
  guide/
    getting-started.md
    docker-compose.md
    agent.md
  reference/
    environment.md
    certificates.md
    relay.md
    wireguard.md
  operations/
    backup-restore.md
    migration.md
    faq.md
  public/
    screenshots/
  .vitepress/
    config.mjs
    theme/
      index.js
      custom.css

.github/
  workflows/
    docs-pages.yml
```

## Content Model

The initial site should prioritize practical user documentation:

- Home page: short product summary and primary entry links.
- Quick start: Docker Compose deployment as the recommended path.
- Agent setup: Linux, macOS, Windows client notes, NAT agent behavior.
- Configuration reference: environment variables grouped by subsystem.
- Feature guides: certificates, HTTP/L4 rules, Relay, WireGuard, traffic statistics.
- Operations: backup, restore, migration, upgrades, and FAQ.

The README remains a compact project overview. The docs site becomes the expanded operational manual.

## GitHub Pages

Add a GitHub Actions workflow at `.github/workflows/docs-pages.yml`.

The workflow should:

- Trigger on pushes to `main` that touch `docs-site/**` or the workflow file.
- Allow manual `workflow_dispatch`.
- Use Node.js to install dependencies inside `docs-site/`.
- Build the VitePress site.
- Upload `docs-site/.vitepress/dist` as the Pages artifact.
- Deploy using GitHub Pages Actions.

VitePress `base` should support repository Pages paths. For this repository, the default base should be `/nginx-reverse-emby/`. The workflow may override `VITEPRESS_BASE` later if a custom domain is added.

## Screenshot Workflow

Screenshots used by documentation should live under:

```text
docs-site/public/screenshots/
```

Markdown references should use:

```md
![Panel example](/screenshots/panel-example.png)
```

When writing panel UI documentation, Chrome MCP can be used to:

1. Start the relevant local dev server or packaged stack.
2. Navigate the browser to the panel page being documented.
3. Capture screenshots into `docs-site/public/screenshots/`.
4. Reference those screenshots from the Markdown page.

Screenshots should avoid secrets, real tokens, private domains, certificates, or runtime files from `panel/data/`. If a screen contains sensitive data, use a local sample environment or redact the UI before capturing.

## Build Commands

Documentation commands should be local to `docs-site/`:

```bash
cd docs-site
npm install
npm run dev
npm run build
npm run preview
```

The root project does not need to depend on the documentation toolchain.

## Verification

Minimum verification for the implementation:

- `cd docs-site && npm install`
- `cd docs-site && npm run build`
- Confirm the generated output exists at `docs-site/.vitepress/dist`.
- Confirm the GitHub Pages workflow references the same build output.

If screenshot pages are added, verify at least one page renders the referenced image correctly in local preview.

## Non-Goals

- Do not move or repurpose the root `docs/` directory for the public documentation site.
- Do not merge documentation routing into `panel/frontend`.
- Do not require Docker image builds for documentation-only changes.
- Do not commit generated VitePress build output.
- Do not commit screenshots containing credentials, register tokens, API tokens, private certificates, or production hostnames.
