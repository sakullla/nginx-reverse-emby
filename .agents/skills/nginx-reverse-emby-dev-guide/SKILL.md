---
name: nginx-reverse-emby-dev-guide
description: Maintain and extend the Nginx-Reverse-Emby repository with behavior-accurate changes across deploy.sh, nginx templates, Docker dynamic proxy scripts, and agent docs. Use when tasks involve CLI option changes, URL parsing, certificate flow, template variable wiring, PROXY_RULE runtime behavior, or regression-safe documentation updates.
---

# Nginx-Reverse-Emby Dev Guide

Follow this skill to make safe, implementation-accurate updates in this repository.

## Workflow

1. Treat `deploy.sh` as the source of truth.
2. Read only required files before editing:
- `deploy.sh`
- `conf.d/p.example.com.conf`
- `conf.d/p.example.com.no_tls.conf`
- `docker/25-dynamic-reverse-proxy.sh`
- `docker/default.conf.template`
- docs to be updated (`README.md`, `AGENTS.md`, `CLAUDE.md`)
3. Build a quick behavior matrix from code, not from stale docs.
4. Apply code changes first, then update docs in the same patch.
5. Run lightweight consistency checks (option names, function names, variable names, file paths).
6. Report known gaps if environment blocks runtime validation.

## High-Risk Areas

Check these areas explicitly on every related change:
- CLI option parsing in `parse_arguments()`
- URL parsing output contract `proto|domain|port|path`
- IPv6 bracket handling (`[addr]`)
- template variable export + `envsubst` var list sync
- cert mode split (Standalone vs DNS)
- remove flow safety for shared/wildcard certs
- Docker `PROXY_RULE_N` contiguous index behavior

## Required Sync Rules

When changing CLI options:
1. Update `getopt` long/short options.
2. Update help text in `show_help()`.
3. Update docs and examples that mention the option.

When changing template variables:
1. Update `generate_nginx_config()` exports.
2. Update `vars` list passed to `envsubst`.
3. Update template files that consume the variable.

When changing Docker behavior:
1. Keep rule format `frontend_url,backend_url` unless explicitly migrating.
2. Re-check filename convention `{domain}.{port}.conf`.
3. Confirm stop-on-first-missing-index behavior is intentional.

## References

Load `references/project-guide.md` for:
- current function map
- argument truth table
- template variable contract
- regression checklist and known drifts
