#!/bin/sh
set -eu

entrypoint_log() {
    if [ -z "${NGINX_ENTRYPOINT_QUIET_LOGS:-}" ]; then
        echo "$@"
    fi
}

is_true() {
    case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
        1|true|yes|on) return 0 ;;
        *) return 1 ;;
    esac
}

is_nginx_running() {
    if command -v pgrep >/dev/null 2>&1 && pgrep -x nginx >/dev/null 2>&1; then
        return 0
    fi
    if command -v pidof >/dev/null 2>&1 && pidof nginx >/dev/null 2>&1; then
        return 0
    fi
    return 1
}

PROXY_DEPLOY_MODE="${PROXY_DEPLOY_MODE:-front_proxy}"
DIRECT_CERT_MODE="${DIRECT_CERT_MODE:-acme}"
ACME_AUTO_RENEW="${ACME_AUTO_RENEW:-1}"
ACME_RENEW_INTERVAL="${ACME_RENEW_INTERVAL:-86400}"
ACME_HOME="${ACME_HOME:-/opt/acme.sh}"
ACME_SCRIPT="${ACME_SCRIPT:-$ACME_HOME/acme.sh}"
ACME_DNS_PROVIDER="${ACME_DNS_PROVIDER:-}"
ACME_STANDALONE_STOP_NGINX="${ACME_STANDALONE_STOP_NGINX:-1}"
NGINX_BIN="${NGINX_BIN:-nginx}"

if [ "$(printf '%s' "$PROXY_DEPLOY_MODE" | tr '[:upper:]' '[:lower:]' | tr '-' '_')" != "direct" ]; then
    exit 0
fi

if [ "$(printf '%s' "$DIRECT_CERT_MODE" | tr '[:upper:]' '[:lower:]')" != "acme" ]; then
    exit 0
fi

if ! is_true "$ACME_AUTO_RENEW"; then
    exit 0
fi

case "$ACME_RENEW_INTERVAL" in
    ''|*[!0-9]*)
        entrypoint_log "$0: warning: invalid ACME_RENEW_INTERVAL='$ACME_RENEW_INTERVAL', fallback to 86400"
        ACME_RENEW_INTERVAL=86400
        ;;
esac

if [ "$ACME_RENEW_INTERVAL" -lt 60 ]; then
    ACME_RENEW_INTERVAL=60
fi

if [ ! -x "$ACME_SCRIPT" ]; then
    entrypoint_log "$0: info: skip auto renew loop because '$ACME_SCRIPT' does not exist yet"
    exit 0
fi

run_acme_cron() {
    nginx_was_running=0
    if [ -z "$ACME_DNS_PROVIDER" ] && is_true "$ACME_STANDALONE_STOP_NGINX" && is_nginx_running; then
        nginx_was_running=1
        entrypoint_log "$0: stopping nginx before acme cron (standalone mode)"
        "$NGINX_BIN" -s stop >/dev/null 2>&1 || true
    fi

    if ! "$ACME_SCRIPT" --cron --home "$ACME_HOME" --config-home "$ACME_HOME" --cert-home "$ACME_HOME"; then
        entrypoint_log "$0: warning: acme cron execution failed"
    fi

    if [ "$nginx_was_running" -eq 1 ]; then
        entrypoint_log "$0: starting nginx after acme cron"
        "$NGINX_BIN" >/dev/null 2>&1 || true
    fi
}

(
    entrypoint_log "$0: started acme auto-renew loop (interval=${ACME_RENEW_INTERVAL}s)"
    run_acme_cron
    while true; do
        sleep "$ACME_RENEW_INTERVAL"
        run_acme_cron
    done
) &

exit 0
