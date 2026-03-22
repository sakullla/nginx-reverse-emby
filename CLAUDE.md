# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Nginx-Reverse-Emby is an automated reverse proxy solution designed for Emby, Jellyfin, and various HTTP services. It features a web management panel, automatic SSL certificate renewal with acme.sh, and IPv4/IPv6 dual-stack support.

**Tech Stack:**
- Frontend: Vue 3 + Vite + Pinia + Axios (SPA)
- Backend: Node.js HTTP server (single file: `panel/backend/server.js`)
- Infrastructure: Nginx + Shell scripts for automation
- Deployment: Docker (multi-stage build)

## Architecture

### Three-Layer Architecture

1. **Frontend Panel** (`panel/frontend/`)
   - Vue 3 SPA with component-based architecture
   - Pinia store (`src/stores/rules.js`) manages proxy rules and authentication state
   - Components in `src/components/`: RuleList, RuleForm, RuleItem, L4RuleList, L4RuleForm, CertificateList, TokenConfig
   - API client in `src/api/index.js` communicates with backend via REST

2. **Backend Server** (`panel/backend/server.js`)
   - Simple Node.js HTTP server (no framework dependencies)
   - Manages proxy rules stored in JSON format (`/opt/nginx-reverse-emby/panel/data/proxy_rules.json`)
   - Provides REST API: GET/POST/PUT/DELETE for rules, GET for stats
   - Token-based authentication via `X-Panel-Token` header
   - Triggers nginx config regeneration and reload after rule changes

3. **Infrastructure Layer** (`docker/` scripts)
   - `25-dynamic-reverse-proxy.sh`: **Core script** - reads rules and generates nginx configs dynamically
   - `20-panel-backend.sh`: Starts the Node.js backend server
   - `15-panel-config.sh`: Initializes panel configuration
   - `30-acme-renew.sh`: Manages SSL certificate renewal with acme.sh
   - Nginx config templates: `default.conf.template`, `default.direct.*.conf.template`, `panel.conf.template`

### Data Flow

```
User → Frontend Panel → Backend API → Rules JSON → 25-dynamic-reverse-proxy.sh → Nginx Config → nginx -t → nginx -s reload
```

### Deployment Modes

- **`direct` mode** (default): Container directly handles ports 80/443 and SSL termination
- **`front_proxy` mode**: Container only does internal forwarding; external proxy handles SSL

### Master/Agent Architecture

- **Master**: Runs complete panel and backend, manages rules and config distribution
- **Agent**: Lightweight node running on target hosts, requires only Node.js 18+ and Nginx
- **NAT Agent**: Behind NAT/firewall, polls Master via heartbeat to pull configs (no inbound ports needed)

### Key Directories

- `/opt/nginx-reverse-emby/panel/data`: Persistent data (rules, certs, acme.sh state)
- `/etc/nginx/conf.d/dynamic`: Generated nginx configs for each proxy rule
- `/etc/nginx/stream-conf.d/dynamic`: Generated nginx configs for L4 rules
- `/etc/nginx/templates`: Nginx config templates

## Common Development Commands

### Frontend Development

```bash
# Install dependencies
cd panel/frontend && npm ci

# Development server (with hot reload)
npm run dev

# Production build (outputs to dist/)
npm run build

# Preview production build
npm run preview
```

### Docker Development

```bash
# Build Docker image
docker build -t nginx-reverse-emby .

# Run with docker-compose
docker compose up -d

# View logs
docker compose logs -f

# Rebuild and restart
docker compose up -d --build

# Stop and remove
docker compose down
```

### Nginx Operations

```bash
# Test nginx configuration
nginx -t

# Reload nginx (graceful)
nginx -s reload

# View nginx error log
tail -f /var/log/nginx/error.log

# Check nginx status endpoint
curl http://127.0.0.1:18080/nginx_status
```

### Backend Development

```bash
# Run backend server directly (for testing)
cd panel/backend
node server.js

# Environment variables for backend:
# PANEL_BACKEND_PORT=18081
# API_TOKEN=your-token
# PANEL_AUTO_APPLY=1
```

### Testing Dynamic Proxy Generation

```bash
# Manually trigger proxy config generation
/docker-entrypoint.d/25-dynamic-reverse-proxy.sh

# Check generated configs
ls -la /etc/nginx/conf.d/dynamic/

# View a specific generated config
cat /etc/nginx/conf.d/dynamic/rule_1.conf
```

## Important Implementation Details

### SSL Certificate Management

- Certificates managed by `acme.sh` in `/opt/nginx-reverse-emby/panel/data/.acme.sh`
- Supports HTTP-01 and DNS-01 validation (via DNS API providers like Cloudflare)
- Auto-renewal configured via cron in `30-acme-renew.sh`
- Certificate state tracked in `.state/active_cert_domains`
- Managed certificates: centralized cert management across agents (requires Cloudflare DNS API)

