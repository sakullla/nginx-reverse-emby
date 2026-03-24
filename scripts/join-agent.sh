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
  Automatically installs missing Node.js 18+, curl, nginx, openssl, and socat when possible
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

normalize_master_url() {
    value="$(trim_slash "$1")"
    value="$(printf '%s' "$value" | sed 's#/panel-api/public/join-agent\.sh$##')"
    value="$(printf '%s' "$value" | sed 's#/panel-api$##')"
    printf '%s' "$value"
}

is_valid_master_url() {
    printf '%s' "$1" | grep -Eq '^https?://[^/]+$'
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
    [ -n "$node_bin" ] || return 0
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

systemctl_usable() {
    command -v systemctl >/dev/null 2>&1 || return 1
    [ -d /run/systemd/system ]
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

nginx_supports_stream() {
    command -v nginx >/dev/null 2>&1 || return 1
    nginx_build_info="$(nginx -V 2>&1 || true)"
    printf '%s' "$nginx_build_info" | grep -Eq -- '--with-stream(=dynamic)?' || return 1

    load_module_line=""
    if printf '%s' "$nginx_build_info" | grep -Eq -- '--with-stream=dynamic'; then
        nginx_conf_path="$(detect_nginx_conf_path || true)"
        [ -n "$nginx_conf_path" ] || return 1
        [ -f "$nginx_conf_path" ] || return 1
        load_module_line="$(grep -E '^[[:space:]]*load_module[[:space:]].*ngx_stream_module\.so;' "$nginx_conf_path" | head -n 1 || true)"
        [ -n "$load_module_line" ] || return 1
    fi

    tmp_dir=$(mktemp -d)
    tmp_conf="$tmp_dir/nginx.conf"
    {
        [ -n "$load_module_line" ] && printf '%s\n' "$load_module_line"
        printf '%s\n' 'events {}'
        printf '%s\n' 'stream {' '    server { listen 127.0.0.1:1; }' '}'
    } > "$tmp_conf"

    if nginx -t -q -c "$tmp_conf" >/dev/null 2>&1; then
        rm -rf "$tmp_dir"
        return 0
    fi

    rm -rf "$tmp_dir"
    return 1
}

run_root_cmd() {
    if [ -n "${SUDO_BIN:-}" ]; then
        "$SUDO_BIN" "$@"
    else
        "$@"
    fi
}

apt_get_noninteractive() {
    command -v apt-get >/dev/null 2>&1 || return 127

    attempt=1
    max_attempts="${APT_RETRY_ATTEMPTS:-5}"
    retry_delay="${APT_RETRY_DELAY_SECONDS:-5}"

    while [ "$attempt" -le "$max_attempts" ]; do
        if run_root_cmd env \
            DEBIAN_FRONTEND=noninteractive \
            APT_LISTCHANGES_FRONTEND=none \
            apt-get \
            -o Acquire::Retries=3 \
            -o Acquire::http::Timeout=30 \
            -o Acquire::https::Timeout=30 \
            -o Dpkg::Use-Pty=0 \
            "$@"; then
            return 0
        fi

        if [ "$attempt" -ge "$max_attempts" ]; then
            break
        fi

        echo "[JOIN] apt-get $1 failed (attempt $attempt/$max_attempts), retrying in ${retry_delay}s..." >&2
        sleep "$retry_delay"
        attempt=$((attempt + 1))
    done

    return 1
}

zypper_has_package() {
    command -v zypper >/dev/null 2>&1 || return 1
    zypper -n search -s "$1" 2>/dev/null | grep -Eq '^[^|]*\|[[:space:]]*'"$1"'[[:space:]]*\|'
}

install_nodejs_apt() {
    target_major="${1:-20}"
    arch="$(dpkg --print-architecture 2>/dev/null || true)"

    case "$arch" in
        amd64|arm64|armhf|ppc64el|s390x) ;;
        *)
            echo "Automatic Node.js installation is not supported on this architecture: ${arch:-unknown}" >&2
            return 1
            ;;
    esac

    echo "[JOIN] Installing Node.js ${target_major}.x from NodeSource..."
    apt_get_noninteractive update
    apt_get_noninteractive install -y --no-install-recommends ca-certificates curl gnupg
    run_root_cmd mkdir -p /usr/share/keyrings

    tmp_key="$(mktemp)"
    curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key -o "$tmp_key"
    run_root_cmd gpg --dearmor --yes -o /usr/share/keyrings/nodesource.gpg "$tmp_key"
    rm -f "$tmp_key"

    printf 'deb [arch=%s signed-by=/usr/share/keyrings/nodesource.gpg] https://deb.nodesource.com/node_%s.x nodistro main\n' "$arch" "$target_major" | run_root_cmd tee /etc/apt/sources.list.d/nodesource.list >/dev/null
    apt_get_noninteractive update
    apt_get_noninteractive install -y --no-install-recommends nodejs
}

