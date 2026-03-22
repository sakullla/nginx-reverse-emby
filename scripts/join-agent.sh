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

Recommended one-click:
  --install-systemd        Install and start the systemd service

Behavior:
  Automatically installs missing Node.js 18+, curl, and nginx when possible
  (requires root or sudo and a supported package manager / Homebrew)

Optional:
  --master-url URL         Master panel URL (default: embedded panel URL or required when unavailable)
  --asset-base-url URL     Panel-hosted asset base URL (default: embedded panel asset URL)
  --agent-name NAME        Agent node name, default: current hostname
  --agent-token TOKEN      Agent heartbeat token, default: auto-generated
  --agent-url URL          Optional public URL for direct access / display
  --data-dir DIR           Install/state directory, default: ./agent-data
  --rules-file FILE        Rules JSON path, default: <data-dir>/proxy_rules.json
  --state-file FILE        Agent state file, default: <data-dir>/agent-state.json
  --interval-ms N          Heartbeat interval in ms, default: 10000
  --version VERSION        Agent version, default: 1
  --tags TAGS              Comma-separated tags, e.g. edge,emby
  --apply-command CMD      Optional custom apply command; if omitted, installs the built-in nginx apply script
  --deploy-mode MODE       direct or front_proxy; remote agents default to direct
  --local-node             Follow the configured deploy mode like master's local node
  --install-systemd        Install a systemd service for the lightweight agent
  --install-launchd        Install and load a launchd agent on macOS
  -h, --help               Show help

Examples:
  curl -fsSL ${DEFAULT_MASTER_URL:-http://master.example.com:8080}/panel-api/public/join-agent.sh | bash -s -- --register-token change-this-register-token --install-systemd
  join-agent.sh --register-token change-this-register-token --install-systemd
  join-agent.sh --register-token change-this-register-token --install-launchd
  join-agent.sh --register-token change-this-register-token --local-node --deploy-mode front_proxy
  join-agent.sh --master-url http://master.example.com:8080 --register-token change-this-register-token --apply-command '/usr/local/bin/custom-apply.sh'
EOF
}

trim_slash() {
    printf '%s' "$1" | sed 's#/*$##'
}

normalize_deploy_mode() {
    mode_raw=$(printf '%s' "${1:-direct}" | tr '[:upper:]' '[:lower:]' | tr '-' '_')
    case "$mode_raw" in
        front_proxy|front|upstream|proxy) printf 'front_proxy' ;;
        direct|direct_tls|self_managed|host) printf 'direct' ;;
        *) printf 'direct' ;;
    esac
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

xml_escape() {
    printf '%s' "$1" | sed \
        -e 's/&/\&amp;/g' \
        -e 's/</\&lt;/g' \
        -e 's/>/\&gt;/g' \
        -e "s/'/\&apos;/g" \
        -e 's/"/\&quot;/g'
}

detect_node_bin() {
    if command -v node >/dev/null 2>&1; then
        printf '%s\n' "node"
        return 0
    fi
    if command -v nodejs >/dev/null 2>&1; then
        printf '%s\n' "nodejs"
        return 0
    fi
    return 1
}

