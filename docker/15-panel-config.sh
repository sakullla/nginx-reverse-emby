#!/bin/sh
set -eu

if [ "${PANEL_ENABLED:-1}" = "0" ]; then
    rm -f /etc/nginx/conf.d/00-panel.conf
    exit 0
fi

template_file="/etc/nginx/templates/panel.conf.template"
output_file="/etc/nginx/conf.d/00-panel.conf"

if [ ! -f "$template_file" ]; then
    echo "$0: error: panel template '$template_file' was not found" >&2
    exit 1
fi

panel_port="${PANEL_PORT:-8080}"
panel_backend_port="${PANEL_BACKEND_PORT:-18081}"
export panel_port
export panel_backend_port

envsubst '${panel_port} ${panel_backend_port}' < "$template_file" > "$output_file"

mkdir -p /opt/nginx-reverse-emby/panel/data