install_nodejs_rpm() {
    target_major="${1:-20}"

    echo "[JOIN] Installing Node.js ${target_major}.x from NodeSource..."
    tmp_script="$(mktemp)"
    curl -fsSL "https://rpm.nodesource.com/setup_${target_major}.x" -o "$tmp_script"
    chmod 700 "$tmp_script"
    run_root_cmd bash "$tmp_script"
    rm -f "$tmp_script"

    if command -v dnf >/dev/null 2>&1; then
        run_root_cmd dnf install -y nodejs
    else
        run_root_cmd yum install -y nodejs
    fi
}

enable_nginx_stream_dynamic_module() {
    command -v nginx >/dev/null 2>&1 || return 1

    nginx_conf_path="$(detect_nginx_conf_path || true)"
    [ -n "$nginx_conf_path" ] || return 1
    [ -f "$nginx_conf_path" ] || return 1

    if grep -Eq '^[[:space:]]*load_module[[:space:]].*ngx_stream_module\.so;' "$nginx_conf_path"; then
        return 0
    fi

    module_path=""
    for candidate in \
        /usr/lib/nginx/modules/ngx_stream_module.so \
        /usr/lib64/nginx/modules/ngx_stream_module.so \
        /etc/nginx/modules/ngx_stream_module.so
    do
        if [ -f "$candidate" ]; then
            module_path="$candidate"
            break
        fi
    done

    [ -n "$module_path" ] || return 1

    SUDO_BIN="$(require_root_or_sudo)" || {
        echo "Enabling nginx stream support requires root or sudo" >&2
        return 1
    }

    tmp_conf_local="$(mktemp)"
    if grep -Eq '^[[:space:]]*#[[:space:]]*load_module[[:space:]].*ngx_stream_module\.so;' "$nginx_conf_path"; then
        sed 's@^[[:space:]]*#[[:space:]]*load_module[[:space:]]\(.*ngx_stream_module\.so;.*\)$@load_module \1@' "$nginx_conf_path" > "$tmp_conf_local"
    else
        {
            printf 'load_module %s;\n' "$module_path"
            cat "$nginx_conf_path"
        } > "$tmp_conf_local"
    fi

    nginx_prefix="$(dirname -- "$nginx_conf_path")"
    tmp_conf="$(run_root_cmd mktemp "$nginx_prefix/nginx.conf.XXXXXX")"
    run_root_cmd cp "$tmp_conf_local" "$tmp_conf"
    rm -f "$tmp_conf_local"

    if ! run_root_cmd nginx -t -q -c "$tmp_conf" >/dev/null 2>&1; then
        run_root_cmd rm -f "$tmp_conf"
        return 1
    fi

    run_root_cmd cp "$tmp_conf" "$nginx_conf_path"
    run_root_cmd rm -f "$tmp_conf"
    return 0
}

ensure_nginx_stream_module() {
    enable_nginx_stream_dynamic_module || true
    if nginx_supports_stream; then
        return 0
    fi

    if command -v dnf >/dev/null 2>&1; then
        run_root_cmd dnf install -y nginx-module-stream >/dev/null 2>&1 || true
    elif command -v yum >/dev/null 2>&1; then
        run_root_cmd yum install -y nginx-module-stream >/dev/null 2>&1 || true
    fi

    enable_nginx_stream_dynamic_module || true
    nginx_supports_stream
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

    if systemctl_usable; then
        run_root_cmd mkdir -p /etc/systemd/system/nginx.service.d
        printf '%s\n' '[Service]' 'ExecStartPost=/bin/sleep 0.1' | run_root_cmd tee /etc/systemd/system/nginx.service.d/override.conf >/dev/null
        run_root_cmd systemctl daemon-reload
        run_root_cmd systemctl enable nginx >/dev/null 2>&1 || true
        run_root_cmd systemctl restart nginx
        return 0
    fi

    if command -v service >/dev/null 2>&1; then
        if run_root_cmd service nginx restart || run_root_cmd service nginx start; then
            return 0
        fi
    fi

    if command -v rc-update >/dev/null 2>&1; then
        run_root_cmd rc-update add nginx default >/dev/null 2>&1 || true
    fi
    if command -v rc-service >/dev/null 2>&1; then
        if run_root_cmd rc-service nginx restart || run_root_cmd rc-service nginx start; then
            return 0
        fi
    fi

    if run_root_cmd nginx -s reload >/dev/null 2>&1; then
        return 0
    fi

    run_root_cmd nginx
}

