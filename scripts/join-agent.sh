#!/bin/sh
set -eu

DEFAULT_MASTER_URL="__DEFAULT_MASTER_URL__"
DEFAULT_ASSET_BASE_URL="__DEFAULT_ASSET_BASE_URL__"
UNSET_MASTER_SENTINEL="__JOIN_AGENT_DEFAULT_MASTER_URL__"
UNSET_ASSET_BASE_URL_SENTINEL="__JOIN_AGENT_DEFAULT_ASSET_BASE_URL__"

[ "$DEFAULT_MASTER_URL" = "$UNSET_MASTER_SENTINEL" ] && DEFAULT_MASTER_URL=""
[ "$DEFAULT_ASSET_BASE_URL" = "$UNSET_ASSET_BASE_URL_SENTINEL" ] && DEFAULT_ASSET_BASE_URL=""
case "$DEFAULT_MASTER_URL" in __DEFAULT_*__) DEFAULT_MASTER_URL="" ;; esac
case "$DEFAULT_ASSET_BASE_URL" in __DEFAULT_*__) DEFAULT_ASSET_BASE_URL="" ;; esac

usage() {
    cat <<EOF
Usage: join-agent.sh --register-token TOKEN [options]

Required:
  --register-token TOKEN   Master registration token

Optional:
  --master-url URL         Master control-plane URL (default: embedded panel URL)
  --asset-base-url URL     Control-plane asset base URL (default: embedded asset URL)
  --agent-name NAME        Agent name, default: current hostname
  --agent-token TOKEN      Agent heartbeat token, default: auto-generated
  --agent-url URL          Optional public URL for display / direct access
  --data-dir DIR           Install directory, default: ./agent-data
  --version VERSION        Agent version sent during registration, default: 1
  --tags TAGS              Comma-separated tags, e.g. edge,emby
  --binary-url URL         Download URL override for the nre-agent binary
  --install-systemd        Install and start a systemd service (Linux)
  --install-launchd        Install and load a launchd agent (macOS)
  -h, --help               Show help

Examples:
  curl -fsSL ${DEFAULT_MASTER_URL:-http://master.example.com:3000}/panel-api/public/join-agent.sh | sh -s -- --register-token change-this-register-token --install-systemd
  join-agent.sh --master-url http://master.example.com:3000 --register-token change-this-register-token --install-systemd
  join-agent.sh --register-token change-this-register-token --install-launchd
EOF
}

trim_slash() {
    printf '%s' "$1" | sed 's#/*$##'
}

normalize_master_url() {
    value="$(trim_slash "$1")"
    value="$(printf '%s' "$value" | sed 's#/panel-api/public/join-agent\.sh$##')"
    value="$(printf '%s' "$value" | sed 's#/panel-api$##')"
    printf '%s' "$value"
}

is_valid_master_url() {
    printf '%s' "$1" | grep -Eq '^https?://[^/]+$'
}

shell_quote() {
    printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\''/g")"
}

json_string() {
    escaped=$(printf '%s' "$1" | sed ':a;N;$!ba;s/\\/\\\\/g;s/"/\\"/g;s/\r/\\r/g;s/\n/\\n/g;s/\t/\\t/g')
    printf '"%s"' "$escaped"
}

extract_registered_agent_id() {
    printf '%s' "$1" | tr -d '\r\n' | sed -n 's/.*"agent"[[:space:]]*:[[:space:]]*{[^}]*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

generate_token() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 24
        return 0
    fi
    if command -v python3 >/dev/null 2>&1; then
        python3 - <<'PY'
import secrets
print(secrets.token_hex(24))
PY
        return 0
    fi
    date +%s | cksum | awk '{print $1 $2}' | cut -c1-48
}

absolute_path() {
    target="$1"
    case "$target" in
        /*) printf '%s\n' "$target" ;;
        *)
            target_dir=$(dirname -- "$target")
            target_name=$(basename -- "$target")
            mkdir -p "$target_dir"
            target_dir_abs=$(CDPATH= cd -- "$target_dir" && pwd)
            printf '%s/%s\n' "$target_dir_abs" "$target_name"
            ;;
    esac
}

detect_platform() {
    uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]'
}

detect_arch() {
    arch=$(uname -m 2>/dev/null || printf 'unknown')
    case "$arch" in
        x86_64|amd64) printf 'amd64' ;;
        arm64|aarch64) printf 'arm64' ;;
        *) printf '%s' "$arch" ;;
    esac
}

xml_escape() {
    printf '%s' "$1" | sed \
        -e 's/&/\&amp;/g' \
        -e 's/</\&lt;/g' \
        -e 's/>/\&gt;/g' \
        -e "s/'/\&apos;/g" \
        -e 's/"/\&quot;/g'
}

require_root_or_sudo() {
    if [ "$(id -u)" -eq 0 ]; then
        printf '%s\n' ""
        return 0
    fi
    if command -v sudo >/dev/null 2>&1; then
        printf '%s\n' "sudo"
        return 0
    fi
    return 1
}

run_root_cmd() {
    if [ -n "${SUDO_BIN:-}" ]; then
        "$SUDO_BIN" "$@"
    else
        "$@"
    fi
}

resolve_script_dir() {
    script_path=${0:-}
    [ -n "$script_path" ] || return 1
    case "$script_path" in
        /*) dir=$(dirname -- "$script_path") ;;
        */*) dir=$(dirname -- "$script_path") ;;
        *) dir="." ;;
    esac
    CDPATH= cd -- "$dir" 2>/dev/null && pwd
}

