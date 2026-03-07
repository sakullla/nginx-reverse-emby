#!/bin/sh
set -eu

if [ "${PANEL_ENABLED:-1}" = "0" ]; then
    exit 0
fi

export PANEL_BACKEND_HOST="${PANEL_BACKEND_HOST:-127.0.0.1}"
export PANEL_BACKEND_PORT="${PANEL_BACKEND_PORT:-18081}"
export PANEL_RULES_FILE="${PANEL_RULES_FILE:-/opt/nginx-reverse-emby/panel/data/proxy_rules.csv}"

mkdir -p "$(dirname "$PANEL_RULES_FILE")"
touch "$PANEL_RULES_FILE"

node /opt/nginx-reverse-emby/panel/backend/server.js &