has_ipv6() {
    command -v ip >/dev/null 2>&1 || return 1
    ip -6 addr show scope global 2>/dev/null | grep -q inet6
}

get_resolver_host() {
    system_dns=$(awk '
        BEGIN { first = 1 }
        /^nameserver[[:space:]]+/ {
            value = ($2 ~ /:/ ? "[" $2 "]" : $2)
            if (!first) {
                printf " "
            }
            printf "%s", value
            first = 0
        }
    ' /etc/resolv.conf 2>/dev/null || true)
    if [ -n "$system_dns" ]; then
        printf '%s\n' "$system_dns"
        return 0
    fi

    printf '%s\n' "1.1.1.1 8.8.8.8"
}

get_nginx_resolver() {
    if [ -n "${NGINX_LOCAL_RESOLVERS:-}" ]; then
        printf '%s\n' "$NGINX_LOCAL_RESOLVERS"
        return 0
    fi

    resolver_hosts="$(get_resolver_host)"
    if has_ipv6; then
        printf '%s\n' "$resolver_hosts"
    else
        printf '%s ipv6=off\n' "$resolver_hosts"
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
    stream_resolver="$(get_nginx_resolver)"

    run_root_cmd mkdir -p /etc/nginx/stream-conf.d/dynamic /etc/nginx/conf.d/dynamic

    tmp_script=$(mktemp)
    cat > "$tmp_script" <<'EOF'
const fs = require('fs')

const mainConf = process.env.NGINX_MAIN_CONF_FILE
const streamResolver = String(process.env.NGINX_STREAM_RESOLVER || '').trim()
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
  ...(streamResolver
    ? [
        `resolver ${streamResolver};`,
        'resolver_timeout 5s;',
      ]
    : []),
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
  source =
    source.replace(/\s*$/, '\n') +
    `\nstream {\n    ${streamLines.join('\n    ')}\n}\n`
  changed = true
}

if (changed) {
  fs.writeFileSync(mainConf, source, 'utf8')
  console.log(`[JOIN] Updated nginx main config for stream includes: ${mainConf}`)
}
EOF

    if ! run_root_cmd env NGINX_MAIN_CONF_FILE="$nginx_conf_path" NGINX_STREAM_RESOLVER="$stream_resolver" "$node_bin_path" "$tmp_script"; then
        rm -f "$tmp_script"
        echo "Failed to update nginx.conf for stream includes" >&2
        exit 1
    fi
    rm -f "$tmp_script"

    if ! run_root_cmd nginx -t; then
        echo "nginx -t failed after updating nginx.conf" >&2
        exit 1
    fi

    if systemctl_usable; then
        run_root_cmd systemctl reload nginx || run_root_cmd systemctl restart nginx
    elif command -v service >/dev/null 2>&1; then
        if run_root_cmd service nginx reload || run_root_cmd service nginx restart; then
            return 0
        fi
    elif command -v rc-service >/dev/null 2>&1; then
        if run_root_cmd rc-service nginx reload || run_root_cmd rc-service nginx restart; then
            return 0
        fi
    fi

    run_root_cmd nginx -s reload || run_root_cmd nginx
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
        centos)
            os_name="centos"
            if command -v dnf >/dev/null 2>&1; then
                pm="dnf"
            else
                pm="yum"
            fi
            ;;
        rhel|almalinux|rocky)
            os_name="rhel"
            if command -v dnf >/dev/null 2>&1; then
                pm="dnf"
            else
                pm="yum"
            fi
            ;;
        fedora)
            os_name="fedora"
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
        opensuse-tumbleweed)
            os_name="opensuse_tumbleweed"
            pm="zypper"
            ;;
        opensuse-leap)
            os_name="opensuse_leap"
            pm="zypper"
            ;;
        sles)
            os_name="sles"
            pm="zypper"
            ;;
        *)
            echo "[JOIN] Nginx mainline not supported on $ID, falling back to system nginx package..." >&2
            install_system_nginx
            return $?
            ;;
    esac

    echo "[JOIN] Installing or upgrading nginx mainline for $os_name..."

    case "$os_name" in
        debian|ubuntu)
            apt_get_noninteractive update
            apt_get_noninteractive install -y --no-install-recommends "$gnupg_pm" ca-certificates curl lsb-release "${os_name}-keyring"
            run_root_cmd mkdir -p /usr/share/keyrings
            curl -fsSL https://nginx.org/keys/nginx_signing.key | run_root_cmd gpg --dearmor --yes -o /usr/share/keyrings/nginx-archive-keyring.gpg
            echo "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.gpg] http://nginx.org/packages/mainline/$os_name `lsb_release -cs` nginx" | run_root_cmd tee /etc/apt/sources.list.d/nginx.list > /dev/null
            printf '%s\n' "Package: *" "Pin: origin nginx.org" "Pin-Priority: 900" | run_root_cmd tee /etc/apt/preferences.d/99nginx > /dev/null
            apt_get_noninteractive update
            apt_get_noninteractive install -y --no-install-recommends nginx
            enable_nginx_stream_dynamic_module || true
            restart_nginx_after_install
            ;;
        centos|rhel)
            SUDO="${SUDO_BIN:-}"
            $SUDO "$pm" install -y yum-utils
            release_major="${VERSION_ID%%.*}"
            arch_name="$(uname -m)"
            echo -e "[nginx-mainline]\nname=NGINX Mainline Repository\nbaseurl=https://nginx.org/packages/mainline/$os_name/$release_major/$arch_name/\ngpgcheck=1\nenabled=1\ngpgkey=https://nginx.org/keys/nginx_signing.key" | $SUDO tee /etc/yum.repos.d/nginx.repo > /dev/null
            $SUDO "$pm" install -y nginx
            restart_nginx_after_install
            ;;
        fedora)
            echo "[JOIN] Nginx mainline not supported on Fedora, falling back to system nginx package..." >&2
            install_system_nginx
            return $?
            ;;
        arch)
            SUDO="${SUDO_BIN:-}"
            $SUDO "$pm" -Sy --noconfirm nginx-mainline
            restart_nginx_after_install
            ;;
        alpine)
            SUDO="${SUDO_BIN:-}"
            alpine_minor="$(printf '%s' "$VERSION_ID" | awk -F. '{ print $1 "." $2 }')"
            [ -n "$alpine_minor" ] || {
                echo "Unable to determine Alpine major.minor version from VERSION_ID=$VERSION_ID" >&2
                exit 1
            }
            run_root_cmd mkdir -p /etc/apk/keys
            curl -fsSL https://nginx.org/keys/nginx_signing.rsa.pub -o /tmp/nginx_signing.rsa.pub
            run_root_cmd mv /tmp/nginx_signing.rsa.pub /etc/apk/keys/nginx_signing.rsa.pub
            repo_url="https://nginx.org/packages/mainline/alpine/v$alpine_minor/main"
            if ! grep -Fq "$repo_url" /etc/apk/repositories 2>/dev/null; then
                printf '%s\n' "$repo_url" | run_root_cmd tee -a /etc/apk/repositories >/dev/null
            fi
            run_root_cmd "$pm" update
            $SUDO "$pm" add --no-cache nginx
            restart_nginx_after_install
            ;;
        opensuse_tumbleweed)
            run_root_cmd "$pm" --non-interactive install --no-recommends nginx
            enable_nginx_stream_dynamic_module || true
            restart_nginx_after_install
            ;;
        opensuse_leap)
            echo "[JOIN] Nginx mainline not supported on openSUSE Leap, falling back to system nginx package..." >&2
            install_system_nginx
            return $?
            ;;
        sles)
            sles_major="${VERSION_ID%%.*}"
            run_root_cmd rpm --import https://nginx.org/keys/nginx_signing.key
            if ! zypper lr | awk '{print $2}' | grep -qx nginx-mainline; then
                run_root_cmd "$pm" --non-interactive addrepo -f "https://nginx.org/packages/mainline/sles/$sles_major" nginx-mainline
            fi
            run_root_cmd "$pm" --non-interactive refresh nginx-mainline
            run_root_cmd "$pm" --non-interactive install --no-recommends nginx
            enable_nginx_stream_dynamic_module || true
            restart_nginx_after_install
            ;;
        suse)
            echo "[JOIN] Nginx mainline not supported on this SUSE target, falling back to system nginx package..." >&2
            install_system_nginx
            return $?
            ;;
    esac
}

