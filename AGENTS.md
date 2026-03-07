# AGENTS.md

This file is the source-of-truth operational guide for AI coding agents working in this repository.

> Consistency rule: Keep this file aligned with `CLAUDE.md`. If implementation changes behavior, update `README.md`, `AGENTS.md`, and `CLAUDE.md` in the same patch.

## Project Summary

Nginx-Reverse-Emby is a Bash-first automation project for one-command Nginx reverse proxy deployment. It supports:
- IPv4/IPv6 frontend and backend URLs
- Automatic TLS issuance/install via acme.sh
- Optional DNS API validation (Cloudflare included)
- Docker runtime config generation from `PROXY_RULE_N` and panel-managed rule files
- Docker dual-mode deployment via env (`front_proxy` and `direct` with optional ACME cert management)

Primary implementation lives in `deploy.sh`.

## Core Files

- `deploy.sh`: full deploy/remove lifecycle
- `conf.d/p.example.com.conf`: HTTPS template (HTTP/3 + QUIC)
- `conf.d/p.example.com.no_tls.conf`: HTTP-only template
- `nginx.conf`: host nginx main config template
- `panel/backend/server.js`: Docker panel backend API
- `panel/frontend/index.html`: Docker panel frontend
- `docker/25-dynamic-reverse-proxy.sh`: Docker entrypoint config generator
- `docker/15-panel-config.sh`: Docker panel nginx config renderer
- `docker/20-panel-backend.sh`: Docker panel backend bootstrap
- `docker/default.conf.template`: Docker server template
- `docker/default.direct.no_tls.conf.template`: Docker direct mode HTTP template
- `docker/default.direct.tls.conf.template`: Docker direct mode HTTPS template
- `docker/panel.conf.template`: Docker panel server template
- `docker/nginx.conf`: Docker nginx main config
- `Dockerfile`: multi-stage image build (includes frontend build)
- `.github/workflows/docker-build.yml`: GHCR build/push

## Runtime Flow (`deploy.sh`)

`main()` order:
1. `parse_arguments`
2. `remove_domain_config` branch if `--remove` is set
3. `setup_env`
4. `prompt_interactive_mode`
5. `display_summary`
6. `install_dependencies`
7. `generate_nginx_config`
8. `issue_certificate`
9. `test_and_reload_nginx`

Script safety model:
- `set -e`
- `set -o pipefail`
- `trap 'handle_error $LINENO' ERR`
- automatic `sudo` fallback when not root

## CLI Arguments (actual implementation)

Supported options:
- `-y, --you-domain <URL>`
- `-r, --r-domain <URL>`
- `-m, --cert-domain <domain>`
- `-d, --parse-cert-domain`
- `-D, --dns <provider>`
- `-R, --resolver <dns list>`
- `-c, --template-domain-config <path|url>`
- `--gh-proxy <url>`
- `--cf-token <token>`
- `--cf-account-id <id>`
- `--remove <URL>`
- `-Y, --yes`
- `-h, --help`

Important behavior notes:
- Long option is `--template-domain-config` (not `--template`).
- `--remove` should use a full URL including scheme for deterministic matching.
- `parse_url()` format is `proto|domain|port|path`; bracketed IPv6 is supported.
- `--parse-cert-domain` auto-root extraction is only applied when domain matches `*.*.*`.

## Config Rendering Model

Rendered target paths:
- `/etc/nginx/conf.d/{clean_domain}.{port}.conf`
- `/etc/nginx/certs/{format_cert_domain}/`
- backup path: `/etc/nginx/backup/`

Template variables exported by `generate_nginx_config()`:
- `${you_domain}`
- `${you_frontend_port}`
- `${resolver}`
- `${format_cert_domain}`
- `${you_domain_path}`
- `${you_domain_path_rewrite}`
- `${r_domain_full}`

Path behavior:
- non-root frontend path generates rewrite rule automatically
- backend URL is composed from protocol + host + optional port

## Certificate Behavior

- TLS disabled (`no_tls=yes`) skips certificate issuance.
- IP frontends use short-lived profile: `--certificate-profile shortlived --days 6`.
- DNS mode uses `issue_certificate_dns()`.
- Standalone mode uses `issue_certificate_standalone()`.
- Cloudflare mode consumes `CF_Token` and `CF_Account_ID`.

## Remove Behavior

- resolves target by `domain + port`
- requires explicit `--yes` in non-interactive mode
- detects shared/wildcard cert usage and avoids unsafe deletion
- validates nginx config before reload/restart

## Docker Mode

