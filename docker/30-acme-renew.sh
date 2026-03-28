#!/bin/sh
set -eu

DATA_ROOT="${DATA_ROOT:-/opt/nginx-reverse-emby/panel/data}"
ACME_HOME="${ACME_HOME:-$DATA_ROOT/.acme.sh}"
ACME_SCRIPT="$ACME_HOME/acme.sh"
PROXY_DEPLOY_MODE="${PROXY_DEPLOY_MODE:-front_proxy}"
DIRECT_CERT_MODE="${DIRECT_CERT_MODE:-acme}"
MANAGED_CERTS_POLICY_JSON="${PANEL_MANAGED_CERTS_POLICY_JSON:-$DATA_ROOT/managed_cert_policy.json}"
DIRECT_CERT_DIR="${DIRECT_CERT_DIR:-$DATA_ROOT/certs}"
NGINX_BIN="${NGINX_BIN:-nginx}"
ACME_AUTO_RENEW="${ACME_AUTO_RENEW:-1}"
ACME_RENEW_INTERVAL="${ACME_RENEW_INTERVAL:-86400}"
ACME_RENEW_FOREGROUND="${ACME_RENEW_FOREGROUND:-0}"
ACME_STANDALONE_STOP_NGINX="${ACME_STANDALONE_STOP_NGINX:-1}"
ACME_COMMON_ARGS="--home $ACME_HOME --config-home $ACME_HOME --cert-home $ACME_HOME"

entrypoint_log() {
    if [ -z "${NGINX_ENTRYPOINT_QUIET_LOGS:-}" ]; then
        echo "[ACME] $@"
    fi
}

is_true() {
    case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
        1|true|yes|on) return 0 ;;
        *) return 1 ;;
    esac
}

normalize_deploy_mode() {
    mode_raw=$(printf '%s' "$PROXY_DEPLOY_MODE" | tr '[:upper:]' '[:lower:]' | tr '-' '_')
    case "$mode_raw" in
        front_proxy|front|upstream|proxy) printf 'front_proxy' ;;
        direct|direct_tls|self_managed|host) printf 'direct' ;;
        *) printf 'front_proxy' ;;
    esac
}

port_is_listening() {
    port="$1"

    if command -v ss >/dev/null 2>&1; then
        if ss -H -ltn 2>/dev/null | awk -v target_port="$port" '
            {
                local_addr = $4
                gsub(/^\[|\]$/, "", local_addr)
                split(local_addr, parts, ":")
                if (parts[length(parts)] == target_port) {
                    found = 1
                }
            }
            END { exit(found ? 0 : 1) }
        '; then
            return 0
        fi
        return 1
    fi

    if command -v lsof >/dev/null 2>&1; then
        if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
            return 0
        fi
        return 1
    fi

    if command -v netstat >/dev/null 2>&1; then
        if netstat -ltn 2>/dev/null | awk -v target_port="$port" '
            NR > 2 {
                local_addr = $4
                gsub(/^\[|\]$/, "", local_addr)
                split(local_addr, parts, ":")
                if (parts[length(parts)] == target_port) {
                    found = 1
                }
            }
            END { exit(found ? 0 : 1) }
        '; then
            return 0
        fi
        return 1
    fi

    return 1
}

nginx_pid_is_running() {
    if [ -s /var/run/nginx.pid ]; then
        nginx_pid=$(cat /var/run/nginx.pid 2>/dev/null || true)
        [ -n "$nginx_pid" ] && kill -0 "$nginx_pid" 2>/dev/null
        return $?
    fi
    return 1
}

wait_for_port_release() {
    port="$1"
    retries="${2:-50}"
    while [ "$retries" -gt 0 ]; do
        if ! port_is_listening "$port"; then
            return 0
        fi
        sleep 0.2
        retries=$((retries - 1))
    done
    return 1
}

stop_nginx_for_standalone_acme() {
    cert_domain="$1"
    ACME_STOPPED_NGINX="0"
    if ! port_is_listening 80; then
        return 0
    fi

    if is_true "$ACME_STANDALONE_STOP_NGINX" && nginx_pid_is_running; then
        entrypoint_log "Temporarily stopping nginx to renew standalone certificate for $cert_domain"
        "$NGINX_BIN" -s quit >/dev/null 2>&1 || "$NGINX_BIN" -s stop >/dev/null 2>&1 || true
        if ! wait_for_port_release 80; then
            entrypoint_log "Failed to stop nginx before renewing $cert_domain"
            return 1
        fi
        ACME_STOPPED_NGINX="1"
        return 0
    fi

    entrypoint_log "Port 80 is busy, cannot renew standalone certificate for $cert_domain"
    return 1
}