install_system_nginx() {
    echo "[JOIN] Attempting to install system nginx package..."

    if command -v apt-get >/dev/null 2>&1; then
        apt_get_noninteractive install -y --no-install-recommends nginx || return 1
        restart_nginx_after_install
        return 0
    fi

    if command -v dnf >/dev/null 2>&1; then
        run_root_cmd dnf install -y nginx || return 1
        restart_nginx_after_install
        return 0
    fi

    if command -v yum >/dev/null 2>&1; then
        run_root_cmd yum install -y nginx || return 1
        restart_nginx_after_install
        return 0
    fi

    if command -v zypper >/dev/null 2>&1; then
        run_root_cmd zypper --non-interactive install --no-recommends nginx || return 1
        restart_nginx_after_install
        return 0
    fi

    if command -v pacman >/dev/null 2>&1; then
        SUDO="${SUDO_BIN:-}"
        $SUDO pacman -Sy --noconfirm nginx || return 1
        restart_nginx_after_install
        return 0
    fi

    if command -v apk >/dev/null 2>&1; then
        SUDO="${SUDO_BIN:-}"
        $SUDO apk add --no-cache nginx || return 1
        restart_nginx_after_install
        return 0
    fi

    echo "[JOIN] Unable to install nginx automatically. Please install nginx manually." >&2
    return 1
}