copy_or_download_binary() {
    asset_name="$1"
    dest_path="$2"
    local_path=""

    if [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/../panel/public/agent-assets/$asset_name" ]; then
        local_path="$SCRIPT_DIR/../panel/public/agent-assets/$asset_name"
    elif [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/$asset_name" ]; then
        local_path="$SCRIPT_DIR/$asset_name"
    fi

    mkdir -p "$(dirname -- "$dest_path")"

    if [ -n "$local_path" ] && [ -f "$local_path" ]; then
        cp "$local_path" "$dest_path"
        chmod 755 "$dest_path"
        return 0
    fi

    if [ -n "$BINARY_URL" ]; then
        echo "[JOIN] Downloading nre-agent from $BINARY_URL ..." >&2
        curl -fsSL --connect-timeout 15 --max-time 300 "$BINARY_URL" -o "$dest_path"
        chmod 755 "$dest_path"
        return 0
    fi

    [ -n "$ASSET_BASE_URL" ] || {
        echo "Missing nre-agent binary source. Re-run with --asset-base-url URL or --binary-url URL." >&2
        exit 1
    }

    echo "[JOIN] Downloading $asset_name from $ASSET_BASE_URL ..." >&2
    curl -fsSL --connect-timeout 15 --max-time 300 "$ASSET_BASE_URL/$asset_name" -o "$dest_path"
    chmod 755 "$dest_path"
}

build_tags_json() {
    if [ -z "$1" ]; then
        printf '[]'
        return 0
    fi

    old_ifs=$IFS
    IFS=,
    set -- $1
    IFS=$old_ifs

    first=1
    printf '['
    for tag in "$@"; do
        trimmed=$(printf '%s' "$tag" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
        [ -n "$trimmed" ] || continue
        if [ "$first" -eq 0 ]; then
            printf ','
        fi
        json_string "$trimmed"
        first=0
    done
    printf ']'
}

build_register_payload() {
    tags_json=$(build_tags_json "$AGENT_TAGS")
    printf '{'
    printf '"name":%s,' "$(json_string "$AGENT_NAME")"
    printf '"agent_url":%s,' "$(json_string "$AGENT_URL")"
    printf '"agent_token":%s,' "$(json_string "$AGENT_TOKEN")"
    printf '"version":%s,' "$(json_string "$AGENT_VERSION")"
    printf '"platform":%s,' "$(json_string "$PLATFORM-$ARCH")"
    printf '"tags":%s,' "$tags_json"
    printf '"capabilities":["http_rules","l4","cert_install"],'
    printf '"mode":"pull",'
    printf '"register_token":%s' "$(json_string "$REGISTER_TOKEN")"
    printf '}'
}

MASTER_URL="$DEFAULT_MASTER_URL"
ASSET_BASE_URL="$DEFAULT_ASSET_BASE_URL"
REGISTER_TOKEN=""
AGENT_NAME="${HOSTNAME:-$(hostname)}"
AGENT_TOKEN=""
AGENT_URL=""
DATA_DIR="./agent-data"
AGENT_VERSION="1"
AGENT_TAGS=""
INSTALL_SYSTEMD="0"
INSTALL_LAUNCHD="0"
BINARY_URL=""
SCRIPT_DIR="$(resolve_script_dir 2>/dev/null || true)"
PLATFORM="$(detect_platform)"
ARCH="$(detect_arch)"

while [ $# -gt 0 ]; do
    case "$1" in
        --master-url) MASTER_URL="$2"; shift 2 ;;
        --asset-base-url) ASSET_BASE_URL="$2"; shift 2 ;;
        --register-token) REGISTER_TOKEN="$2"; shift 2 ;;
        --agent-name) AGENT_NAME="$2"; shift 2 ;;
        --agent-token) AGENT_TOKEN="$2"; shift 2 ;;
        --agent-url) AGENT_URL="$2"; shift 2 ;;
        --data-dir) DATA_DIR="$2"; shift 2 ;;
        --version) AGENT_VERSION="$2"; shift 2 ;;
        --tags) AGENT_TAGS="$2"; shift 2 ;;
        --binary-url) BINARY_URL="$2"; shift 2 ;;
        --install-systemd) INSTALL_SYSTEMD="1"; shift 1 ;;
        --install-launchd) INSTALL_LAUNCHD="1"; shift 1 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "Unknown argument: $1" >&2; usage >&2; exit 1 ;;
    esac
