# GEMINI.md - Nginx-Reverse-Emby Project Context

This file provides the necessary context and instructions for AI agents working on the **Nginx-Reverse-Emby** project.

## Project Overview

**Nginx-Reverse-Emby** is an automated reverse proxy solution tailored for media servers (Emby, Jellyfin) and general HTTP services. It combines Nginx with a web-based management panel, automated SSL via `acme.sh`, and dynamic configuration generation.

### Core Architecture
- **Frontend Panel** (`panel/frontend/`): Vue 3 (Composition API) + Vite + Pinia. A modern SPA for managing proxy rules.
- **Backend Server** (`panel/backend/server.js`): Lightweight Node.js server (no external frameworks like Express). Manages `proxy_rules.json`, provides a REST API, and triggers system scripts.
- **Automation Scripts** (`docker/`): Shell scripts that bridge the panel and Nginx.
    - `25-dynamic-reverse-proxy.sh`: The core generator that translates JSON rules into Nginx site configs and handles SSL issuance.
    - `20-panel-backend.sh`: Orchestrates the Node.js backend startup.
    - `30-acme-renew.sh`: Manages certificate renewals via cron.
- **Deployment**: Primarily Docker-based (`Dockerfile`, `docker-compose.yaml`) with support for a `host` network mode to simplify IPv6 and multi-port handling.

## Technical Stack & Conventions

### Backend (Node.js)
- **Runtime**: Node.js (uses `http` module directly).
- **Storage**: Flat-file JSON (`/opt/nginx-reverse-emby/panel/data/proxy_rules.json`).
- **Auth**: Simple token-based authentication via `X-Panel-Token` header.
- **Integration**: Uses `child_process.spawnSync` to execute shell scripts for Nginx reloads.

### Frontend (Vue 3)
- **Pattern**: Composition API with `<script setup>`.
- **State Management**: Pinia (`src/stores/rules.js`).
- **Styling**: Vanilla CSS with a focus on a clean, modern UI (standard components in `src/components/base`).
- **API**: Axios-based client in `src/api/index.js`.

### Infrastructure (Nginx & Shell)
- **Config Templates**: Located in `docker/*.template`.
- **Generated Configs**: Placed in `/etc/nginx/conf.d/dynamic/`.
- **SSL**: `acme.sh` integration supporting HTTP-01 (standalone) and DNS-01 (e.g., Cloudflare API).

## Key Development Commands

### Frontend Development
```bash
cd panel/frontend
npm install    # Install dependencies
npm run dev    # Start development server with hot-reload
npm run build  # Build for production (outputs to dist/)
```

### Backend Development
```bash
cd panel/backend
# Requires environment variables usually set by Docker
export API_TOKEN=your_test_token
node server.js
```

### Docker & Integration
```bash
docker compose up -d --build  # Rebuild and start the stack
docker exec -it nginx-reverse-emby /docker-entrypoint.d/25-dynamic-reverse-proxy.sh  # Manually trigger generator
```

## Directory Structure Highlights
- `panel/frontend/src/`: Vue source code.
- `panel/backend/server.js`: Main API logic.
- `docker/`: Shell scripts and Nginx templates.
- `conf.d/`: Example/static Nginx configurations.

## Development Mandates
- **Language**: User-facing UI and messages should be in **Chinese**.
- **Surgical Edits**: When modifying `server.js`, maintain the dependency-free `http` module approach unless explicitly told to add a framework.
- **Safety**: Never hardcode `API_TOKEN`. Always use environment variables.
- **Verification**: After changes to rule generation or Nginx templates, always verify with `nginx -t`.

## Troubleshooting
- **Logs**: Check container logs for backend errors and `/var/log/nginx/error.log` for proxy issues.
- **ACME**: `acme.sh` logs are stored in the persistent `data` volume under `.acme.sh/`.
