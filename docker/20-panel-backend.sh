#!/bin/sh
set -eu

if [ "${PANEL_ENABLED:-1}" = "0" ]; then
    exit 0
fi

AGENT_ENV_FILE="${PANEL_AGENT_ENV_FILE:-/opt/nginx-reverse-emby/panel/data/agent.env}"
if [ -f "$AGENT_ENV_FILE" ]; then
    set -a
    # shellcheck disable=SC1090
    . "$AGENT_ENV_FILE"
    set +a
fi

export PANEL_BACKEND_HOST="${PANEL_BACKEND_HOST:-127.0.0.1}"
export PANEL_BACKEND_PORT="${PANEL_BACKEND_PORT:-18081}"
export PANEL_RULES_FILE="${PANEL_RULES_FILE:-/opt/nginx-reverse-emby/panel/data/proxy_rules.csv}"
export PANEL_ROLE="${PANEL_ROLE:-master}"
export AGENT_NAME="${AGENT_NAME:-$(hostname)}"
export AGENT_PUBLIC_URL="${AGENT_PUBLIC_URL:-}"
export AGENT_API_TOKEN="${AGENT_API_TOKEN:-${API_TOKEN:-}}"
export MASTER_REGISTER_TOKEN="${MASTER_REGISTER_TOKEN:-${API_TOKEN:-}}"

mkdir -p "$(dirname "$PANEL_RULES_FILE")"
touch "$PANEL_RULES_FILE"

node /opt/nginx-reverse-emby/panel/backend/server.js &
