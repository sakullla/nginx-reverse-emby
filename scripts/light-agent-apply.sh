#!/bin/sh
set -eu

[ -n "${RULES_JSON:-}" ] || { echo "RULES_JSON is required" >&2; exit 1; }
[ -f "$RULES_JSON" ] || { echo "Rules file not found: $RULES_JSON" >&2; exit 1; }

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
AGENT_HOME="${AGENT_HOME:-$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)}"
L4_RULES_JSON="${L4_RULES_JSON:-$AGENT_HOME/l4_rules.json}"
MANAGED_CERTS_JSON="${MANAGED_CERTS_JSON:-$AGENT_HOME/managed_certificates.json}"
MANAGED_CERTS_POLICY_JSON="${MANAGED_CERTS_POLICY_JSON:-$AGENT_HOME/managed_certificates.policy.json}"
RUNTIME_DIR="${AGENT_RUNTIME_DIR:-$AGENT_HOME/runtime}"
GENERATOR_SCRIPT="${AGENT_GENERATOR_SCRIPT:-$RUNTIME_DIR/25-dynamic-reverse-proxy.sh}"
NGINX_BIN_PATH="${AGENT_NGINX_BIN:-${NGINX_BIN:-nginx}}"

NRE_DYNAMIC_DIR="${NRE_DYNAMIC_DIR:-/etc/nginx/conf.d/dynamic}"
NRE_STREAM_DYNAMIC_DIR="${NRE_STREAM_DYNAMIC_DIR:-/etc/nginx/stream-conf.d/dynamic}"
NRE_INCLUDE_FILE="${NRE_INCLUDE_FILE:-/etc/nginx/conf.d/zz-nginx-reverse-emby-agent.include.conf}"
NRE_GLOBALS_FILE="${NRE_GLOBALS_FILE:-/etc/nginx/conf.d/zz-nginx-reverse-emby-agent.globals.conf}"
NRE_STATUS_CONF_FILE="${NRE_STATUS_CONF_FILE:-/etc/nginx/conf.d/zz-nginx-reverse-emby-agent.status.conf}"
NRE_STATUS_PORT="${NRE_STATUS_PORT:-18080}"
NRE_TEMPLATE_FILE="${NRE_TEMPLATE_FILE:-$RUNTIME_DIR/default.conf.template}"
NRE_DIRECT_NO_TLS_TEMPLATE_FILE="${NRE_DIRECT_NO_TLS_TEMPLATE_FILE:-$RUNTIME_DIR/default.direct.no_tls.conf.template}"
NRE_DIRECT_TLS_TEMPLATE_FILE="${NRE_DIRECT_TLS_TEMPLATE_FILE:-$RUNTIME_DIR/default.direct.tls.conf.template}"

is_true() {
    case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
        1|true|yes|on) return 0 ;;
        *) return 1 ;;
    esac
}

normalize_mode() {
    mode_raw=$(printf '%s' "${1:-direct}" | tr '[:upper:]' '[:lower:]' | tr '-' '_')
    case "$mode_raw" in
        front_proxy|front|upstream|proxy) printf 'front_proxy' ;;
        direct|direct_tls|self_managed|host) printf 'direct' ;;
        *) printf 'direct' ;;
    esac
}

normalize_deploy_mode() {
    if is_true "${AGENT_FOLLOW_MASTER_DEPLOY_MODE:-${AGENT_LOCAL_NODE:-0}}"; then
        normalize_mode "${PROXY_DEPLOY_MODE:-direct}"
        return 0
    fi

    printf 'direct'
}

