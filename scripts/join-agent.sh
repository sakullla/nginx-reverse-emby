#!/bin/sh
set -eu

usage() {
    cat <<'EOF'
Usage: join-agent.sh --master-url URL --register-token TOKEN --apply-command CMD [options]

Required:
  --master-url URL         Master panel URL, e.g. http://master.example.com:8080
  --register-token TOKEN   Master registration token
  --apply-command CMD      Local apply command; will receive RULES_JSON env

Optional:
  --agent-name NAME        Agent node name, default: current hostname
  --agent-token TOKEN      Agent heartbeat token, default: auto-generated
  --agent-url URL          Optional public URL for direct access / display
  --data-dir DIR           State/config directory, default: ./agent-data
  --rules-file FILE        Rules JSON path, default: <data-dir>/proxy_rules.json
  --state-file FILE        Agent state file, default: <data-dir>/agent-state.json
  --interval-ms N          Heartbeat interval in ms, default: 10000
  --version VERSION        Agent version, default: 1
  --tags TAGS              Comma-separated tags, e.g. edge,emby
  --install-systemd        Install a systemd service for the lightweight agent
  -h, --help               Show help
EOF
}

trim_slash() {
    printf '%s' "$1" | sed 's#/*$##'
}

shell_quote() {
    printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"
}

generate_token() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 24
        return 0
    fi
    if command -v python >/dev/null 2>&1; then
        python - <<'PY'
import secrets
print(secrets.token_hex(24))
PY
        return 0
    fi
    date +%s | sha256sum | cut -d' ' -f1 | cut -c1-48
}

MASTER_URL=""
REGISTER_TOKEN=""
AGENT_NAME="${HOSTNAME:-$(hostname)}"
AGENT_TOKEN=""
AGENT_URL=""
DATA_DIR="./agent-data"
RULES_FILE=""
STATE_FILE=""
INTERVAL_MS="10000"
AGENT_VERSION="1"
AGENT_TAGS=""
APPLY_COMMAND=""
INSTALL_SYSTEMD="0"
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

while [ $# -gt 0 ]; do
    case "$1" in
        --master-url) MASTER_URL="$2"; shift 2 ;;
        --register-token) REGISTER_TOKEN="$2"; shift 2 ;;
        --agent-name) AGENT_NAME="$2"; shift 2 ;;
        --agent-token) AGENT_TOKEN="$2"; shift 2 ;;
        --agent-url) AGENT_URL="$2"; shift 2 ;;
        --data-dir) DATA_DIR="$2"; shift 2 ;;
        --rules-file) RULES_FILE="$2"; shift 2 ;;
        --state-file) STATE_FILE="$2"; shift 2 ;;
        --interval-ms) INTERVAL_MS="$2"; shift 2 ;;
        --version) AGENT_VERSION="$2"; shift 2 ;;
        --tags) AGENT_TAGS="$2"; shift 2 ;;
        --apply-command) APPLY_COMMAND="$2"; shift 2 ;;
        --install-systemd) INSTALL_SYSTEMD="1"; shift 1 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "Unknown argument: $1" >&2; usage >&2; exit 1 ;;
    esac
done

[ -n "$MASTER_URL" ] || { echo "Missing --master-url" >&2; exit 1; }
[ -n "$REGISTER_TOKEN" ] || { echo "Missing --register-token" >&2; exit 1; }
[ -n "$APPLY_COMMAND" ] || { echo "Missing --apply-command" >&2; exit 1; }

command -v node >/dev/null 2>&1 || { echo "node is required" >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }

MASTER_URL="$(trim_slash "$MASTER_URL")"
AGENT_URL="$(trim_slash "$AGENT_URL")"
AGENT_TOKEN="${AGENT_TOKEN:-$(generate_token)}"
mkdir -p "$DATA_DIR"

RULES_FILE="${RULES_FILE:-$DATA_DIR/proxy_rules.json}"
STATE_FILE="${STATE_FILE:-$DATA_DIR/agent-state.json}"
ENV_FILE="$DATA_DIR/agent.env"

cat > "$ENV_FILE" <<EOF
MASTER_PANEL_URL=$(shell_quote "$MASTER_URL")
MASTER_REGISTER_TOKEN=$(shell_quote "$REGISTER_TOKEN")
AGENT_NAME=$(shell_quote "$AGENT_NAME")
AGENT_TOKEN=$(shell_quote "$AGENT_TOKEN")
AGENT_PUBLIC_URL=$(shell_quote "$AGENT_URL")
AGENT_VERSION=$(shell_quote "$AGENT_VERSION")
AGENT_TAGS=$(shell_quote "$AGENT_TAGS")
AGENT_HEARTBEAT_INTERVAL_MS=$(shell_quote "$INTERVAL_MS")
RULES_JSON=$(shell_quote "$RULES_FILE")
AGENT_STATE_FILE=$(shell_quote "$STATE_FILE")
APPLY_COMMAND=$(shell_quote "$APPLY_COMMAND")
EOF

PAYLOAD=$(node -e "const payload = {name: process.argv[1], agent_url: process.argv[2], agent_token: process.argv[3], version: process.argv[4], tags: process.argv[5] ? process.argv[5].split(',').map(v => v.trim()).filter(Boolean) : [], mode: 'pull', register_token: process.argv[6]}; process.stdout.write(JSON.stringify(payload));" "$AGENT_NAME" "$AGENT_URL" "$AGENT_TOKEN" "$AGENT_VERSION" "$AGENT_TAGS" "$REGISTER_TOKEN")

echo "[JOIN] Writing agent env: $ENV_FILE"
echo "[JOIN] Registering lightweight agent to: $MASTER_URL/panel-api/agents/register"

REGISTER_RESPONSE=$(curl -fsS \
  -H "Content-Type: application/json" \
  -H "X-Register-Token: $REGISTER_TOKEN" \
  -H "X-Agent-Token: $AGENT_TOKEN" \
  -d "$PAYLOAD" \
  "$MASTER_URL/panel-api/agents/register")

echo "[JOIN] Registered successfully: $REGISTER_RESPONSE"
echo "[JOIN] Rules file: $RULES_FILE"
echo "[JOIN] State file: $STATE_FILE"

if [ "$INSTALL_SYSTEMD" = "1" ]; then
    SERVICE_FILE="/etc/systemd/system/nginx-reverse-emby-agent.service"
    cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Nginx Reverse Emby Lightweight Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$ENV_FILE
ExecStart=$(command -v node) $SCRIPT_DIR/light-agent.js
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable --now nginx-reverse-emby-agent.service
    echo "[JOIN] Installed and started systemd service: nginx-reverse-emby-agent.service"
else
    echo "[JOIN] Start command:"
    echo "  set -a && . $ENV_FILE && set +a && node $SCRIPT_DIR/light-agent.js"
fi
