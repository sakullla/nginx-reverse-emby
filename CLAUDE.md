# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**Nginx-Reverse-Emby** is a highly automated Bash script for one-click Nginx reverse proxy configuration. It supports IPv4/IPv6, automatic SSL certificate management via acme.sh (including Let's Encrypt short-lived certificates for IPs), and Docker containerization.

## Development Commands

### Testing the Deployment Script

```bash
# Interactive mode
bash deploy.sh

# Non-interactive mode
bash deploy.sh -y <frontend-url> -r <backend-url>

# Example: HTTPS proxy
bash deploy.sh -y https://proxy.example.com -r http://192.168.1.100:8096

# Example: IPv6 with short-lived IP certificate
bash deploy.sh -y https://[2400:db8::1]:9443 -r https://backend.com
```

### Docker Build and Test

```bash
# Build image
docker build -t nginx-reverse-emby .

# Run with proxy rules
docker run -e PROXY_RULE_1="http://frontend.com,http://backend:8080" \
           -e PROXY_RULE_2="http://api.frontend.com,http://api-backend:3000" \
           ghcr.io/sakullla/nginx-reverse-emby:latest
```

### Nginx Configuration Testing

```bash
# Test Nginx configuration
nginx -t

# Check Nginx logs
journalctl -u nginx -n 50
```

## Architecture

### Core Components

1. **deploy.sh** - Main deployment script with dual operation modes:
   - Interactive mode: Wizard-style prompts for user input
   - Non-interactive mode: Command-line arguments for automation

2. **Configuration Templates** (conf `d/`):
   - `p.example.com.conf` - HTTPS configuration template
   - `p.example.com.no_tls.conf` - HTTP configuration template
   - Templates use `envsubst` with variables like `${you_domain}`, `${r_domain_full}`, `${resolver}`

3. **Docker Setup** (docker/):
   - `25-dynamic-reverse-proxy.sh` - Entrypoint that reads `PROXY_RULE_N` env vars and generates configs
   - `default.conf.template` - Docker-specific Nginx config template

### Deployment Flow

```
1. Parameter Parsing → 2. Environment Setup → 3. Interactive Mode (optional)
    ↓
4. Dependency Installation → 5. Config Generation → 6. Certificate Issuance
    ↓
7. Nginx Reload
```

### Key Functions in deploy.sh

| Function | Purpose |
|----------|---------|
| `parse_arguments()` | Parses command-line options using getopt |
| `parse_url()` | Parses URLs supporting IPv6 bracket format `[address]` |
| `install_dependencies()` | Installs Nginx (from official source), acme.sh, socat, cron |
| `generate_nginx_config()` | Renders templates via `envsubst` |
| `issue_certificate()` | Obtains SSL certs (Standalone or DNS mode) |
| `remove_domain_config()` | Safely removes config and certs |

### URL Parsing Format

The `parse_url()` function outputs: `protocol|domain|port|path`

- IPv6 addresses are wrapped in brackets: `[2400:db8::1]`
- Domain/IP extraction handles both bracketed IPv6 and regular IPv4/domain names
- Protocol defaults to https if not specified

### Configuration File Naming

Configs are named precisely as `{clean_domain}.{port}.conf` to allow multiple ports per domain:
- `example.com.443.conf`
- `example.com.8443.conf`

### Certificate Management

| Mode | Use Case |
|------|----------|
| Standalone (HTTP-01) | Single domains and IP addresses |
| DNS (DNS-01) | Wildcard certificates via Cloudflare API |
| Short-lived | IP addresses (6-day validity, auto-renewed) |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `CONF_HOME` | Remote config template base URL |
| `ACME_INSTALL_URL` | acme.sh installation script URL |
| `BACKUP_DIR` | Configuration backup directory (`/etc/nginx/backup`) |

### Docker Environment Variables

| Variable | Format |
|----------|--------|
| `PROXY_RULE_N` | `frontend-url,backend-url` (N = 1, 2, 3...) |
| `NGINX_LOCAL_RESOLVERS` | DNS resolvers (default: `1.1.1.1`) |
| `NGINX_ENTRYPOINT_QUIET_LOGS` | Set non-empty to suppress logs |

## Code Conventions

### Bash Scripting

- Always use strict mode: `set -e; set -o pipefail`
- Error handling with `trap 'handle_error $LINENO' ERR`
- Use `$SUDO` variable for permission handling (auto-detects if sudo needed)
- Log functions: `log_info`, `log_success`, `log_warn`, `log_error`
- Color variables: `RED`, `GREEN`, `YELLOW`, `BLUE`, `NC`

### Template Variables

For `conf.d/` templates:
- `${you_domain}` - Frontend domain/IP (IPv6 with brackets)
- `${you_frontend_port}` - Frontend listening port
- `${resolver}` - DNS resolver configuration
- `${format_cert_domain}` - Certificate domain (clean, no brackets)
- `${you_domain_path}` - Frontend path
- `${you_domain_path_rewrite}` - Path rewrite rules
- `${r_domain_full}` - Complete backend URL

## File Locations

| Path | Purpose |
|------|---------|
| `/etc/nginx/conf.d/{domain}.{port}.conf` | Site configuration |
| `/etc/nginx/certs/{cert_domain}/` | SSL certificate storage |
| `/etc/nginx/backup/` | Configuration backups |
| `$HOME/.acme.sh/acme.sh` | acme.sh installation |

## China Network Optimization

The script automatically detects Chinese IP addresses via Cloudflare CDN trace and uses `gh.llkk.cc` proxy for GitHub resources. This can be overridden with `--gh-proxy`.
