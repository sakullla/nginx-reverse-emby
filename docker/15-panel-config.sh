#!/bin/sh
set -eu

if [ "${PANEL_ENABLED:-1}" = "0" ]; then
    rm -f /etc/nginx/conf.d/00-panel.conf
    exit 0
fi

# 使用 /opt 目录下的模板，避免被 20-envsubst-on-templates.sh 处理
template_file="/opt/nginx-reverse-emby/panel/panel.conf.template"
output_file="/etc/nginx/conf.d/00-panel.conf"

if [ ! -f "$template_file" ]; then
    echo "$0: error: panel template '$template_file' was not found" >&2
    exit 1
fi

panel_port="${PANEL_PORT:-8080}"
panel_backend_port="${PANEL_BACKEND_PORT:-18081}"
panel_client_max_body_size="${NGINX_CLIENT_MAX_BODY_SIZE:-5g}"

supports_ipv6() {
    if [ -n "${NGINX_ENABLE_IPV6:-}" ]; then
        case "$(printf '%s' "$NGINX_ENABLE_IPV6" | tr '[:upper:]' '[:lower:]')" in
            1|true|yes|on) return 0 ;;
            *) return 1 ;;
        esac
    fi

    node -e "
        const net = require('net')
        const server = net.createServer()
        server.once('error', () => process.exit(1))
        server.listen({ host: '::1', port: 0 }, () => {
          server.close(() => process.exit(0))
        })
    " >/dev/null 2>&1
}

if supports_ipv6; then
    panel_listen_ipv6_line="    listen [::]:${panel_port};"
else
    panel_listen_ipv6_line=""
fi

export panel_port
export panel_backend_port
export panel_client_max_body_size
export panel_listen_ipv6_line

envsubst '${panel_port} ${panel_backend_port} ${panel_client_max_body_size} ${panel_listen_ipv6_line}' < "$template_file" > "$output_file"

mkdir -p /opt/nginx-reverse-emby/panel/data