install_runtime_packages() {
    missing_node="$1"
    missing_curl="$2"
    missing_nginx="$3"
    missing_openssl="$4"
    missing_socat="$5"
    platform="$6"

    [ "$missing_node$missing_curl$missing_nginx$missing_openssl$missing_socat" != "00000" ] || return 0

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
        [ "$missing_openssl" = "1" ] && pkgs="$pkgs openssl@3"
        [ "$missing_socat" = "1" ] && pkgs="$pkgs socat"
        if [ -n "$pkgs" ]; then
            brew update
            brew install $pkgs
        fi
        return 0
    fi

    if command -v apt-get >/dev/null 2>&1; then
        apt_get_noninteractive update
        pkgs="ca-certificates"
        [ "$missing_curl" = "1" ] && pkgs="$pkgs curl"
        [ "$missing_openssl" = "1" ] && pkgs="$pkgs openssl"
        [ "$missing_socat" = "1" ] && pkgs="$pkgs socat"
        apt_get_noninteractive install -y --no-install-recommends $pkgs
        [ "$missing_node" = "1" ] && install_nodejs_apt 20
        [ "$missing_nginx" = "1" ] && install_mainline_nginx "$platform"
        return 0
    fi

    if command -v dnf >/dev/null 2>&1; then
        pkgs="ca-certificates"
        [ "$missing_curl" = "1" ] && pkgs="$pkgs curl"
        [ "$missing_openssl" = "1" ] && pkgs="$pkgs openssl"
        [ "$missing_socat" = "1" ] && pkgs="$pkgs socat"
        run_root_cmd dnf install -y $pkgs
        [ "$missing_node" = "1" ] && install_nodejs_rpm 20
        [ "$missing_nginx" = "1" ] && install_mainline_nginx "$platform"
        return 0
    fi

    if command -v yum >/dev/null 2>&1; then
        pkgs="ca-certificates"
        [ "$missing_curl" = "1" ] && pkgs="$pkgs curl"
        [ "$missing_openssl" = "1" ] && pkgs="$pkgs openssl"
        [ "$missing_socat" = "1" ] && pkgs="$pkgs socat"
        run_root_cmd yum install -y $pkgs
        [ "$missing_node" = "1" ] && install_nodejs_rpm 20
        [ "$missing_nginx" = "1" ] && install_mainline_nginx "$platform"
        return 0
    fi

    if command -v apk >/dev/null 2>&1; then
        pkgs="ca-certificates"
        [ "$missing_node" = "1" ] && pkgs="$pkgs nodejs npm"
        [ "$missing_curl" = "1" ] && pkgs="$pkgs curl"
        [ "$missing_openssl" = "1" ] && pkgs="$pkgs openssl"
        [ "$missing_socat" = "1" ] && pkgs="$pkgs socat"
        run_root_cmd apk add --no-cache $pkgs
        [ "$missing_nginx" = "1" ] && install_mainline_nginx "$platform"
        return 0
    fi

    if command -v zypper >/dev/null 2>&1; then
        pkgs="ca-certificates"
        if [ "$missing_node" = "1" ]; then
            node_pkg=""
            for candidate in nodejs24 nodejs22 nodejs20 nodejs18 nodejs; do
                if zypper_has_package "$candidate"; then
                    node_pkg="$candidate"
                    break
                fi
            done
            [ -n "$node_pkg" ] || {
                echo "Unable to find a Node.js 18+ package in zypper repositories." >&2
                exit 1
            }
            pkgs="$pkgs $node_pkg"
        fi
        [ "$missing_curl" = "1" ] && pkgs="$pkgs curl"
        [ "$missing_openssl" = "1" ] && pkgs="$pkgs openssl"
        [ "$missing_socat" = "1" ] && pkgs="$pkgs socat"
        run_root_cmd zypper --non-interactive install --no-recommends $pkgs
        [ "$missing_nginx" = "1" ] && install_mainline_nginx "$platform"
        return 0
    fi

    if command -v pacman >/dev/null 2>&1; then
        pkgs="ca-certificates"
        [ "$missing_node" = "1" ] && pkgs="$pkgs nodejs"
        [ "$missing_curl" = "1" ] && pkgs="$pkgs curl"
        [ "$missing_openssl" = "1" ] && pkgs="$pkgs openssl"
        [ "$missing_socat" = "1" ] && pkgs="$pkgs socat"
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
        30-acme-renew.sh)
            [ -n "$SCRIPT_DIR" ] && local_path="$SCRIPT_DIR/../docker/30-acme-renew.sh"
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