current_node_major() {
    node_bin="$1"
    "$node_bin" -p "process.versions.node.split('.')[0]" 2>/dev/null || true
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

detect_nginx_conf_path() {
    command -v nginx >/dev/null 2>&1 || return 1

    conf_path=$(nginx -V 2>&1 | sed -n 's/.*--conf-path=\([^ ]*\).*/\1/p' | head -n 1)
    if [ -n "$conf_path" ]; then
        printf '%s\n' "$conf_path"
        return 0
    fi

    [ -f /etc/nginx/nginx.conf ] && {
        printf '%s\n' "/etc/nginx/nginx.conf"
        return 0
    }

    return 1
}

run_root_cmd() {
    if [ -n "${SUDO_BIN:-}" ]; then
        "$SUDO_BIN" "$@"
    else
        "$@"
    fi
}

nginx_supports_early_hints() {
    command -v nginx >/dev/null 2>&1 || return 1

    tmp_dir=$(mktemp -d)
    tmp_conf="$tmp_dir/nginx.conf"
    cat > "$tmp_conf" <<'EOF'
events {}
http {
    map $http_sec_fetch_mode $early_hints {
        default $http2$http3;
    }

    server {
        listen 127.0.0.1:1;

        location / {
            early_hints $early_hints;
            return 204;
        }
    }
}
EOF

    if nginx -t -q -c "$tmp_conf" >/dev/null 2>&1; then
        rm -rf "$tmp_dir"
        return 0
    fi

    rm -rf "$tmp_dir"
    return 1
}

restart_nginx_after_install() {
    run_root_cmd rm -f /etc/nginx/conf.d/default.conf

    if command -v systemctl >/dev/null 2>&1; then
        run_root_cmd mkdir -p /etc/systemd/system/nginx.service.d
        printf '%s\n' '[Service]' 'ExecStartPost=/bin/sleep 0.1' | run_root_cmd tee /etc/systemd/system/nginx.service.d/override.conf >/dev/null
        run_root_cmd systemctl daemon-reload
        run_root_cmd systemctl enable nginx >/dev/null 2>&1 || true
        run_root_cmd systemctl restart nginx
        return 0
    fi

    if command -v service >/dev/null 2>&1; then
        run_root_cmd service nginx restart || run_root_cmd service nginx start
        return 0
    fi

    if command -v rc-update >/dev/null 2>&1; then
        run_root_cmd rc-update add nginx default >/dev/null 2>&1 || true
    fi
    if command -v rc-service >/dev/null 2>&1; then
        run_root_cmd rc-service nginx restart || run_root_cmd rc-service nginx start
    fi
}

ensure_nginx_stream_support() {
    node_bin_path="$1"
    nginx_conf_path="$(detect_nginx_conf_path || true)"
    [ -n "$nginx_conf_path" ] || {
        echo "Unable to detect nginx.conf path automatically." >&2
        exit 1
    }
    [ -f "$nginx_conf_path" ] || {
        echo "nginx.conf not found: $nginx_conf_path" >&2
        exit 1
    }

    SUDO_BIN="$(require_root_or_sudo)" || {
        echo "Updating nginx.conf for agent mode requires root or sudo" >&2
        exit 1
    }

    run_root_cmd mkdir -p /etc/nginx/stream-conf.d/dynamic /etc/nginx/conf.d/dynamic

    tmp_script=$(mktemp)
    cat > "$tmp_script" <<'EOF'
const fs = require('fs')

const mainConf = process.env.NGINX_MAIN_CONF_FILE
let source = fs.readFileSync(mainConf, 'utf8')
let changed = false

function findBlockRange(text, blockName) {
  const regex = new RegExp(`\\b${blockName}\\s*\\{`, 'm')
  const match = regex.exec(text)
  if (!match) return null

  const openBrace = text.indexOf('{', match.index)
  if (openBrace === -1) return null

  let depth = 0
  for (let i = openBrace; i < text.length; i++) {
    const ch = text[i]
    if (ch === '{') depth++
    else if (ch === '}') {
      depth--
      if (depth === 0) {
        return {
          openBrace,
          bodyStart: openBrace + 1,
          bodyEnd: i,
        }
      }
    }
  }
  return null
}

function ensureLinesInBlock(text, blockName, lines) {
  const range = findBlockRange(text, blockName)
  if (!range) return { text, found: false, changed: false }

  const body = text.slice(range.bodyStart, range.bodyEnd)
  const missing = lines.filter((line) => !body.includes(line))
  if (missing.length === 0) return { text, found: true, changed: false }

  const insertion = '\n' + missing.map((line) => `    ${line}`).join('\n') + '\n'
  return {
    text: text.slice(0, range.bodyEnd) + insertion + text.slice(range.bodyEnd),
    found: true,
    changed: true,
  }
}

const streamLines = [
  'include /etc/nginx/stream-conf.d/*.conf;',
  'include /etc/nginx/stream-conf.d/dynamic/*.conf;',
]

const result = ensureLinesInBlock(source, 'stream', streamLines)
if (result.found) {
  if (result.changed) {
    source = result.text
    changed = true
  }
} else {
  source = source.replace(/\s*$/, '\n') + `\nstream {\n    ${streamLines[0]}\n    ${streamLines[1]}\n}\n`
  changed = true
}

if (changed) {
  fs.writeFileSync(mainConf, source, 'utf8')
  console.log(`[JOIN] Updated nginx main config for stream includes: ${mainConf}`)
}
EOF

    if ! run_root_cmd env NGINX_MAIN_CONF_FILE="$nginx_conf_path" "$node_bin_path" "$tmp_script"; then
        rm -f "$tmp_script"
        echo "Failed to update nginx.conf for stream includes" >&2
        exit 1
    fi
    rm -f "$tmp_script"

    if ! run_root_cmd nginx -t; then
        echo "nginx -t failed after updating nginx.conf" >&2
        exit 1
    fi

    if command -v systemctl >/dev/null 2>&1; then
        run_root_cmd systemctl reload nginx || run_root_cmd systemctl restart nginx
    elif command -v service >/dev/null 2>&1; then
        run_root_cmd service nginx reload || run_root_cmd service nginx restart
    elif command -v rc-service >/dev/null 2>&1; then
        run_root_cmd rc-service nginx reload || run_root_cmd rc-service nginx restart
    else
        run_root_cmd nginx -s reload
    fi
}

install_mainline_nginx() {
    platform="$1"
    os_name=""
    pm=""
    gnupg_pm="gnupg"

    if [ "$platform" = "darwin" ] && command -v brew >/dev/null 2>&1; then
        brew update
        brew install nginx
        return 0
    fi

    [ -f /etc/os-release ] || {
        echo "Unsupported system: missing /etc/os-release" >&2
        exit 1
    }
    . /etc/os-release

    case "$ID" in
        debian|devuan|kali)
            os_name="debian"
            pm="apt-get"
            gnupg_pm="gnupg2"
            ;;
        ubuntu)
            os_name="ubuntu"
            pm="apt-get"
            gnupg_pm="gnupg"
            ;;
        centos|fedora|rhel|almalinux|rocky|amzn)
            os_name="rhel"
            if command -v dnf >/dev/null 2>&1; then
                pm="dnf"
            else
                pm="yum"
            fi
            ;;
        arch|archarm)
            os_name="arch"
            pm="pacman"
            ;;
        alpine)
            os_name="alpine"
            pm="apk"
            ;;
        opensuse*|sles)
            os_name="suse"
            pm="zypper"
            ;;
        *)
            echo "Automatic nginx mainline installation is not supported on this system: $ID" >&2
            exit 1
            ;;
    esac

    echo "[JOIN] Installing or upgrading nginx mainline for $os_name..."

    case "$os_name" in
        debian|ubuntu)
            SUDO="${SUDO_BIN:-}"
            $SUDO "$pm" update
            $SUDO "$pm" install -y "$gnupg_pm" ca-certificates lsb-release "${os_name}-keyring"
            curl -sL https://nginx.org/keys/nginx_signing.key | $SUDO gpg --dearmor -o /usr/share/keyrings/nginx-archive-keyring.gpg
            echo "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.gpg] http://nginx.org/packages/mainline/$os_name `lsb_release -cs` nginx" | $SUDO tee /etc/apt/sources.list.d/nginx.list > /dev/null
            echo -e "Package: *\nPin: origin nginx.org\nPin: release o=nginx\nPin-Priority: 900" | $SUDO tee /etc/apt/preferences.d/99nginx > /dev/null
            $SUDO "$pm" update
            $SUDO "$pm" install -y nginx
            $SUDO mkdir -p /etc/systemd/system/nginx.service.d
            echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" | $SUDO tee /etc/systemd/system/nginx.service.d/override.conf > /dev/null
            $SUDO systemctl daemon-reload
            $SUDO rm -f /etc/nginx/conf.d/default.conf
            $SUDO systemctl restart nginx
            ;;
        rhel)
            SUDO="${SUDO_BIN:-}"
            $SUDO "$pm" install -y yum-utils
            echo -e "[nginx-mainline]\nname=NGINX Mainline Repository\nbaseurl=https://nginx.org/packages/mainline/centos/\$releasever/\$basearch/\ngpgcheck=1\nenabled=1\ngpgkey=https://nginx.org/keys/nginx_signing.key" | $SUDO tee /etc/yum.repos.d/nginx.repo > /dev/null
            $SUDO "$pm" install -y nginx
            $SUDO mkdir -p /etc/systemd/system/nginx.service.d
            echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" | $SUDO tee /etc/systemd/system/nginx.service.d/override.conf > /dev/null
            $SUDO systemctl daemon-reload
            $SUDO rm -f /etc/nginx/conf.d/default.conf
            $SUDO systemctl restart nginx
            ;;
        arch)
            SUDO="${SUDO_BIN:-}"
            $SUDO "$pm" -Sy --noconfirm nginx-mainline
            $SUDO mkdir -p /etc/systemd/system/nginx.service.d
            echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" | $SUDO tee /etc/systemd/system/nginx.service.d/override.conf > /dev/null
            $SUDO systemctl daemon-reload
            $SUDO rm -f /etc/nginx/conf.d/default.conf
            $SUDO systemctl restart nginx
            ;;
        alpine)
            SUDO="${SUDO_BIN:-}"
            $SUDO "$pm" add --no-cache nginx
            $SUDO rc-update add nginx default
            $SUDO rm -f /etc/nginx/conf.d/default.conf
            $SUDO rc-service nginx restart
            ;;
        suse)
            run_root_cmd "$pm" --non-interactive install --no-recommends nginx
            restart_nginx_after_install
            ;;
    esac
}