expected_listen_ports() {
    node -e '
const fs = require("fs")
const deployMode = String(process.env.PROXY_DEPLOY_MODE || "direct")
  .trim()
  .toLowerCase()
  .replace(/-/g, "_") === "front_proxy"
  ? "front_proxy"
  : "direct"
const frontProxyPort = String(process.env.FRONT_PROXY_PORT || "3000").trim() || "3000"

let rules = []
try {
  rules = JSON.parse(fs.readFileSync(process.argv[1], "utf8"))
} catch {
  process.exit(0)
}

const ports = []
for (const rule of Array.isArray(rules) ? rules : []) {
  if (!rule || rule.enabled === false) continue
  const frontendUrl = String(rule.frontend_url || "").trim()
  if (!frontendUrl) continue

  try {
    const url = new URL(frontendUrl)
    let port = url.port || (url.protocol === "https:" ? "443" : "80")
    if (deployMode === "front_proxy") {
      port = frontProxyPort
    }
    if (port) ports.push(String(port))
  } catch {}
}

process.stdout.write([...new Set(ports)].join("\n"))
' "$RULES_JSON"
}

expected_l4_listen_ports() {
    node -e '
const fs = require("fs")

let rules = []
try {
  rules = JSON.parse(fs.readFileSync(process.argv[1], "utf8"))
} catch {
  process.exit(0)
}

const ports = []
for (const rule of Array.isArray(rules) ? rules : []) {
  if (!rule || rule.enabled === false) continue
  const port = Number(rule.listen_port)
  if (Number.isFinite(port) && port > 0) {
    ports.push(String(port))
  }
}

process.stdout.write([...new Set(ports)].join("\n"))
' "$L4_RULES_JSON"
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

    if [ -r /proc/net/tcp ] || [ -r /proc/net/tcp6 ]; then
        port_hex=$(printf '%04X' "$port")
        for proc_file in /proc/net/tcp /proc/net/tcp6; do
            [ -r "$proc_file" ] || continue
            if awk -v port_hex="$port_hex" '
                $4 == "0A" {
                    split($2, local_addr, ":")
                    if (local_addr[2] == port_hex) {
                        found = 1
                    }
                }
                END { exit(found ? 0 : 1) }
            ' "$proc_file"; then
                return 0
            fi
        done
        return 1
    fi

    return 2
}

[ -f "$GENERATOR_SCRIPT" ] || { echo "Generator script not found: $GENERATOR_SCRIPT" >&2; exit 1; }
[ -f "$NRE_TEMPLATE_FILE" ] || { echo "Template not found: $NRE_TEMPLATE_FILE" >&2; exit 1; }
[ -f "$NRE_DIRECT_NO_TLS_TEMPLATE_FILE" ] || { echo "Template not found: $NRE_DIRECT_NO_TLS_TEMPLATE_FILE" >&2; exit 1; }
[ -f "$NRE_DIRECT_TLS_TEMPLATE_FILE" ] || { echo "Template not found: $NRE_DIRECT_TLS_TEMPLATE_FILE" >&2; exit 1; }

mkdir -p "$NRE_DYNAMIC_DIR" "$NRE_STREAM_DYNAMIC_DIR" "$(dirname "$NRE_INCLUDE_FILE")" "$(dirname "$NRE_GLOBALS_FILE")" "$(dirname "$NRE_STATUS_CONF_FILE")" "$AGENT_HOME/.state" "$AGENT_HOME/certs"

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

if is_true "${NRE_ENABLE_NGINX_STATUS:-1}"; then
    cat > "$NRE_STATUS_CONF_FILE" <<EOF
server {
    listen 127.0.0.1:${NRE_STATUS_PORT};
    listen [::1]:${NRE_STATUS_PORT};

    location = /nginx_status {
        stub_status on;
        access_log off;
        allow 127.0.0.1;
        allow ::1;
        deny all;
    }
}
EOF
else
    rm -f "$NRE_STATUS_CONF_FILE"
fi

resolved_deploy_mode=$(normalize_deploy_mode)