MISSING_NODE="0"
MISSING_CURL="0"
MISSING_NGINX="0"
MISSING_OPENSSL="0"
MISSING_SOCAT="0"

NODE_BIN="$(detect_node_bin || true)"
NODE_MAJOR="$(current_node_major "$NODE_BIN")"
[ -n "$NODE_BIN" ] || MISSING_NODE="1"
if [ -n "$NODE_BIN" ] && [ -n "$NODE_MAJOR" ] && [ "$NODE_MAJOR" -lt 18 ]; then
    echo "[JOIN] Detected Node.js $NODE_MAJOR; upgrading to Node.js 18+..." >&2
    MISSING_NODE="1"
fi
command -v curl >/dev/null 2>&1 || MISSING_CURL="1"
command -v nginx >/dev/null 2>&1 || MISSING_NGINX="1"
command -v openssl >/dev/null 2>&1 || MISSING_OPENSSL="1"
command -v socat >/dev/null 2>&1 || MISSING_SOCAT="1"

install_runtime_packages "$MISSING_NODE" "$MISSING_CURL" "$MISSING_NGINX" "$MISSING_OPENSSL" "$MISSING_SOCAT" "$PLATFORM"

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
command -v openssl >/dev/null 2>&1 || { echo "openssl is required after dependency installation" >&2; exit 1; }
command -v socat >/dev/null 2>&1 || { echo "socat is required after dependency installation" >&2; exit 1; }
if ! nginx_supports_early_hints; then
    echo "[JOIN] Warning: current nginx does not support early_hints; the agent apply script will disable that directive automatically." >&2
fi
NODE_BIN_PATH="$(command -v "$NODE_BIN" || true)"
[ -n "$NODE_BIN_PATH" ] || { echo "unable to resolve node executable path" >&2; exit 1; }
AGENT_CAPABILITIES="http_rules,local_acme,cert_install,l4"
ensure_nginx_stream_module >/dev/null 2>&1 || true
if nginx_supports_stream; then
    ensure_nginx_stream_support "$NODE_BIN_PATH"
else
    AGENT_CAPABILITIES="http_rules,local_acme,cert_install"
    echo "[JOIN] Warning: current nginx does not include stream support; disabling L4 capability for this agent." >&2