install_runtime_packages() {
    missing_node="$1"
    missing_curl="$2"
    missing_nginx="$3"
    platform="$4"

    [ "$missing_node$missing_curl$missing_nginx" != "000" ] || return 0

    SUDO_BIN="$(require_root_or_sudo)" || {
        echo "Missing runtime dependencies and automatic install requires root or sudo" >&2
        echo "Please install Node.js 18+, curl, and nginx manually, or rerun as root." >&2
        exit 1
    }

    echo "[JOIN] Missing dependencies detected. Attempting automatic installation..."

    if [ "$platform" = "darwin" ] && command -v brew >/dev/null 2>&1; then
        pkgs=""
        [ "$missing_node" = "1" ] && pkgs="$pkgs node"
        [ "$missing_curl" = "1" ] && pkgs="$pkgs curl"
        [ "$missing_nginx" = "1" ] && pkgs="$pkgs nginx"
        if [ -n "$pkgs" ]; then
            brew update
            brew install $pkgs
        fi
        return 0
    fi

    if command -v apt-get >/dev/null 2>&1; then
        run_root_cmd apt-get update
        pkgs="ca-certificates"
        [ "$missing_node" = "1" ] && pkgs="$pkgs nodejs"
        [ "$missing_curl" = "1" ] && pkgs="$pkgs curl"
        run_root_cmd apt-get install -y --no-install-recommends $pkgs
        [ "$missing_nginx" = "1" ] && install_mainline_nginx "$platform"
        return 0
    fi

    if command -v dnf >/dev/null 2>&1; then
        pkgs="ca-certificates"
        [ "$missing_node" = "1" ] && pkgs="$pkgs nodejs"
        [ "$missing_curl" = "1" ] && pkgs="$pkgs curl"
        run_root_cmd dnf install -y $pkgs
        [ "$missing_nginx" = "1" ] && install_mainline_nginx "$platform"
        return 0
    fi

    if command -v yum >/dev/null 2>&1; then
        pkgs="ca-certificates"
        [ "$missing_node" = "1" ] && pkgs="$pkgs nodejs"
        [ "$missing_curl" = "1" ] && pkgs="$pkgs curl"
        run_root_cmd yum install -y $pkgs
        [ "$missing_nginx" = "1" ] && install_mainline_nginx "$platform"
        return 0
    fi

    if command -v apk >/dev/null 2>&1; then
        pkgs="ca-certificates"
        [ "$missing_node" = "1" ] && pkgs="$pkgs nodejs npm"
        [ "$missing_curl" = "1" ] && pkgs="$pkgs curl"
        run_root_cmd apk add --no-cache $pkgs
        [ "$missing_nginx" = "1" ] && install_mainline_nginx "$platform"
        return 0
    fi

    if command -v zypper >/dev/null 2>&1; then
        pkgs="ca-certificates"
        [ "$missing_node" = "1" ] && pkgs="$pkgs nodejs18"
        [ "$missing_curl" = "1" ] && pkgs="$pkgs curl"
        run_root_cmd zypper --non-interactive install --no-recommends $pkgs
        [ "$missing_nginx" = "1" ] && install_mainline_nginx "$platform"
        return 0
    fi

    if command -v pacman >/dev/null 2>&1; then
        pkgs="ca-certificates"
        [ "$missing_node" = "1" ] && pkgs="$pkgs nodejs"
        [ "$missing_curl" = "1" ] && pkgs="$pkgs curl"
        run_root_cmd pacman -Sy --noconfirm $pkgs
        [ "$missing_nginx" = "1" ] && install_mainline_nginx "$platform"
        return 0
    fi

    echo "Automatic dependency installation is not supported on this system." >&2
    echo "Please install Node.js 18+, curl, and nginx mainline manually." >&2
    [ "$platform" = "darwin" ] && echo "Tip: install Homebrew first, then rerun this script." >&2
    exit 1
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

copy_or_download_asset() {
    asset_name="$1"
    dest_path="$2"
    chmod_mode="$3"
    local_path=""

    case "$asset_name" in
        light-agent.js)
            [ -n "$SCRIPT_DIR" ] && local_path="$SCRIPT_DIR/light-agent.js"
            ;;
        light-agent-apply.sh)
            [ -n "$SCRIPT_DIR" ] && local_path="$SCRIPT_DIR/light-agent-apply.sh"
            ;;
        25-dynamic-reverse-proxy.sh)
            [ -n "$SCRIPT_DIR" ] && local_path="$SCRIPT_DIR/../docker/25-dynamic-reverse-proxy.sh"
            ;;
        default.conf.template)
            [ -n "$SCRIPT_DIR" ] && local_path="$SCRIPT_DIR/../docker/default.conf.template"
            ;;
        default.direct.no_tls.conf.template)
            [ -n "$SCRIPT_DIR" ] && local_path="$SCRIPT_DIR/../docker/default.direct.no_tls.conf.template"
            ;;
        default.direct.tls.conf.template)
            [ -n "$SCRIPT_DIR" ] && local_path="$SCRIPT_DIR/../docker/default.direct.tls.conf.template"
            ;;
    esac

    mkdir -p "$(dirname -- "$dest_path")"

    if [ -n "$local_path" ] && [ -f "$local_path" ]; then
        cp "$local_path" "$dest_path"
        chmod "$chmod_mode" "$dest_path"
        return 0
    fi

    [ -n "$ASSET_BASE_URL" ] || {
        echo "Missing asset source for $asset_name. Re-run with --asset-base-url URL or download from the panel." >&2
        exit 1
    }

    curl -fsSL "$ASSET_BASE_URL/$asset_name" -o "$dest_path"
    chmod "$chmod_mode" "$dest_path"
}

