#!/bin/sh
set -eu

[ -n "${RULES_JSON:-}" ] || { echo "RULES_JSON is required" >&2; exit 1; }
[ -f "$RULES_JSON" ] || { echo "Rules file not found: $RULES_JSON" >&2; exit 1; }

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
AGENT_HOME="${AGENT_HOME:-$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)}"
RUNTIME_DIR="${AGENT_RUNTIME_DIR:-$AGENT_HOME/runtime}"
GENERATOR_SCRIPT="${AGENT_GENERATOR_SCRIPT:-$RUNTIME_DIR/25-dynamic-reverse-proxy.sh}"
NGINX_BIN_PATH="${AGENT_NGINX_BIN:-${NGINX_BIN:-nginx}}"

NRE_DYNAMIC_DIR="${NRE_DYNAMIC_DIR:-/etc/nginx/conf.d/dynamic}"
NRE_INCLUDE_FILE="${NRE_INCLUDE_FILE:-/etc/nginx/conf.d/zz-nginx-reverse-emby-agent.include.conf}"
NRE_GLOBALS_FILE="${NRE_GLOBALS_FILE:-/etc/nginx/conf.d/zz-nginx-reverse-emby-agent.globals.conf}"
NRE_TEMPLATE_FILE="${NRE_TEMPLATE_FILE:-$RUNTIME_DIR/default.conf.template}"
NRE_DIRECT_NO_TLS_TEMPLATE_FILE="${NRE_DIRECT_NO_TLS_TEMPLATE_FILE:-$RUNTIME_DIR/default.direct.no_tls.conf.template}"
NRE_DIRECT_TLS_TEMPLATE_FILE="${NRE_DIRECT_TLS_TEMPLATE_FILE:-$RUNTIME_DIR/default.direct.tls.conf.template}"

[ -f "$GENERATOR_SCRIPT" ] || { echo "Generator script not found: $GENERATOR_SCRIPT" >&2; exit 1; }
[ -f "$NRE_TEMPLATE_FILE" ] || { echo "Template not found: $NRE_TEMPLATE_FILE" >&2; exit 1; }
[ -f "$NRE_DIRECT_NO_TLS_TEMPLATE_FILE" ] || { echo "Template not found: $NRE_DIRECT_NO_TLS_TEMPLATE_FILE" >&2; exit 1; }
[ -f "$NRE_DIRECT_TLS_TEMPLATE_FILE" ] || { echo "Template not found: $NRE_DIRECT_TLS_TEMPLATE_FILE" >&2; exit 1; }

mkdir -p "$NRE_DYNAMIC_DIR" "$(dirname "$NRE_INCLUDE_FILE")" "$(dirname "$NRE_GLOBALS_FILE")" "$AGENT_HOME/.state" "$AGENT_HOME/certs"

cat > "$NRE_INCLUDE_FILE" <<EOF
include $NRE_DYNAMIC_DIR/*.conf;
EOF

cat > "$NRE_GLOBALS_FILE" <<'EOF'
map $http_upgrade $connection_upgrade {
    default upgrade;
    ""      close;
}

map $http_sec_fetch_mode $early_hints {
    navigate $http2$http3;
}
EOF

export PANEL_RULES_JSON="$RULES_JSON"
export PROXY_DEPLOY_MODE="${PROXY_DEPLOY_MODE:-direct}"
export NRE_TEMPLATE_FILE
export NRE_DIRECT_NO_TLS_TEMPLATE_FILE
export NRE_DIRECT_TLS_TEMPLATE_FILE
export NRE_DYNAMIC_DIR
export DIRECT_CERT_DIR="${DIRECT_CERT_DIR:-$AGENT_HOME/certs}"
export DIRECT_CERT_STATE_FILE="${DIRECT_CERT_STATE_FILE:-$AGENT_HOME/.state/active_cert_domains}"
export ACME_HOME="${ACME_HOME:-$AGENT_HOME/.acme.sh}"
export NGINX_BIN="$NGINX_BIN_PATH"

sh "$GENERATOR_SCRIPT"
"$NGINX_BIN_PATH" -t
"$NGINX_BIN_PATH" -s reload