fi

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
DEFAULT_RENEW_SCRIPT="$BIN_DIR/nginx-reverse-emby-renew.sh"
GENERATOR_FILE="$RUNTIME_DIR/25-dynamic-reverse-proxy.sh"
RENEW_LOOP_FILE="$RUNTIME_DIR/30-acme-renew.sh"
TEMPLATE_FILE="$RUNTIME_DIR/default.conf.template"
DIRECT_NO_TLS_TEMPLATE_FILE="$RUNTIME_DIR/default.direct.no_tls.conf.template"
DIRECT_TLS_TEMPLATE_FILE="$RUNTIME_DIR/default.direct.tls.conf.template"

echo "[JOIN] Installing runtime assets to: $DATA_DIR"
copy_or_download_asset light-agent.js "$LIGHT_AGENT_FILE" 755
copy_or_download_asset light-agent-apply.sh "$DEFAULT_APPLY_SCRIPT" 755
copy_or_download_asset 25-dynamic-reverse-proxy.sh "$GENERATOR_FILE" 755
copy_or_download_asset 30-acme-renew.sh "$RENEW_LOOP_FILE" 755
copy_or_download_asset default.conf.template "$TEMPLATE_FILE" 644
copy_or_download_asset default.direct.no_tls.conf.template "$DIRECT_NO_TLS_TEMPLATE_FILE" 644
copy_or_download_asset default.direct.tls.conf.template "$DIRECT_TLS_TEMPLATE_FILE" 644
cp "$RENEW_LOOP_FILE" "$DEFAULT_RENEW_SCRIPT"
chmod 755 "$DEFAULT_RENEW_SCRIPT"

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
AGENT_CAPABILITIES=$(shell_quote "$AGENT_CAPABILITIES")
AGENT_HEARTBEAT_INTERVAL_MS=$(shell_quote "$INTERVAL_MS")
RULES_JSON=$(shell_quote "$RULES_FILE")
L4_RULES_JSON=$(shell_quote "$DATA_DIR/l4_rules.json")
MANAGED_CERTS_JSON=$(shell_quote "$DATA_DIR/managed_certificates.json")
MANAGED_CERTS_POLICY_JSON=$(shell_quote "$DATA_DIR/managed_certificates.policy.json")
AGENT_STATE_FILE=$(shell_quote "$STATE_FILE")
AGENT_HOME=$(shell_quote "$DATA_DIR")
AGENT_RUNTIME_DIR=$(shell_quote "$RUNTIME_DIR")
AGENT_GENERATOR_SCRIPT=$(shell_quote "$GENERATOR_FILE")
AGENT_DEFAULT_APPLY_COMMAND=$(shell_quote "$DEFAULT_APPLY_SCRIPT")
AGENT_DEFAULT_RENEW_COMMAND=$(shell_quote "$DEFAULT_RENEW_SCRIPT")
APPLY_COMMAND=$(shell_quote "$APPLY_COMMAND")
PROXY_DEPLOY_MODE=$(shell_quote "$DEPLOY_MODE")
AGENT_FOLLOW_MASTER_DEPLOY_MODE=$(shell_quote "$LOCAL_NODE")
NGINX_BIN=$(shell_quote "$NGINX_BIN_PATH")
DATA_ROOT=$(shell_quote "$DATA_DIR")
DIRECT_CERT_DIR=$(shell_quote "$DATA_DIR/certs")
ACME_HOME=$(shell_quote "$DATA_DIR/.acme.sh")
PANEL_MANAGED_CERTS_POLICY_JSON=$(shell_quote "$DATA_DIR/managed_certificates.policy.json")
PANEL_MANAGED_CERTS_SYNC_JSON=$(shell_quote "$DATA_DIR/managed_certificates.json")
ACME_RENEW_FOREGROUND='1'
EOF

PAYLOAD=$("$NODE_BIN" -e "const payload = {name: process.argv[1], agent_url: process.argv[2], agent_token: process.argv[3], version: process.argv[4], tags: process.argv[5] ? process.argv[5].split(',').map(v => v.trim()).filter(Boolean) : [], capabilities: process.argv[6] ? process.argv[6].split(',').map(v => v.trim()).filter(Boolean) : [], mode: 'pull', register_token: process.argv[7]}; process.stdout.write(JSON.stringify(payload));" "$AGENT_NAME" "$AGENT_URL" "$AGENT_TOKEN" "$AGENT_VERSION" "$AGENT_TAGS" "$AGENT_CAPABILITIES" "$REGISTER_TOKEN")

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
echo "[JOIN] Renew command: $DEFAULT_RENEW_SCRIPT"