MASTER_URL="$DEFAULT_MASTER_URL"
ASSET_BASE_URL="$DEFAULT_ASSET_BASE_URL"
PLATFORM="$(detect_platform)"
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
DEPLOY_MODE="${PROXY_DEPLOY_MODE:-direct}"
LOCAL_NODE="0"
INSTALL_SYSTEMD="0"
INSTALL_LAUNCHD="0"
SCRIPT_DIR="$(resolve_script_dir 2>/dev/null || true)"

while [ $# -gt 0 ]; do
    case "$1" in
        --master-url) MASTER_URL="$2"; shift 2 ;;
        --asset-base-url) ASSET_BASE_URL="$2"; shift 2 ;;
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
        --deploy-mode) DEPLOY_MODE="$2"; shift 2 ;;
        --local-node) LOCAL_NODE="1"; shift 1 ;;
        --install-systemd) INSTALL_SYSTEMD="1"; shift 1 ;;
        --install-launchd) INSTALL_LAUNCHD="1"; shift 1 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "Unknown argument: $1" >&2; usage >&2; exit 1 ;;
    esac
done

[ -n "$REGISTER_TOKEN" ] || { echo "Missing --register-token" >&2; exit 1; }
[ -n "$MASTER_URL" ] || {
    echo "Missing --master-url and no embedded panel URL is available" >&2
    exit 1
}
[ "$INSTALL_SYSTEMD$INSTALL_LAUNCHD" != "11" ] || {
    echo "Use either --install-systemd or --install-launchd, not both" >&2
    exit 1
}

