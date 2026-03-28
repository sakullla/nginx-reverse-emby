#!/bin/sh
set -eu

template_file="/opt/nginx-reverse-emby/nginx/nginx.conf.template"
output_file="/etc/nginx/nginx.conf"

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

[ -f "$template_file" ] || {
    echo "$0: error: nginx template '$template_file' was not found" >&2
    exit 1
}

nginx_client_max_body_size="${NGINX_CLIENT_MAX_BODY_SIZE:-5g}"
nginx_client_body_buffer_size="${NGINX_CLIENT_BODY_BUFFER_SIZE:-512k}"

if supports_ipv6; then
    status_ipv6_listen='        listen [::1]:18080;'
    status_ipv6_allow='            allow ::1;'
else
    status_ipv6_listen=''
    status_ipv6_allow=''
fi

awk \
    -v nginx_client_max_body_size="$nginx_client_max_body_size" \
    -v nginx_client_body_buffer_size="$nginx_client_body_buffer_size" \
    -v status_ipv6_listen="$status_ipv6_listen" \
    -v status_ipv6_allow="$status_ipv6_allow" \
    '
    { gsub(/\$\{nginx_client_max_body_size\}/, nginx_client_max_body_size) }
    { gsub(/\$\{nginx_client_body_buffer_size\}/, nginx_client_body_buffer_size) }
    { gsub(/\$\{status_ipv6_listen\}/, status_ipv6_listen) }
    { gsub(/\$\{status_ipv6_allow\}/, status_ipv6_allow) }
    { print }
    ' "$template_file" > "$output_file"

# Ensure stream-conf.d and limit_conn_zones.inc exist for nginx include
mkdir -p /etc/nginx/stream-conf.d/dynamic
touch /etc/nginx/stream-conf.d/limit_conn_zones.inc