if [ "$INSTALL_SYSTEMD" = "1" ]; then
    SUDO_BIN="$(require_root_or_sudo)" || {
        echo "Installing systemd services requires root or sudo" >&2
        exit 1
    }
    command -v systemctl >/dev/null 2>&1 || { echo "systemctl is required for --install-systemd" >&2; exit 1; }
    SERVICE_FILE="/etc/systemd/system/nginx-reverse-emby-agent.service"
    RENEW_SERVICE_FILE="/etc/systemd/system/nginx-reverse-emby-agent-renew.service"
    cat <<EOF | run_root_cmd tee "$SERVICE_FILE" >/dev/null
[Unit]
Description=Nginx Reverse Emby Lightweight Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$ENV_FILE
WorkingDirectory=$DATA_DIR
ExecStart=$NODE_BIN_PATH $LIGHT_AGENT_FILE
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
    cat <<EOF | run_root_cmd tee "$RENEW_SERVICE_FILE" >/dev/null
[Unit]
Description=Nginx Reverse Emby Agent ACME Renew Loop
After=network-online.target nginx-reverse-emby-agent.service
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$ENV_FILE
WorkingDirectory=$DATA_DIR
ExecStart=/bin/sh $DEFAULT_RENEW_SCRIPT
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
    run_root_cmd systemctl daemon-reload
    run_root_cmd systemctl enable --now nginx-reverse-emby-agent.service nginx-reverse-emby-agent-renew.service
    echo "[JOIN] Installed and started systemd services: nginx-reverse-emby-agent.service, nginx-reverse-emby-agent-renew.service"
elif [ "$INSTALL_LAUNCHD" = "1" ]; then
    [ "$PLATFORM" = "darwin" ] || { echo "--install-launchd is only supported on macOS" >&2; exit 1; }
    command -v launchctl >/dev/null 2>&1 || { echo "launchctl is required for --install-launchd" >&2; exit 1; }
    LAUNCHD_DIR="$HOME/Library/LaunchAgents"
    SERVICE_LABEL="com.nginx-reverse-emby.agent"
    SERVICE_FILE="$LAUNCHD_DIR/$SERVICE_LABEL.plist"
    RENEW_LABEL="com.nginx-reverse-emby.agent.renew"
    RENEW_SERVICE_FILE="$LAUNCHD_DIR/$RENEW_LABEL.plist"
    START_COMMAND="set -a && . $(shell_quote "$ENV_FILE") && set +a && exec $(shell_quote "$NODE_BIN_PATH") $(shell_quote "$LIGHT_AGENT_FILE")"
    RENEW_COMMAND="set -a && . $(shell_quote "$ENV_FILE") && set +a && exec /bin/sh $(shell_quote "$DEFAULT_RENEW_SCRIPT")"
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
    cat > "$RENEW_SERVICE_FILE" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$(xml_escape "$RENEW_LABEL")</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-lc</string>
    <string>$(xml_escape "$RENEW_COMMAND")</string>
  </array>
  <key>WorkingDirectory</key>
  <string>$(xml_escape "$DATA_DIR")</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>$(xml_escape "$DATA_DIR/renew.stdout.log")</string>
  <key>StandardErrorPath</key>
  <string>$(xml_escape "$DATA_DIR/renew.stderr.log")</string>
</dict>
</plist>
EOF
    launchctl unload "$SERVICE_FILE" >/dev/null 2>&1 || true
    launchctl unload "$RENEW_SERVICE_FILE" >/dev/null 2>&1 || true
    launchctl load -w "$SERVICE_FILE"
    launchctl load -w "$RENEW_SERVICE_FILE"
    echo "[JOIN] Installed and loaded launchd agents: $SERVICE_LABEL, $RENEW_LABEL"
else
    echo "[JOIN] Start commands:"
    echo "  set -a && . $ENV_FILE && set +a && $NODE_BIN_PATH $LIGHT_AGENT_FILE"
    echo "  set -a && . $ENV_FILE && set +a && /bin/sh $DEFAULT_RENEW_SCRIPT"
fi