MISSING_NODE="0"
MISSING_CURL="0"
MISSING_NGINX="0"

NODE_BIN="$(detect_node_bin || true)"
[ -n "$NODE_BIN" ] || MISSING_NODE="1"
command -v curl >/dev/null 2>&1 || MISSING_CURL="1"
command -v nginx >/dev/null 2>&1 || MISSING_NGINX="1"

install_runtime_packages "$MISSING_NODE" "$MISSING_CURL" "$MISSING_NGINX" "$PLATFORM"

NODE_BIN="$(detect_node_bin || true)"
[ -n "$NODE_BIN" ] || { echo "node is required after dependency installation" >&2; exit 1; }
NODE_MAJOR="$(current_node_major "$NODE_BIN")"
[ -n "$NODE_MAJOR" ] || { echo "unable to determine Node.js version" >&2; exit 1; }
[ "$NODE_MAJOR" -ge 18 ] || {
    echo "Node.js 18+ is required, but found version $NODE_MAJOR" >&2
    echo "Please upgrade Node.js on this machine and retry." >&2
    exit 1
}

command -v curl >/dev/null 2>&1 || { echo "curl is required after dependency installation" >&2; exit 1; }
NGINX_BIN_PATH="$(command -v nginx || true)"
[ -n "$NGINX_BIN_PATH" ] || { echo "nginx is required after dependency installation" >&2; exit 1; }
if ! nginx_supports_early_hints; then
    echo "[JOIN] Detected nginx without early_hints support, upgrading to nginx mainline..." >&2
    SUDO_BIN="$(require_root_or_sudo)" || {
        echo "Upgrading nginx requires root or sudo" >&2
        exit 1
    }
    install_mainline_nginx "$PLATFORM"
    NGINX_BIN_PATH="$(command -v nginx || true)"
    if ! nginx_supports_early_hints; then
        echo "Installed nginx still does not support early_hints. Please install a newer nginx mainline release manually." >&2
        exit 1
    fi
