# CLAUDE.md

This file is the source-of-truth guide for AI coding agents working in this repository.

## Project Summary

Nginx-Reverse-Emby is a Bash-first automation project for one-command Nginx reverse proxy deployment. It supports:
- IPv4/IPv6 frontend and backend URLs
- Automatic TLS issuance/install via acme.sh
- Optional DNS API validation (Cloudflare included)
- Docker runtime config generation from `PROXY_RULE_N`

Primary implementation lives in `deploy.sh`.

## Core Files

- `deploy.sh`: full deploy/remove lifecycle
- `conf.d/p.example.com.conf`: HTTPS template (HTTP/3 + QUIC)
- `conf.d/p.example.com.no_tls.conf`: HTTP-only template
- `nginx.conf`: host nginx main config template
- `docker/25-dynamic-reverse-proxy.sh`: Docker entrypoint config generator
- `docker/default.conf.template`: Docker server template
- `docker/nginx.conf`: Docker nginx main config
- `.github/workflows/docker-build.yml`: GHCR build/push

## Runtime Flow (deploy.sh)

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
- `--remove` expects a full URL including scheme.
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
- reads `PROXY_RULE_1`, `PROXY_RULE_2`, ...
- rule format: `frontend_url,backend_url`
- writes `/etc/nginx/conf.d/{domain}.{frontend_port}.conf`
- stops scanning when first index is missing (no gaps allowed)
- default resolver is `1.1.1.1`, override with `NGINX_LOCAL_RESOLVERS`

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
2. Consider current "contiguous index" stop behavior.
3. Re-test domain path extraction edge cases.

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
3. verify generated file count and names
4. verify path forwarding and redirects

## Known Documentation Drift

- Some docs mention `--template`; implementation uses `--template-domain-config`.
- `--remove` examples without scheme are unsafe; use full URLs.
- `--parse-cert-domain` behavior is stricter than generic "root extraction" wording.
- Docker rule scanning is contiguous-only; gaps truncate processing.

If implementation changes any of these points, update `README.md`, `AGENTS.md`, and `CLAUDE.md` in the same patch.