done

[ -n "$REGISTER_TOKEN" ] || { echo "Missing --register-token" >&2; exit 1; }
[ -n "$MASTER_URL" ] || {
    echo "Missing --master-url and no embedded control-plane URL is available" >&2
    exit 1
}
MASTER_URL="$(normalize_master_url "$MASTER_URL")"
if ! is_valid_master_url "$MASTER_URL"; then
    echo "Invalid --master-url: $MASTER_URL" >&2
    echo "Expected format: http://host:port or https://host" >&2
    exit 1
fi
[ "$INSTALL_SYSTEMD$INSTALL_LAUNCHD" != "11" ] || {
    echo "Use either --install-systemd or --install-launchd, not both" >&2
    exit 1
}

case "$PLATFORM" in
    linux|darwin) ;;
    *) echo "Unsupported platform for join-agent.sh: $PLATFORM" >&2; exit 1 ;;
esac
case "$ARCH" in
    amd64|arm64) ;;
    *) echo "Unsupported architecture for join-agent.sh: $ARCH" >&2; exit 1 ;;
esac

command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }

ASSET_BASE_URL="$(trim_slash "$ASSET_BASE_URL")"
AGENT_URL="$(trim_slash "$AGENT_URL")"
AGENT_TOKEN="${AGENT_TOKEN:-$(generate_token)}"
DATA_DIR="$(absolute_path "$DATA_DIR")"
BIN_DIR="$DATA_DIR/bin"
ENV_FILE="$DATA_DIR/agent.env"
BIN_PATH="$BIN_DIR/nre-agent"
ASSET_NAME="nre-agent-$PLATFORM-$ARCH"

mkdir -p "$BIN_DIR"
echo "[JOIN] Installing nre-agent to: $BIN_PATH"
copy_or_download_binary "$ASSET_NAME" "$BIN_PATH"

cat > "$ENV_FILE" <<EOF
NRE_MASTER_URL=$(shell_quote "$MASTER_URL")
NRE_AGENT_NAME=$(shell_quote "$AGENT_NAME")
NRE_AGENT_TOKEN=$(shell_quote "$AGENT_TOKEN")
NRE_AGENT_URL=$(shell_quote "$AGENT_URL")
NRE_AGENT_VERSION=$(shell_quote "$AGENT_VERSION")
NRE_AGENT_TAGS=$(shell_quote "$AGENT_TAGS")
EOF