fi
NODE_BIN_PATH="$(command -v "$NODE_BIN" || true)"
[ -n "$NODE_BIN_PATH" ] || { echo "unable to resolve node executable path" >&2; exit 1; }
ensure_nginx_stream_support "$NODE_BIN_PATH"

MASTER_URL="$(trim_slash "$MASTER_URL")"
ASSET_BASE_URL="$(trim_slash "$ASSET_BASE_URL")"
AGENT_URL="$(trim_slash "$AGENT_URL")"
AGENT_TOKEN="${AGENT_TOKEN:-$(generate_token)}"
DEPLOY_MODE="$(normalize_deploy_mode "$DEPLOY_MODE")"
mkdir -p "$DATA_DIR"
DATA_DIR="$(CDPATH= cd -- "$DATA_DIR" && pwd)"

RULES_FILE="${RULES_FILE:-$DATA_DIR/proxy_rules.json}"
STATE_FILE="${STATE_FILE:-$DATA_DIR/agent-state.json}"
RULES_FILE="$(absolute_path "$RULES_FILE")"
STATE_FILE="$(absolute_path "$STATE_FILE")"
ENV_FILE="$DATA_DIR/agent.env"
BIN_DIR="$DATA_DIR/bin"
RUNTIME_DIR="$DATA_DIR/runtime"
LIGHT_AGENT_FILE="$BIN_DIR/light-agent.js"
DEFAULT_APPLY_SCRIPT="$BIN_DIR/nginx-reverse-emby-apply.sh"
GENERATOR_FILE="$RUNTIME_DIR/25-dynamic-reverse-proxy.sh"
TEMPLATE_FILE="$RUNTIME_DIR/default.conf.template"
DIRECT_NO_TLS_TEMPLATE_FILE="$RUNTIME_DIR/default.direct.no_tls.conf.template"
DIRECT_TLS_TEMPLATE_FILE="$RUNTIME_DIR/default.direct.tls.conf.template"