restore_nginx_after_standalone_acme() {
    if [ "${ACME_STOPPED_NGINX:-0}" = "1" ]; then
        entrypoint_log "Starting nginx after standalone certificate renewal"
        "$NGINX_BIN" >/dev/null 2>&1 || true
        ACME_STOPPED_NGINX="0"
    fi
}

list_local_http01_managed_domains() {
    [ -f "$MANAGED_CERTS_POLICY_JSON" ] || return 0
    MANAGED_CERTS_POLICY_JSON="$MANAGED_CERTS_POLICY_JSON" node -e "
        const fs = require('fs');
        const file = process.env.MANAGED_CERTS_POLICY_JSON;
        let items = [];
        try {
            items = JSON.parse(fs.readFileSync(file, 'utf8'));
        } catch {
            process.exit(0);
        }
        const domains = [...new Set((Array.isArray(items) ? items : [])
            .filter((item) => item && item.enabled !== false && String(item.issuer_mode || '').trim().toLowerCase() === 'local_http01')
            .map((item) => String(item.domain || '').trim())
            .filter(Boolean))];
        process.stdout.write(domains.join('\n'));
    "
}

acme_cert_is_issued() {
    cert_domain="$1"
    "$ACME_SCRIPT" --info -d "$cert_domain" --ecc $ACME_COMMON_ARGS 2>/dev/null | grep -q "RealFullChainPath"
}

install_cert_files() {
    cert_domain="$1"
    cert_target_dir="$DIRECT_CERT_DIR/$cert_domain"
    mkdir -p "$cert_target_dir"
    "$ACME_SCRIPT" --install-cert -d "$cert_domain" --ecc $ACME_COMMON_ARGS \
        --fullchain-file "$cert_target_dir/cert" \
        --key-file "$cert_target_dir/key" \
        --reloadcmd "sh -c '$NGINX_BIN -e /proc/1/fd/2 -t >/dev/null 2>&1 && { [ -s /var/run/nginx.pid ] && $NGINX_BIN -e /proc/1/fd/2 -s reload || true; }; true'"
}

renew_managed_local_http01_certs() {
    domains="$(list_local_http01_managed_domains || true)"
    [ -n "$domains" ] || {
        return 0
    }

    printf '%s\n' "$domains" | while IFS= read -r cert_domain || [ -n "$cert_domain" ]; do
        [ -n "$cert_domain" ] || continue
        if ! acme_cert_is_issued "$cert_domain"; then
            entrypoint_log "Skipping renew for $cert_domain: no local acme.sh record found"
            continue
        fi
        entrypoint_log "Renewing unified local_http01 certificate for $cert_domain"
        if ! stop_nginx_for_standalone_acme "$cert_domain"; then
            continue
        fi
        if "$ACME_SCRIPT" --renew -d "$cert_domain" --ecc $ACME_COMMON_ARGS; then
            if ! install_cert_files "$cert_domain"; then
                restore_nginx_after_standalone_acme
                entrypoint_log "acme.sh install-cert failed for $cert_domain"
                continue
            fi
            restore_nginx_after_standalone_acme
        else
            restore_nginx_after_standalone_acme
            entrypoint_log "acme.sh renew failed for $cert_domain"
        fi
    done
}

run_once() {
    if [ ! -x "$ACME_SCRIPT" ]; then
        return 0
    fi

    if [ ! -f "$MANAGED_CERTS_POLICY_JSON" ]; then
        entrypoint_log "Managed certificate policy file not found, skipping renew loop"
        return 0
    fi

    renew_managed_local_http01_certs
}

case "$ACME_RENEW_INTERVAL" in
    ''|*[!0-9]*)
        entrypoint_log "Invalid ACME_RENEW_INTERVAL '$ACME_RENEW_INTERVAL', defaulting to 86400"
        ACME_RENEW_INTERVAL=86400
        ;;
esac

if [ "$ACME_RENEW_INTERVAL" -lt 1 ]; then
    entrypoint_log "ACME_RENEW_INTERVAL must be >= 1, defaulting to 86400"
    ACME_RENEW_INTERVAL=86400
fi

if [ "$(normalize_deploy_mode)" != "direct" ]; then
    exit 0
fi

if [ "$(printf '%s' "$DIRECT_CERT_MODE" | tr '[:upper:]' '[:lower:]')" != "acme" ]; then
    exit 0
fi

if ! is_true "$ACME_AUTO_RENEW"; then
    entrypoint_log "ACME auto renew disabled"
    exit 0
fi

renew_loop() {
    while true; do
        sleep "$ACME_RENEW_INTERVAL"
        run_once
    done
}

if is_true "$ACME_RENEW_FOREGROUND"; then
    entrypoint_log "Starting ACME renew loop in foreground with interval ${ACME_RENEW_INTERVAL}s"
    renew_loop
fi

renew_loop &
entrypoint_log "Started ACME renew loop with interval ${ACME_RENEW_INTERVAL}s"