### Authentication Flow

1. Frontend checks for token in localStorage (`panel_token`)
2. All API requests include `X-Panel-Token` header
3. Backend validates token against `API_TOKEN` environment variable
4. If token missing or invalid, frontend shows TokenConfig component

### Config Generation Process

The `25-dynamic-reverse-proxy.sh` script:
1. Reads rules from `proxy_rules.csv` or `proxy_rules.json`
2. Parses each rule's frontend_url (protocol, host, port, path)
3. For HTTPS rules in `direct` mode: requests/installs SSL certificates
4. Generates nginx config file per rule in `/etc/nginx/conf.d/dynamic/`
5. Runs `nginx -t` to validate
6. Executes `nginx -s reload` if validation passes

### L4 (TCP/UDP) Proxy Rules

- Stored in `l4_rules.json`
- Generated configs placed in `/etc/nginx/stream-conf.d/dynamic/`
- Supports TCP and UDP protocols
- Requires nginx stream module

## Debugging Tips

### Frontend Issues

- Check browser console for API errors
- Verify token is set: `localStorage.getItem('panel_token')`
- Check API endpoint: default is `/panel-api` (proxied by nginx to backend)

### Backend Issues

- Check if server is running: `curl http://127.0.0.1:18081/panel-api/rules`
- Verify token: `curl -H "X-Panel-Token: your-token" http://127.0.0.1:18081/panel-api/rules`
- Check backend logs in Docker: `docker compose logs nginx-reverse-emby | grep server.js`

### Nginx Issues

- Always run `nginx -t` before reload
- Check error log: `/var/log/nginx/error.log`
- Verify generated configs: `ls /etc/nginx/conf.d/dynamic/`
- Check if ports are listening: `netstat -tlnp | grep nginx`

### SSL Certificate Issues

- Check acme.sh logs: `cat /opt/nginx-reverse-emby/panel/data/.acme.sh/*.log`
- Verify DNS records for domain validation
- For DNS API: ensure provider credentials are set (e.g., `CF_Token`, `CF_Account_ID`)
- Manual cert request: `$ACME_HOME/acme.sh --issue -d example.com --standalone`

### Agent Issues

- Check agent heartbeat: agents must poll Master within `AGENT_HEARTBEAT_TIMEOUT_MS` (default 90s)
- NAT agents: verify they can reach Master URL
- Check agent logs: `journalctl -u light-agent -f` (if using systemd)
- Verify agent registration: check `agents.json` on Master

## Project-Specific Conventions

- All user-facing text in Chinese (frontend, logs, error messages)
- Backend uses CommonJS (`require`), frontend uses ES modules (`import`)
- Frontend components use Composition API (`<script setup>`)
- Shell scripts use POSIX-compliant syntax (no bashisms) except `scripts/join-agent.sh` which is Bash-specific
- Environment variables prefixed by component: `PANEL_*`, `ACME_*`, `PROXY_*`, `MASTER_*`, `AGENT_*`
- Data persistence: everything under `/opt/nginx-reverse-emby/panel/data`
- Frontend: 2-space indentation, single quotes, no semicolons
- Backend: semicolons, defensive error handling
- Components: `PascalCase.vue`, JS modules: `camelCase`
- Commit style: Conventional Commits (e.g., `feat(panel):`, `fix(agent):`, `docs:`)

## Key Environment Variables

- `API_TOKEN`: Panel authentication token (required in production)
- `PROXY_DEPLOY_MODE`: `direct` or `front_proxy`
- `FRONT_PROXY_PORT`: Container listening port in `front_proxy` mode (default: 3000)
- `PANEL_PORT`: Web panel port (default: 8080)
- `PANEL_ROLE`: Node role - `master` or `agent`
- `PANEL_AUTO_APPLY`: Auto-apply config changes (default: 1)
- `ACME_DNS_PROVIDER`: DNS provider for certificate validation (e.g., `cf`)
- `ACME_EMAIL`: Email for Let's Encrypt notifications
- `ACME_CA`: Certificate authority (default: `letsencrypt`)
- `CF_Token`: Cloudflare API Token (for DNS validation)
- `CF_Account_ID`: Cloudflare Account ID (for DNS validation)
- `MASTER_REGISTER_TOKEN`: Agent registration token
- `MASTER_LOCAL_AGENT_NAME`: Master local node display name
- `MASTER_LOCAL_AGENT_TAGS`: Master local node tags
- `AGENT_NAME`: Agent node name
- `AGENT_TAGS`: Agent node tags (comma-separated)
- `AGENT_CAPABILITIES`: Agent capabilities (default: `http_rules,local_acme,cert_install,l4`)