echo "[JOIN] Installing runtime assets to: $DATA_DIR"
copy_or_download_asset light-agent.js "$LIGHT_AGENT_FILE" 755
copy_or_download_asset light-agent-apply.sh "$DEFAULT_APPLY_SCRIPT" 755
copy_or_download_asset 25-dynamic-reverse-proxy.sh "$GENERATOR_FILE" 755
copy_or_download_asset default.conf.template "$TEMPLATE_FILE" 644
copy_or_download_asset default.direct.no_tls.conf.template "$DIRECT_NO_TLS_TEMPLATE_FILE" 644
copy_or_download_asset default.direct.tls.conf.template "$DIRECT_TLS_TEMPLATE_FILE" 644

if [ -z "$APPLY_COMMAND" ]; then
    APPLY_COMMAND="$DEFAULT_APPLY_SCRIPT"
fi

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
AGENT_HOME=$(shell_quote "$DATA_DIR")
AGENT_RUNTIME_DIR=$(shell_quote "$RUNTIME_DIR")
AGENT_GENERATOR_SCRIPT=$(shell_quote "$GENERATOR_FILE")
AGENT_DEFAULT_APPLY_COMMAND=$(shell_quote "$DEFAULT_APPLY_SCRIPT")
APPLY_COMMAND=$(shell_quote "$APPLY_COMMAND")
PROXY_DEPLOY_MODE=$(shell_quote "$DEPLOY_MODE")
AGENT_FOLLOW_MASTER_DEPLOY_MODE=$(shell_quote "$LOCAL_NODE")
NGINX_BIN=$(shell_quote "$NGINX_BIN_PATH")
EOF

PAYLOAD=$("$NODE_BIN" -e "const payload = {name: process.argv[1], agent_url: process.argv[2], agent_token: process.argv[3], version: process.argv[4], tags: process.argv[5] ? process.argv[5].split(',').map(v => v.trim()).filter(Boolean) : [], mode: 'pull', register_token: process.argv[6]}; process.stdout.write(JSON.stringify(payload));" "$AGENT_NAME" "$AGENT_URL" "$AGENT_TOKEN" "$AGENT_VERSION" "$AGENT_TAGS" "$REGISTER_TOKEN")

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
echo "[JOIN] Light agent: $LIGHT_AGENT_FILE"
echo "[JOIN] Apply command: $APPLY_COMMAND"

if [ "$INSTALL_SYSTEMD" = "1" ]; then
    command -v systemctl >/dev/null 2>&1 || { echo "systemctl is required for --install-systemd" >&2; exit 1; }
    SERVICE_FILE="/etc/systemd/system/nginx-reverse-emby-agent.service"
    cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Nginx Reverse Emby Lightweight Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$ENV_FILE
ExecStart=$NODE_BIN_PATH $LIGHT_AGENT_FILE
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable --now nginx-reverse-emby-agent.service
    echo "[JOIN] Installed and started systemd service: nginx-reverse-emby-agent.service"
elif [ "$INSTALL_LAUNCHD" = "1" ]; then
    [ "$PLATFORM" = "darwin" ] || { echo "--install-launchd is only supported on macOS" >&2; exit 1; }
    command -v launchctl >/dev/null 2>&1 || { echo "launchctl is required for --install-launchd" >&2; exit 1; }
    LAUNCHD_DIR="$HOME/Library/LaunchAgents"
    SERVICE_LABEL="com.nginx-reverse-emby.agent"
    SERVICE_FILE="$LAUNCHD_DIR/$SERVICE_LABEL.plist"
    START_COMMAND="set -a && . $(shell_quote "$ENV_FILE") && set +a && exec $(shell_quote "$NODE_BIN_PATH") $(shell_quote "$LIGHT_AGENT_FILE")"
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
    echo "  set -a && . $ENV_FILE && set +a && $NODE_BIN_PATH $LIGHT_AGENT_FILE"
fi
