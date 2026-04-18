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
Usage:
  join-agent.sh --register-token TOKEN [options]
  join-agent.sh migrate-from-main --register-token TOKEN [options]
  join-agent.sh uninstall-agent [options]

Commands:
  migrate-from-main       Migrate a legacy lightweight Agent node to go-agent
  uninstall-agent         Remove the local Agent runtime from this host

Required:
  --register-token TOKEN   Master registration token

Optional:
  --master-url URL         Master control-plane URL (default: embedded panel URL)
  --asset-base-url URL     Control-plane asset base URL (default: embedded asset URL)
  --agent-name NAME        Agent name, default: current hostname
  --agent-token TOKEN      Agent heartbeat token, default: auto-generated
  --agent-url URL          Optional public URL for display / direct access
  --data-dir DIR           Install directory, default: /var/lib/nre-agent
  --version VERSION        Agent version sent during registration, default: 1
  --tags TAGS              Comma-separated tags, e.g. edge,emby
  --binary-url URL         Download URL override for the nre-agent binary
  --install-systemd        Install and start a systemd service (Linux)
  --install-launchd        Install and load a launchd agent (macOS)
  --source-dir DIR         Legacy lightweight Agent directory for migrate-from-main or uninstall-agent
  -h, --help               Show help

Examples:
  curl -fsSL ${DEFAULT_MASTER_URL:-http://master.example.com:3000}/panel-api/public/join-agent.sh | sh -s -- --register-token change-this-register-token --install-systemd
  join-agent.sh --master-url http://master.example.com:3000 --register-token change-this-register-token --install-systemd
  join-agent.sh --register-token change-this-register-token --install-launchd
  join-agent.sh migrate-from-main --master-url http://master.example.com:3000 --register-token change-this-register-token
  join-agent.sh uninstall-agent --data-dir /var/lib/nre-agent
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
    echo "[ERROR] Cannot generate a cryptographically strong agent token: neither openssl nor python3 is available." >&2
    echo "Install openssl or python3, or provide --agent-token explicitly." >&2
    return 1
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

persist_installed_join_script() {
    mkdir -p "$BIN_DIR"
    [ -n "$MASTER_URL" ] || {
        echo "Missing --master-url; cannot persist installed join-agent.sh" >&2
        exit 1
    }
    curl -fsSL --connect-timeout 15 --max-time 300 "$MASTER_URL/panel-api/public/join-agent.sh" -o "$JOIN_SCRIPT_PATH"
    chmod 755 "$JOIN_SCRIPT_PATH"
}

install_uninstall_wrapper() {
    if [ -z "${SUDO_BIN:-}" ] && [ "$(id -u)" -ne 0 ] && [ ! -w "$(dirname -- "$UNINSTALL_WRAPPER_PATH")" ]; then
        SUDO_BIN="$(require_root_or_sudo)" || {
            echo "Installing uninstall wrapper requires root or sudo" >&2
            exit 1
        }
    fi

    uninstall_source_arg=""
    if [ -n "${WRAPPER_SOURCE_DIR:-}" ]; then
        uninstall_source_arg=" --source-dir $(shell_quote "$SOURCE_DIR")"
    fi

    cat <<EOF | run_root_cmd tee "$UNINSTALL_WRAPPER_PATH" >/dev/null
#!/bin/sh
set -eu
exec $(shell_quote "$JOIN_SCRIPT_PATH") uninstall-agent --data-dir $(shell_quote "$DATA_DIR")$uninstall_source_arg
EOF
    run_root_cmd chmod 755 "$UNINSTALL_WRAPPER_PATH"
    echo "[JOIN] Installed uninstall command: $UNINSTALL_WRAPPER_PATH"
}

remove_uninstall_wrapper() {
    if [ ! -e "$UNINSTALL_WRAPPER_PATH" ]; then
        return 0
    fi
    if [ -z "${SUDO_BIN:-}" ] && [ "$(id -u)" -ne 0 ] && [ ! -w "$UNINSTALL_WRAPPER_PATH" ] && [ ! -w "$(dirname -- "$UNINSTALL_WRAPPER_PATH")" ]; then
        SUDO_BIN="$(require_root_or_sudo)" || {
            echo "Removing uninstall wrapper requires root or sudo" >&2
            exit 1
        }
    fi
    run_root_cmd rm -f "$UNINSTALL_WRAPPER_PATH"
}

service_exists() {
    systemctl status nginx-reverse-emby-agent.service >/dev/null 2>&1
}

service_is_active() {
    systemctl is-active --quiet nginx-reverse-emby-agent.service
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

build_capabilities_json() {
    if [ -z "$1" ]; then
        printf '["http_rules","l4","cert_install"]'
        return 0
    fi

    old_ifs=$IFS
    IFS=,
    set -- $1
    IFS=$old_ifs

    first=1
    printf '['
    for capability in "$@"; do
        trimmed=$(printf '%s' "$capability" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
        [ -n "$trimmed" ] || continue
        if [ "$first" -eq 0 ]; then
            printf ','
        fi
        json_string "$trimmed"
        first=0
    done
    if [ "$first" -eq 1 ]; then
        printf '"http_rules","l4","cert_install"'
    fi
    printf ']'
}

build_register_payload() {
    tags_json=$(build_tags_json "$AGENT_TAGS")
    capabilities_json=$(build_capabilities_json "$AGENT_CAPABILITIES")
    printf '{'
    printf '"name":%s,' "$(json_string "$AGENT_NAME")"
    printf '"agent_url":%s,' "$(json_string "$AGENT_URL")"
    printf '"agent_token":%s,' "$(json_string "$AGENT_TOKEN")"
    printf '"version":%s,' "$(json_string "$AGENT_VERSION")"
    printf '"platform":%s,' "$(json_string "$PLATFORM-$ARCH")"
    printf '"tags":%s,' "$tags_json"
    printf '"capabilities":%s,' "$capabilities_json"
    printf '"mode":"pull",'
    printf '"register_token":%s' "$(json_string "$REGISTER_TOKEN")"
    printf '}'
}

write_agent_env() {
    env_file="$1"
    cat > "$env_file" <<EOF
NRE_MASTER_URL=$(shell_quote "$MASTER_URL")
NRE_AGENT_NAME=$(shell_quote "$AGENT_NAME")
NRE_AGENT_TOKEN=$(shell_quote "$AGENT_TOKEN")
NRE_AGENT_URL=$(shell_quote "$AGENT_URL")
NRE_AGENT_VERSION=$(shell_quote "$AGENT_VERSION")
NRE_AGENT_TAGS=$(shell_quote "$AGENT_TAGS")
NRE_AGENT_CAPABILITIES=$(shell_quote "$AGENT_CAPABILITIES")
NRE_DATA_DIR=$(shell_quote "$DATA_DIR")
EOF
}

register_agent() {
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
}

install_systemd_service() {
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

    SERVICE_EXISTS="0"
    SERVICE_WAS_ACTIVE="0"
    if service_exists; then
        SERVICE_EXISTS="1"
        if service_is_active; then
            SERVICE_WAS_ACTIVE="1"
        fi
    fi
    if [ "$SERVICE_WAS_ACTIVE" = "1" ]; then
        run_root_cmd systemctl stop nginx-reverse-emby-agent.service
    fi

    run_root_cmd mv "$BIN_TMP_PATH" "$BIN_PATH"
    if [ "$SERVICE_EXISTS" = "1" ]; then
        run_root_cmd systemctl enable nginx-reverse-emby-agent.service
        run_root_cmd systemctl start nginx-reverse-emby-agent.service
    else
        run_root_cmd systemctl enable --now nginx-reverse-emby-agent.service
    fi
    install_uninstall_wrapper
    echo "[JOIN] Installed and started systemd service: nginx-reverse-emby-agent.service"
}

install_launchd_service() {
    [ "$PLATFORM" = "darwin" ] || { echo "--install-launchd is only supported on macOS" >&2; exit 1; }
    command -v launchctl >/dev/null 2>&1 || { echo "launchctl is required for --install-launchd" >&2; exit 1; }
    LAUNCHD_DIR="$HOME/Library/LaunchAgents"
    SERVICE_LABEL="com.nginx-reverse-emby.agent"
    SERVICE_FILE="$LAUNCHD_DIR/$SERVICE_LABEL.plist"
    START_COMMAND="set -a && . $(shell_quote "$ENV_FILE") && set +a && exec $(shell_quote "$BIN_PATH")"
    mv "$BIN_TMP_PATH" "$BIN_PATH"
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
    install_uninstall_wrapper
    echo "[JOIN] Installed and loaded launchd agent: $SERVICE_LABEL"
}

install_manual_runtime() {
    mv "$BIN_TMP_PATH" "$BIN_PATH"
    echo "[JOIN] Start command:"
    echo "  set -a && . $ENV_FILE && set +a && $BIN_PATH"
}

migrate_data_dir_contents() {
    old_data_dir="$1"
    new_data_dir="$2"

    [ -d "$old_data_dir" ] || return 0
    mkdir -p "$new_data_dir"
    cp -Rp "$old_data_dir"/. "$new_data_dir"/
    rm -rf "$old_data_dir"
}

load_existing_agent_env_if_present() {
    env_file="$1"
    [ -f "$env_file" ] || return 0

    NRE_MASTER_URL=""
    NRE_AGENT_NAME=""
    NRE_AGENT_TOKEN=""
    NRE_AGENT_URL=""
    NRE_AGENT_VERSION=""
    NRE_AGENT_TAGS=""
    NRE_AGENT_CAPABILITIES=""
    NRE_AGENT_ID=""

    set -a
    . "$env_file"
    set +a

    MASTER_URL="${MASTER_URL:-$NRE_MASTER_URL}"
    AGENT_NAME="${AGENT_NAME:-$NRE_AGENT_NAME}"
    AGENT_TOKEN="${AGENT_TOKEN:-$NRE_AGENT_TOKEN}"
    AGENT_URL="${AGENT_URL:-$NRE_AGENT_URL}"
    AGENT_VERSION="${AGENT_VERSION:-$NRE_AGENT_VERSION}"
    AGENT_TAGS="${AGENT_TAGS:-$NRE_AGENT_TAGS}"
    AGENT_CAPABILITIES="${AGENT_CAPABILITIES:-$NRE_AGENT_CAPABILITIES}"
}

systemd_unit_exists() {
    unit_name="$1"
    command -v systemctl >/dev/null 2>&1 || return 1
    systemctl list-unit-files "$unit_name" --no-legend 2>/dev/null | grep -q "^$unit_name[[:space:]]"
}

disable_systemd_unit_if_present() {
    unit_name="$1"
    if systemd_unit_exists "$unit_name"; then
        run_root_cmd systemctl disable --now "$unit_name"
    fi
}

backup_legacy_unit() {
    unit_name="$1"
    unit_path="/etc/systemd/system/$unit_name"
    backup_path="$DATA_DIR/$unit_name.bak"
    if [ -f "$unit_path" ]; then
        cp "$unit_path" "$backup_path"
    fi
}

restore_legacy_units() {
    restored=0
    for bak in "$DATA_DIR"/*.bak; do
        [ -f "$bak" ] || continue
        unit_name="$(basename "$bak" .bak)"
        unit_path="/etc/systemd/system/$unit_name"
        run_root_cmd cp "$bak" "$unit_path"
        run_root_cmd systemctl daemon-reload
        run_root_cmd systemctl enable --now "$unit_name"
        rm -f "$bak"
        restored=1
    done
    if [ "$restored" -eq 1 ]; then
        echo "[MIGRATE] Restored legacy agent services from backup"
    fi
}

verify_systemd_service_active() {
    attempts=0
    while [ "$attempts" -lt 10 ]; do
        if run_root_cmd systemctl is-active --quiet nginx-reverse-emby-agent.service; then
            return 0
        fi
        attempts=$((attempts + 1))
        sleep 1
    done
    return 1
}

verify_master_connectivity() {
    attempts=0
    while [ "$attempts" -lt 6 ]; do
        if curl -fsS -o /dev/null "$MASTER_URL/panel-api/health" 2>/dev/null; then
            return 0
        fi
        attempts=$((attempts + 1))
        sleep 5
    done
    return 1
}

verify_agent_heartbeat() {
    agent_heartbeat_interval="${NRE_HEARTBEAT_INTERVAL:-10}"
    wait_seconds=$((agent_heartbeat_interval * 2))
    if [ "$wait_seconds" -lt 15 ]; then
        wait_seconds=15
    fi
    echo "[MIGRATE] Waiting ${wait_seconds}s for agent to complete first heartbeat cycle"
    sleep "$wait_seconds"

    service_start="$(run_root_cmd systemctl show -p ActiveEnterTimestamp nginx-reverse-emby-agent.service 2>/dev/null | cut -d= -f2- || true)"
    if [ -n "$service_start" ]; then
        error_lines="$(run_root_cmd journalctl -u nginx-reverse-emby-agent.service --since "$service_start" --no-pager 2>/dev/null | grep -c 'sync error\|heartbeat failed\|runtime apply error' || true)"
        if [ "$error_lines" -gt 0 ]; then
            echo "[MIGRATE] Agent logged $error_lines heartbeat/sync error(s) since startup, aborting migration" >&2
            return 1
        fi
    fi

    echo "[MIGRATE] Probing heartbeat endpoint with agent credentials to confirm registration"
    heartbeat_resp="$(curl -fsS -o /dev/null -w '%{http_code}' \
        -H "X-Agent-Token: $AGENT_TOKEN" \
        -H "Content-Type: application/json" \
        -d '{"version":"1"}' \
        "$MASTER_URL/panel-api/agents/heartbeat" 2>/dev/null || true)"
    if [ "$heartbeat_resp" != "200" ]; then
        echo "[MIGRATE] Heartbeat probe returned HTTP $heartbeat_resp (expected 200), agent may be unauthorized or misconfigured" >&2
        return 1
    fi

    return 0
}

list_legacy_cert_domains() {
    tmp_domains=$(mktemp)
    if [ -d "$OLD_DIRECT_CERT_DIR" ]; then
        for cert_dir in "$OLD_DIRECT_CERT_DIR"/*; do
            [ -d "$cert_dir" ] || continue
            basename -- "$cert_dir" >> "$tmp_domains"
        done
    fi
    if [ -f "$OLD_MANAGED_CERTS_JSON" ]; then
        grep -o '"domain"[[:space:]]*:[[:space:]]*"[^"]*"' "$OLD_MANAGED_CERTS_JSON" 2>/dev/null | \
            sed 's/.*"domain"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/' >> "$tmp_domains" || true
    fi
    if [ -s "$tmp_domains" ]; then
        sort -u "$tmp_domains"
    fi
    rm -f "$tmp_domains"
}

cleanup_legacy_acme() {
    [ -x "$OLD_ACME_HOME/acme.sh" ] || return 0

    tmp_domains=$(mktemp)
    list_legacy_cert_domains > "$tmp_domains"
    [ -s "$tmp_domains" ] || {
        rm -f "$tmp_domains"
        return 0
    }

    while IFS= read -r cert_domain; do
        [ -n "$cert_domain" ] || continue
        echo "[MIGRATE] Removing legacy acme record: $cert_domain"
        acme_domain="$(normalize_legacy_acme_domain "$cert_domain")"
        if "$OLD_ACME_HOME/acme.sh" --home "$OLD_ACME_HOME" --config-home "$OLD_ACME_HOME" --cert-home "$OLD_ACME_HOME" --remove -d "$acme_domain" --ecc >/dev/null 2>&1; then
            continue
        fi
        if "$OLD_ACME_HOME/acme.sh" --home "$OLD_ACME_HOME" --config-home "$OLD_ACME_HOME" --cert-home "$OLD_ACME_HOME" --remove -d "$acme_domain" >/dev/null 2>&1; then
            continue
        fi
        rm -f "$tmp_domains"
        echo "failed to remove legacy acme record: $cert_domain" >&2
        exit 1
    done < "$tmp_domains"
    rm -f "$tmp_domains"
}

normalize_legacy_acme_domain() {
    case "$1" in
        \*.*) printf '%s\n' "${1#*.}" ;;
        *) printf '%s\n' "$1" ;;
    esac
}

cleanup_legacy_nginx_runtime() {
    if ! legacy_nginx_runtime_present; then
        return 0
    fi
    if command -v systemctl >/dev/null 2>&1; then
        disable_systemd_unit_if_present nginx.service >/dev/null 2>&1 || true
    fi
    run_root_cmd rm -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.include.conf
    run_root_cmd rm -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.globals.conf
    run_root_cmd rm -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.status.conf
    run_root_cmd rm -rf /etc/nginx/conf.d/dynamic
    run_root_cmd rm -rf /etc/nginx/stream-conf.d/dynamic
}

legacy_nginx_runtime_present() {
    [ -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.include.conf ] && return 0
    [ -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.globals.conf ] && return 0
    [ -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.status.conf ] && return 0
    [ -d /etc/nginx/conf.d/dynamic ] && return 0
    [ -d /etc/nginx/stream-conf.d/dynamic ] && return 0
    return 1
}

cleanup_legacy_runtime() {
    cleanup_legacy_acme

    disable_systemd_unit_if_present nginx-reverse-emby-agent-renew.service
    if [ -f /etc/systemd/system/nginx-reverse-emby-agent-renew.service ]; then
        run_root_cmd rm -f /etc/systemd/system/nginx-reverse-emby-agent-renew.service
        run_root_cmd systemctl daemon-reload
    fi
    cleanup_legacy_nginx_runtime
    run_root_cmd rm -rf "$OLD_SOURCE_DIR"
    rm -f "$DATA_DIR"/*.bak
}

cleanup_local_agent_runtime() {
    if [ "$PLATFORM" = "linux" ]; then
        SUDO_BIN="$(require_root_or_sudo)" || {
            echo "Uninstalling systemd services requires root or sudo" >&2
            exit 1
        }
        disable_systemd_unit_if_present nginx-reverse-emby-agent.service
        disable_systemd_unit_if_present nginx-reverse-emby-agent-renew.service
        run_root_cmd rm -f /etc/systemd/system/nginx-reverse-emby-agent.service
        run_root_cmd rm -f /etc/systemd/system/nginx-reverse-emby-agent-renew.service
        if command -v systemctl >/dev/null 2>&1; then
            run_root_cmd systemctl daemon-reload
        fi
        run_root_cmd rm -f "$UNINSTALL_WRAPPER_PATH"
        run_root_cmd rm -rf "$DATA_DIR"
        if [ -n "${SOURCE_DIR:-}" ]; then
            SOURCE_DIR="$(absolute_path "$SOURCE_DIR")"
            run_root_cmd rm -rf "$SOURCE_DIR"
        fi
    elif [ "$PLATFORM" = "darwin" ]; then
        SERVICE_FILE="$HOME/Library/LaunchAgents/com.nginx-reverse-emby.agent.plist"
        if [ -f "$SERVICE_FILE" ]; then
            launchctl unload "$SERVICE_FILE" >/dev/null 2>&1 || true
            rm -f "$SERVICE_FILE"
        fi
        remove_uninstall_wrapper
        rm -rf "$DATA_DIR"
        if [ -n "${SOURCE_DIR:-}" ]; then
            SOURCE_DIR="$(absolute_path "$SOURCE_DIR")"
            rm -rf "$SOURCE_DIR"
        fi
    else
        remove_uninstall_wrapper
        rm -rf "$DATA_DIR"
        if [ -n "${SOURCE_DIR:-}" ]; then
            SOURCE_DIR="$(absolute_path "$SOURCE_DIR")"
            rm -rf "$SOURCE_DIR"
        fi
    fi
    cleanup_legacy_nginx_runtime
}

load_legacy_runtime() {
    OLD_SOURCE_DIR="$(absolute_path "$SOURCE_DIR")"
    OLD_ENV_FILE="$OLD_SOURCE_DIR/agent.env"
    OLD_MANAGED_CERTS_JSON="$OLD_SOURCE_DIR/managed_certificates.json"

    [ -f "$OLD_ENV_FILE" ] || {
        echo "legacy agent env not found: $OLD_ENV_FILE" >&2
        exit 1
    }

    set -a
    . "$OLD_ENV_FILE"
    set +a

    OLD_DIRECT_CERT_DIR="${DIRECT_CERT_DIR:-$OLD_SOURCE_DIR/certs}"
    OLD_ACME_HOME="${ACME_HOME:-$OLD_SOURCE_DIR/.acme.sh}"

    MASTER_URL="${MASTER_URL:-${MASTER_PANEL_URL:-}}"
    AGENT_NAME="${AGENT_NAME:-${NRE_AGENT_NAME:-${AGENT_NAME:-}}}"
    AGENT_TOKEN="${AGENT_TOKEN:-${NRE_AGENT_TOKEN:-${AGENT_TOKEN:-}}}"
    AGENT_URL="${AGENT_URL:-${NRE_AGENT_URL:-${AGENT_PUBLIC_URL:-}}}"
    AGENT_VERSION="${AGENT_VERSION:-${NRE_AGENT_VERSION:-${AGENT_VERSION:-}}}"
    AGENT_TAGS="${AGENT_TAGS:-${NRE_AGENT_TAGS:-${AGENT_TAGS:-}}}"
    AGENT_CAPABILITIES="${AGENT_CAPABILITIES:-${AGENT_CAPABILITIES:-http_rules,local_acme,cert_install,l4}}"
}

run_join() {
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

    if [ "$INSTALL_SYSTEMD" = "1" ] && [ "$USER_DATA_DIR_DEFAULT" = "1" ]; then
        OLD_DATA_DIR="$(absolute_path "./agent-data")"
    elif [ "$USER_DATA_DIR_DEFAULT" = "1" ]; then
        DATA_DIR="$HOME/.nre-agent"
    fi

    DATA_DIR="$(absolute_path "$DATA_DIR")"
    if [ "${OLD_DATA_DIR:-}" ] && [ ! -f "$DATA_DIR/agent.env" ] && [ -f "$OLD_DATA_DIR/agent.env" ]; then
        echo "[JOIN] Migrating agent data from $OLD_DATA_DIR to $DATA_DIR"
        migrate_data_dir_contents "$OLD_DATA_DIR" "$DATA_DIR"
    fi

    BIN_DIR="$DATA_DIR/bin"
    ENV_FILE="$DATA_DIR/agent.env"
    BIN_PATH="$BIN_DIR/nre-agent"
    BIN_TMP_PATH="$BIN_PATH.tmp.$$"
    JOIN_SCRIPT_PATH="$BIN_DIR/join-agent.sh"
    UNINSTALL_WRAPPER_PATH="/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh"
    ASSET_NAME="nre-agent-$PLATFORM-$ARCH"

    load_existing_agent_env_if_present "$ENV_FILE"

    AGENT_NAME="${AGENT_NAME:-${HOSTNAME:-$(hostname)}}"
    AGENT_VERSION="${AGENT_VERSION:-1}"

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

    ASSET_BASE_URL="$(trim_slash "$ASSET_BASE_URL")"
    AGENT_URL="$(trim_slash "$AGENT_URL")"
    AGENT_TOKEN="${AGENT_TOKEN:-$(generate_token)}"

    mkdir -p "$BIN_DIR"
    echo "[JOIN] Installing nre-agent to: $BIN_PATH"
    rm -f "$BIN_TMP_PATH"
    copy_or_download_binary "$ASSET_NAME" "$BIN_TMP_PATH"
    persist_installed_join_script
    write_agent_env "$ENV_FILE"
    register_agent

    echo "[JOIN] Agent binary: $BIN_PATH"
    echo "[JOIN] Agent env: $ENV_FILE"

    if [ "$INSTALL_SYSTEMD" = "1" ]; then
        install_systemd_service
    elif [ "$INSTALL_LAUNCHD" = "1" ]; then
        install_launchd_service
    else
        install_manual_runtime
    fi
}

run_migrate_from_main() {
    [ "$PLATFORM" = "linux" ] || { echo "migrate-from-main is only supported on Linux" >&2; exit 1; }
    INSTALL_LAUNCHD="0"
    if [ "$INSTALL_SYSTEMD" != "1" ]; then
        INSTALL_SYSTEMD="1"
    fi

    load_legacy_runtime

    AGENT_NAME="${AGENT_NAME:-${HOSTNAME:-$(hostname)}}"
    AGENT_VERSION="${AGENT_VERSION:-1}"

    [ -n "$REGISTER_TOKEN" ] || { echo "Missing --register-token" >&2; exit 1; }
    [ -n "$MASTER_URL" ] || {
        echo "Missing --master-url and legacy agent env does not provide MASTER_PANEL_URL" >&2
        exit 1
    }
    [ -n "$AGENT_TOKEN" ] || {
        echo "legacy agent token missing" >&2
        exit 1
    }

    MASTER_URL="$(normalize_master_url "$MASTER_URL")"
    if ! is_valid_master_url "$MASTER_URL"; then
        echo "Invalid --master-url: $MASTER_URL" >&2
        echo "Expected format: http://host:port or https://host" >&2
        exit 1
    fi

    command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }
    command -v systemctl >/dev/null 2>&1 || { echo "systemctl is required for migrate-from-main" >&2; exit 1; }

    DATA_DIR="$(absolute_path "$DATA_DIR")"
    if [ "$DATA_DIR" = "$OLD_SOURCE_DIR" ]; then
        echo "--data-dir must not be the same as --source-dir during migration" >&2
        exit 1
    fi

    ASSET_BASE_URL="$(trim_slash "$ASSET_BASE_URL")"
    AGENT_URL="$(trim_slash "$AGENT_URL")"
    BIN_DIR="$DATA_DIR/bin"
    ENV_FILE="$DATA_DIR/agent.env"
    BIN_PATH="$BIN_DIR/nre-agent"
    BIN_TMP_PATH="$BIN_PATH.tmp.$$"
    JOIN_SCRIPT_PATH="$BIN_DIR/join-agent.sh"
    UNINSTALL_WRAPPER_PATH="/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh"
    WRAPPER_SOURCE_DIR="$SOURCE_DIR"
    ASSET_NAME="nre-agent-$PLATFORM-$ARCH"

    mkdir -p "$BIN_DIR"
    echo "[MIGRATE] Preparing go-agent install: $BIN_PATH"
    rm -f "$BIN_TMP_PATH"
    copy_or_download_binary "$ASSET_NAME" "$BIN_TMP_PATH"
    persist_installed_join_script
    write_agent_env "$ENV_FILE"

    SUDO_BIN="$(require_root_or_sudo)" || {
        echo "Migrating systemd services requires root or sudo" >&2
        exit 1
    }

    register_agent

    echo "[MIGRATE] Backing up legacy unit files"
    backup_legacy_unit nginx-reverse-emby-agent.service
    backup_legacy_unit nginx-reverse-emby-agent-renew.service

    echo "[MIGRATE] Stopping legacy lightweight Agent services"
    disable_systemd_unit_if_present nginx-reverse-emby-agent.service
    disable_systemd_unit_if_present nginx-reverse-emby-agent-renew.service

    install_systemd_service

    if ! verify_systemd_service_active; then
        echo "[MIGRATE] new go-agent service failed to become active, restoring legacy services" >&2
        disable_systemd_unit_if_present nginx-reverse-emby-agent.service
        restore_legacy_units
        exit 1
    fi

    if ! verify_master_connectivity; then
        echo "[MIGRATE] master is not reachable at $MASTER_URL, restoring legacy services" >&2
        disable_systemd_unit_if_present nginx-reverse-emby-agent.service
        restore_legacy_units
        exit 1
    fi

    if ! verify_agent_heartbeat; then
        echo "[MIGRATE] agent heartbeat verification failed, restoring legacy services" >&2
        disable_systemd_unit_if_present nginx-reverse-emby-agent.service
        restore_legacy_units
        exit 1
    fi

    echo "[MIGRATE] New go-agent service is active, heartbeat verified, cleaning legacy runtime"
    cleanup_legacy_runtime
}

run_uninstall_agent() {
    if [ "$USER_DATA_DIR_DEFAULT" = "1" ]; then
        if [ -d "/var/lib/nre-agent" ]; then
            DATA_DIR="/var/lib/nre-agent"
        elif [ -n "${HOME:-}" ] && [ -d "$HOME/.nre-agent" ]; then
            DATA_DIR="$HOME/.nre-agent"
        fi
    fi
    DATA_DIR="$(absolute_path "$DATA_DIR")"
    cleanup_local_agent_runtime
    echo "[UNINSTALL] Local agent runtime removed. Delete the agent record from the control panel if it is no longer needed."
}

COMMAND="join"
MASTER_URL="$DEFAULT_MASTER_URL"
ASSET_BASE_URL="$DEFAULT_ASSET_BASE_URL"
REGISTER_TOKEN=""
AGENT_NAME=""
AGENT_TOKEN=""
AGENT_URL=""
DATA_DIR="/var/lib/nre-agent"
USER_DATA_DIR_DEFAULT="1"
AGENT_VERSION=""
AGENT_TAGS=""
AGENT_CAPABILITIES=""
INSTALL_SYSTEMD="0"
INSTALL_LAUNCHD="0"
BINARY_URL=""
SOURCE_DIR="/opt/nginx-reverse-emby-agent"
WRAPPER_SOURCE_DIR=""
SCRIPT_DIR="$(resolve_script_dir 2>/dev/null || true)"
PLATFORM="$(detect_platform)"
ARCH="$(detect_arch)"

if [ $# -gt 0 ] && [ "$1" = "migrate-from-main" ]; then
    COMMAND="migrate-from-main"
    shift 1
elif [ $# -gt 0 ] && [ "$1" = "uninstall-agent" ]; then
    COMMAND="uninstall-agent"
    shift 1
fi

while [ $# -gt 0 ]; do
    case "$1" in
        --master-url) MASTER_URL="$2"; shift 2 ;;
        --asset-base-url) ASSET_BASE_URL="$2"; shift 2 ;;
        --register-token) REGISTER_TOKEN="$2"; shift 2 ;;
        --agent-name) AGENT_NAME="$2"; shift 2 ;;
        --agent-token) AGENT_TOKEN="$2"; shift 2 ;;
        --agent-url) AGENT_URL="$2"; shift 2 ;;
        --data-dir) DATA_DIR="$2"; USER_DATA_DIR_DEFAULT="0"; shift 2 ;;
        --version) AGENT_VERSION="$2"; shift 2 ;;
        --tags) AGENT_TAGS="$2"; shift 2 ;;
        --binary-url) BINARY_URL="$2"; shift 2 ;;
        --source-dir) SOURCE_DIR="$2"; WRAPPER_SOURCE_DIR="$2"; shift 2 ;;
        --install-systemd) INSTALL_SYSTEMD="1"; shift 1 ;;
        --install-launchd) INSTALL_LAUNCHD="1"; shift 1 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "Unknown argument: $1" >&2; usage >&2; exit 1 ;;
    esac
done

case "$COMMAND" in
    join) run_join ;;
    migrate-from-main) run_migrate_from_main ;;
    uninstall-agent) run_uninstall_agent ;;
    *) echo "Unknown command: $COMMAND" >&2; exit 1 ;;
esac