PAYLOAD=$(build_register_payload)

echo "[JOIN] Registering Go agent to: $MASTER_URL/panel-api/agents/register"
REGISTER_RESPONSE=$(curl -fsS \
  -H "Content-Type: application/json" \
  -H "X-Register-Token: $REGISTER_TOKEN" \
  -H "X-Agent-Token: $AGENT_TOKEN" \
  -d "$PAYLOAD" \
  "$MASTER_URL/panel-api/agents/register")
REGISTERED_AGENT_ID="$(extract_registered_agent_id "$REGISTER_RESPONSE")"
[ -n "$REGISTERED_AGENT_ID" ] || {
    echo "Registered agent id missing from register response" >&2
    exit 1
}
printf 'NRE_AGENT_ID=%s\n' "$(shell_quote "$REGISTERED_AGENT_ID")" >> "$ENV_FILE"

echo "[JOIN] Registered successfully: $REGISTER_RESPONSE"
echo "[JOIN] Agent binary: $BIN_PATH"
echo "[JOIN] Agent env: $ENV_FILE"

if [ "$INSTALL_SYSTEMD" = "1" ]; then
    [ "$PLATFORM" = "linux" ] || { echo "--install-systemd is only supported on Linux" >&2; exit 1; }
    SUDO_BIN="$(require_root_or_sudo)" || {
        echo "Installing systemd services requires root or sudo" >&2
        exit 1
    }
    command -v systemctl >/dev/null 2>&1 || { echo "systemctl is required for --install-systemd" >&2; exit 1; }
    SERVICE_FILE="/etc/systemd/system/nginx-reverse-emby-agent.service"
    cat <<EOF | run_root_cmd tee "$SERVICE_FILE" >/dev/null
[Unit]
Description=Nginx Reverse Emby Go Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$ENV_FILE
WorkingDirectory=$DATA_DIR
ExecStart=$BIN_PATH
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
    run_root_cmd systemctl daemon-reload
    run_root_cmd systemctl enable --now nginx-reverse-emby-agent.service
    echo "[JOIN] Installed and started systemd service: nginx-reverse-emby-agent.service"
elif [ "$INSTALL_LAUNCHD" = "1" ]; then
    [ "$PLATFORM" = "darwin" ] || { echo "--install-launchd is only supported on macOS" >&2; exit 1; }
    command -v launchctl >/dev/null 2>&1 || { echo "launchctl is required for --install-launchd" >&2; exit 1; }
    LAUNCHD_DIR="$HOME/Library/LaunchAgents"
    SERVICE_LABEL="com.nginx-reverse-emby.agent"
    SERVICE_FILE="$LAUNCHD_DIR/$SERVICE_LABEL.plist"
    START_COMMAND="set -a && . $(shell_quote "$ENV_FILE") && set +a && exec $(shell_quote "$BIN_PATH")"
    mkdir -p "$LAUNCHD_DIR"
    cat > "$SERVICE_FILE" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$(xml_escape "$SERVICE_LABEL")</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-lc</string>
    <string>$(xml_escape "$START_COMMAND")</string>
  </array>
  <key>WorkingDirectory</key>
  <string>$(xml_escape "$DATA_DIR")</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>$(xml_escape "$DATA_DIR/agent.stdout.log")</string>
  <key>StandardErrorPath</key>
  <string>$(xml_escape "$DATA_DIR/agent.stderr.log")</string>
</dict>
</plist>
EOF
    launchctl unload "$SERVICE_FILE" >/dev/null 2>&1 || true
    launchctl load -w "$SERVICE_FILE"
    echo "[JOIN] Installed and loaded launchd agent: $SERVICE_LABEL"
else
    echo "[JOIN] Start command:"
    echo "  set -a && . $ENV_FILE && set +a && $BIN_PATH"
fi