export PANEL_RULES_JSON="$RULES_JSON"
export PANEL_L4_RULES_JSON="$L4_RULES_JSON"
export PANEL_MANAGED_CERTS_SYNC_JSON="$MANAGED_CERTS_JSON"
export PANEL_MANAGED_CERTS_POLICY_JSON="$MANAGED_CERTS_POLICY_JSON"
export PROXY_DEPLOY_MODE="$resolved_deploy_mode"
export NRE_TEMPLATE_FILE
export NRE_DIRECT_NO_TLS_TEMPLATE_FILE
export NRE_DIRECT_TLS_TEMPLATE_FILE
export NRE_DYNAMIC_DIR
export DIRECT_CERT_DIR="${DIRECT_CERT_DIR:-$AGENT_HOME/certs}"
export DIRECT_CERT_STATE_FILE="${DIRECT_CERT_STATE_FILE:-$AGENT_HOME/.state/active_cert_domains}"
export ACME_HOME="${ACME_HOME:-$AGENT_HOME/.acme.sh}"
export NGINX_BIN="$NGINX_BIN_PATH"

sh "$GENERATOR_SCRIPT"
expected_http_ports=$(expected_listen_ports || true)
expected_l4_ports=$(expected_l4_listen_ports || true)
echo "[AGENT] Apply mode: $resolved_deploy_mode"
if [ -n "$expected_http_ports" ]; then
    echo "[AGENT] Expected HTTP listen ports:"
    printf '%s\n' "$expected_http_ports"
fi
if [ -n "$expected_l4_ports" ]; then
    echo "[AGENT] Expected L4 listen ports:"
    printf '%s\n' "$expected_l4_ports"
fi
if [ -z "$expected_http_ports" ] && [ -z "$expected_l4_ports" ]; then
    echo "[AGENT] No enabled rules found; skipping port validation"
fi
"$NGINX_BIN_PATH" -t
"$NGINX_BIN_PATH" -s reload

tmp_effective_config=$(mktemp)
if ! "$NGINX_BIN_PATH" -T >"$tmp_effective_config" 2>&1; then
    cat "$tmp_effective_config" >&2
    rm -f "$tmp_effective_config"
    exit 1
fi

if ! grep -F "$NRE_DYNAMIC_DIR" "$tmp_effective_config" >/dev/null 2>&1 && \
   ! grep -F "$NRE_INCLUDE_FILE" "$tmp_effective_config" >/dev/null 2>&1; then
    echo "Generated configs are not part of the active nginx config: $NRE_DYNAMIC_DIR/*.conf" >&2
    echo "Check whether nginx.conf includes /etc/nginx/conf.d/*.conf, or point NRE_INCLUDE_FILE to an already included path." >&2
    rm -f "$tmp_effective_config"
    exit 1
fi

if [ -n "$expected_l4_ports" ]; then
    if ! grep -F "$NRE_STREAM_DYNAMIC_DIR" "$tmp_effective_config" >/dev/null 2>&1; then
        echo "Generated L4 configs are not part of the active nginx config: $NRE_STREAM_DYNAMIC_DIR/*.conf" >&2
        echo "Check whether nginx.conf includes /etc/nginx/stream-conf.d/*.conf and /etc/nginx/stream-conf.d/dynamic/*.conf." >&2
        rm -f "$tmp_effective_config"
        exit 1
    fi
fi

combined_ports=$(printf '%s\n%s\n' "$expected_http_ports" "$expected_l4_ports" | awk 'NF && !seen[$0]++')
if [ -n "$combined_ports" ]; then
    missing_ports=""
    port_check_skipped="0"
    for port in $combined_ports; do
        if port_is_listening "$port"; then
            continue
        fi

        port_check_status=$?
        if [ "$port_check_status" -eq 2 ]; then
            port_check_skipped="1"
            continue
        fi

        missing_ports="${missing_ports} ${port}"
    done

    if [ -n "$missing_ports" ]; then
        echo "nginx reload succeeded, but expected listen ports are not active:${missing_ports}" >&2
        echo "Check whether nginx loaded $NRE_INCLUDE_FILE and whether another service is occupying the target port." >&2
        rm -f "$tmp_effective_config"
        exit 1
    fi

    if [ "$port_check_skipped" = "1" ]; then
        echo "[AGENT] Listen port probe skipped (ss/lsof/netstat unavailable); active config include was verified"
    else
        echo "[AGENT] Listen port probe passed"
    fi
fi

rm -f "$tmp_effective_config"