`docker/25-dynamic-reverse-proxy.sh`:
- reads `PROXY_RULE_1`, `PROXY_RULE_2`, ... (contiguous scan)
- merges panel rules from `PANEL_RULES_FILE` (csv lines: `frontend_url,backend_url`)
- rule format: `frontend_url,backend_url`
- writes `/etc/nginx/conf.d/dynamic/{domain}.{effective_frontend_port}.conf`
- env rule scan stops when first index is missing (no gaps allowed)
- default resolver is `1.1.1.1`, override with `NGINX_LOCAL_RESOLVERS`
- `PROXY_DEPLOY_MODE=front_proxy` keeps HTTP passthrough mode for upstream TLS termination
- front proxy listen port is controlled by `FRONT_PROXY_PORT` (default `3000`), not by rule URL port
- `PROXY_DEPLOY_MODE=direct` renders HTTP/HTTPS configs by frontend URL scheme
- direct HTTPS mode uses per-domain cert files in `DIRECT_CERT_DIR` (default `/etc/nginx/certs`)
- direct cert handling supports `DIRECT_CERT_MODE=acme|manual` (default `acme`)
- direct cert cleanup on rule removal can be controlled by `DIRECT_CERT_CLEANUP` (default enabled)
- ACME envs: `ACME_EMAIL`, `ACME_DNS_PROVIDER`, `ACME_HOME`, `ACME_CA`, `ACME_STANDALONE_STOP_NGINX`
- Docker image installs `cron`/`crontab` for acme.sh bootstrap
- container startup launches an ACME renew loop for `direct + acme`, controlled by `ACME_AUTO_RENEW` and `ACME_RENEW_INTERVAL` (default `86400`)
- direct ACME commands pin `home/config-home/cert-home` to `ACME_HOME`, avoiding fallback to `/root/.acme.sh`
- direct runtime apply returns a clear error when standalone issuance would conflict with nginx already occupying port `80`
- panel backend runs on container stdout/stderr; nginx apply commands are executed with captured stdio for reliable `/dev/stdout` logging
- Docker nginx logging uses `/proc/1/fd/1` and `/proc/1/fd/2` so config tests from child processes do not depend on inherited `/dev/stdout`
- direct ACME `install-cert` reload hooks are best-effort so one in-flight certificate installation does not fail because another rule's cert files are not ready yet
- direct ACME issuance checks existing acme.sh record first, then installs cert files (deploy.sh-aligned)
- direct ACME DNS/standalone paths clean stale records and retry once on first issuance failure
- when `ACME_DNS_PROVIDER` is set but frontend host is IP, direct mode falls back to standalone challenge
- auto renew loop envs: `ACME_AUTO_RENEW` and `ACME_RENEW_INTERVAL` (seconds)

Panel mode:
- default panel URL is `http://<host>:8080/` (`PANEL_PORT`)
- panel API is proxied by nginx to backend `PANEL_BACKEND_PORT` (default `18081`)
- panel mutations can auto-apply nginx config (`PANEL_AUTO_APPLY`, default enabled)

## Development Rules

When editing `deploy.sh`:
1. Keep strict mode and trap behavior intact.
2. For new args, update getopt + help text + docs together.
3. For new template vars, update export list and envsubst var list together.
4. Preserve IPv6 bracket compatibility in URL parsing.
5. Keep remove flow conservative around shared certs.

When editing templates:
1. Keep variable names aligned with exports in `deploy.sh`.
2. Validate both TLS and no-TLS templates.
3. Re-check `proxy_redirect` and `/backstream` behavior.

When editing Docker entrypoint:
1. Preserve `PROXY_RULE_N` compatibility.
2. Keep current env "contiguous index" stop behavior.
3. Keep panel file rule format `frontend_url,backend_url`.
4. Re-test domain path extraction edge cases.

When editing panel backend/frontend:
1. Keep API routes `/api/rules` and `/api/apply` stable for frontend compatibility.
2. Keep rule persistence compatible with `PANEL_RULES_FILE`.
3. Ensure apply flow remains `generate -> nginx -t -> nginx -s reload`.

## Validation Checklist

Host mode:
1. `nginx -t`
2. deploy HTTPS domain once
3. deploy IPv6 URL once
4. deploy HTTP(no TLS) once
5. remove with `--remove https://domain:port --yes`

Docker mode:
1. `docker build -t nginx-reverse-emby .`
2. run with contiguous `PROXY_RULE_1..N`
3. verify panel is reachable from `PANEL_PORT` (default `8080`)
4. verify generated file count and names in `/etc/nginx/conf.d/dynamic/`
5. verify path forwarding and redirects
6. verify `PROXY_DEPLOY_MODE=front_proxy` works behind upstream nginx (HTTP only)
7. verify `PROXY_DEPLOY_MODE=direct` can issue/install certs for HTTPS rules
8. verify deleting a rule cleans stale cert directory/record when direct mode is enabled
9. verify auto renew loop runs when `DIRECT_CERT_MODE=acme`

## Known Documentation Drift

- Some docs mention `--template`; implementation uses `--template-domain-config`.
- `--remove` examples without scheme are unsafe; use full URLs.
- `--parse-cert-domain` behavior is stricter than generic "root extraction" wording.
- Docker env rule scanning is contiguous-only; gaps truncate processing.

If implementation changes any of these points, update `README.md`, `AGENTS.md`, and `CLAUDE.md` in the same patch.
