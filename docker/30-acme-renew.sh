#!/bin/sh
set -eu

DATA_ROOT="/opt/nginx-reverse-emby/panel/data"
ACME_HOME="${ACME_HOME:-$DATA_ROOT/.acme.sh}"
ACME_SCRIPT="$ACME_HOME/acme.sh"
PROXY_DEPLOY_MODE="${PROXY_DEPLOY_MODE:-front_proxy}"
DIRECT_CERT_MODE="${DIRECT_CERT_MODE:-acme}"
ACME_AUTO_RENEW="${ACME_AUTO_RENEW:-1}"
ACME_RENEW_INTERVAL="${ACME_RENEW_INTERVAL:-86400}"
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

(
    while true; do
        sleep "$ACME_RENEW_INTERVAL"

        if [ ! -x "$ACME_SCRIPT" ]; then
            continue
        fi

        entrypoint_log "Running scheduled acme.sh --cron"
        if ! "$ACME_SCRIPT" --cron $ACME_COMMON_ARGS; then
            entrypoint_log "acme.sh --cron failed"
        fi
    done
) &

entrypoint_log "Started ACME renew loop with interval ${ACME_RENEW_INTERVAL}s"
