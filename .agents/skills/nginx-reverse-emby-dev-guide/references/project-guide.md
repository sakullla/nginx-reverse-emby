# Project Guide Reference

## 1. Function Map (`deploy.sh`)

- `parse_url()`: parse URL into `proto|domain|port|path`, supports bracketed IPv6.
- `parse_arguments()`: parse all CLI args using `getopt`.
- `install_dependencies()`: detect distro, install nginx/acme/socat/cron.
- `generate_nginx_config()`: render templates via `envsubst` and write conf.
- `issue_certificate()`: select and execute cert issuance/install flow.
- `remove_domain_config()`: remove target config and optional cert artifacts safely.
- `test_and_reload_nginx()`: run `nginx -t`, then reload/restart.

## 2. CLI Truth Table (current implementation)

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

## 3. Template Variable Contract

Values exported by `generate_nginx_config()`:
- `${you_domain}`
- `${you_frontend_port}`
- `${resolver}`
- `${format_cert_domain}`
- `${you_domain_path}`
- `${you_domain_path_rewrite}`
- `${r_domain_full}`

Any new template variable must be added in all three places:
1. export statements
2. `vars` string
3. consuming template

## 4. Docker Runtime Contract

- Input vars: `PROXY_RULE_1`, `PROXY_RULE_2`, ...
- Rule format: `frontend_url,backend_url`
- Filename format: `{domain}.{frontend_port}.conf`
- Resolver env: `NGINX_LOCAL_RESOLVERS` (default `1.1.1.1`)
- Loop behavior: stop scanning at first missing index

## 5. Regression Checklist

Host mode:
1. Validate config syntax (`nginx -t`).
2. Deploy one HTTPS domain case.
3. Deploy one HTTP (no TLS) case.
4. Deploy one IPv6 frontend case.
5. Remove one config via full URL + `--yes`.

Docker mode:
1. Build image.
2. Start with contiguous rules.
3. Verify generated file count and naming.
4. Verify path routing and redirects.

## 6. Known Drifts to Track

- Some docs still mention `--template`, while code uses `--template-domain-config`.
- `--remove` expects scheme-inclusive URL for reliable parsing.
- `--parse-cert-domain` extraction condition is stricter than generic wording.
- Docker rule scan is contiguous-only; index gaps truncate processing.
