#!/bin/sh
set -eu

# ---- 可配置默认值（环境变量可覆盖，便于自动化 / 非交互部署） ----
repo_raw_base="${NRE_REPO_RAW_BASE:-https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main}"
install_dir="${NRE_INSTALL_DIR:-nginx-reverse-emby}"
image="${NRE_IMAGE:-sakullla/nginx-reverse-emby:latest}"
timezone="${NRE_TIMEZONE:-Asia/Shanghai}"
public_url="${NRE_PUBLIC_URL:-}"
trust_forwarded_headers="${NRE_TRUST_FORWARDED_HEADERS:-}"
docker_cli_version="${NRE_DOCKER_CLI_VERSION:-29.5.3}"
docker_compose_version="${NRE_DOCKER_COMPOSE_VERSION:-v5.1.4}"
panel_health_url="${NRE_PANEL_HEALTH_URL:-http://127.0.0.1:8080/panel-api/info}"
panel_api_base="${NRE_PANEL_API_BASE:-http://127.0.0.1:8080}"
panel_root_url="${NRE_PANEL_ROOT_URL:-http://127.0.0.1:8080/}"
public_panel_ready_attempts="${NRE_PUBLIC_PANEL_READY_ATTEMPTS:-36}"
panel_cert_wait_timeout="${NRE_PANEL_CERT_WAIT_TIMEOUT:-300}"
panel_cert_wait_interval="${NRE_PANEL_CERT_WAIT_INTERVAL:-5}"

opt_noninteractive=0
opt_yes=0

usage() {
    cat <<'EOF'
用法：deploy-compose.sh [选项]

nginx-reverse-emby 一键部署脚本：下载 compose、生成 token、按需安装 Docker，
交互时只需填写域名（可回车跳过）和可选 Cloudflare Token。

选项：
  --dir DIR            安装目录，默认 nginx-reverse-emby
  --image IMAGE        容器镜像，默认 sakullla/nginx-reverse-emby:latest
  --timezone TZ        面板时区，默认 Asia/Shanghai
  --public-url URL     已有 HTTPS 面板地址，例如 https://panel.example.com
  --cf-token TOKEN     直接提供 Cloudflare API Token（跳过交互输入并在线校验）
  --non-interactive    关闭所有交互提示，未提供的值回退到默认或环境变量
  --yes                跳过临时 HTTP 部署前的确认
  -h, --help           显示帮助

环境变量（同样可覆盖对应选项，便于 curl | sh 自动化）：
  NRE_REPO_RAW_BASE    docker-compose.yaml 下载地址前缀
  NRE_INSTALL_DIR / NRE_IMAGE / NRE_TIMEZONE / NRE_PUBLIC_URL
  NRE_TRUST_FORWARDED_HEADERS 显式覆盖代理头信任；反代模式默认 true，直连默认 false
  API_TOKEN            已有面板 token；不设置则自动生成
  MASTER_REGISTER_TOKEN 已有 Agent 注册 token；不设置则自动生成
  CF_TOKEN             Cloudflare API Token；设置后自动启用 DNS-01 并在线校验
  ACME_DNS_PROVIDER    设为 cf 以启用 Cloudflare DNS 验证
  NRE_PANEL_CERT_WAIT_TIMEOUT HTTPS 面板自代理证书等待秒数，默认 300
  NRE_NONINTERACTIVE   设为 1 等同 --non-interactive（用于 cron / CI）
  NO_COLOR             设为任意值关闭彩色输出
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --dir)
            [ "$#" -ge 2 ] || { echo "缺少 --dir 的值" >&2; exit 2; }
            install_dir="$2"
            shift 2
            ;;
        --image)
            [ "$#" -ge 2 ] || { echo "缺少 --image 的值" >&2; exit 2; }
            image="$2"
            shift 2
            ;;
        --timezone)
            [ "$#" -ge 2 ] || { echo "缺少 --timezone 的值" >&2; exit 2; }
            timezone="$2"
            shift 2
            ;;
        --public-url)
            [ "$#" -ge 2 ] || { echo "缺少 --public-url 的值" >&2; exit 2; }
            public_url="$2"
            shift 2
            ;;
        --cf-token)
            [ "$#" -ge 2 ] || { echo "缺少 --cf-token 的值" >&2; exit 2; }
            CF_TOKEN="$2"
            shift 2
            ;;
        --non-interactive)
            opt_noninteractive=1
            shift
            ;;
        --yes)
            opt_yes=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "未知选项：$1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

# ---- 输出样式（仅在终端启用，便于复制 / 阅读日志） ----
if [ -t 2 ] && [ -z "${NO_COLOR:-}" ]; then
    C_CYAN=$(printf '\033[36m')
    C_YELLOW=$(printf '\033[33m')
    C_RED=$(printf '\033[31m')
    C_BOLD=$(printf '\033[1m')
    C_RESET=$(printf '\033[0m')
else
    C_CYAN=""
    C_YELLOW=""
    C_RED=""
    C_BOLD=""
    C_RESET=""
fi

say() {
    printf '\n%s[NRE]%s %s\n' "$C_CYAN" "$C_RESET" "$*" >&2
}

warn() {
    printf '\n%s[注意]%s %s\n' "$C_YELLOW" "$C_RESET" "$*" >&2
}

err() {
    printf '\n%s[错误]%s %s\n' "$C_RED" "$C_RESET" "$*" >&2
}

# 交互能力判定：优先 /dev/tty，以便 `curl ... | sh`（脚本本身经 stdin 喂入）
# 仍能在用户的终端上弹出提问。
if [ -c /dev/tty ] && [ "$opt_noninteractive" -eq 0 ] && [ "${NRE_NONINTERACTIVE:-0}" != "1" ]; then
    interactive=1
else
    interactive=0
fi

# 从控制台读一行。只从 /dev/tty 读取，绝不读 stdin——这样 `curl ... | sh`
# （脚本本身经 stdin 喂入）时也不会把脚本后续行当成回答吞掉。无可用 /dev/tty
# 时直接采用默认值（调用方已用 interactive 开关保证提问仅在 /dev/tty 可用时发生）。
ask() {
    _prompt="$1"
    _default="${2:-}"
    if [ -n "$_default" ]; then
        printf '%s [%s]: ' "$_prompt" "$_default" >&2
    else
        printf '%s: ' "$_prompt" >&2
    fi
    _answer=""
    if [ -c /dev/tty ]; then
        IFS= read -r _answer </dev/tty 2>/dev/null || _answer=""
    fi
    if [ -z "$_answer" ]; then
        printf '%s' "$_default"
    else
        printf '%s' "$_answer"
    fi
}

# 隐藏回显读取敏感输入（如 token）。同样只从 /dev/tty 读取；无 stty 时回显读取。
read_secret() {
    _prompt="$1"
    _default="${2:-}"
    printf '%s: ' "$_prompt" >&2
    _answer=""
    if [ -c /dev/tty ]; then
        if command -v stty >/dev/null 2>&1; then
            stty -echo </dev/tty 2>/dev/null || true
            IFS= read -r _answer </dev/tty 2>/dev/null || _answer=""
            stty echo </dev/tty 2>/dev/null || true
            printf '\n' >&2
        else
            IFS= read -r _answer </dev/tty 2>/dev/null || _answer=""
        fi
    fi
    [ -z "$_answer" ] && _answer="$_default"
    printf '%s' "$_answer"
}

ask_yes_no() {
    _prompt="$1"
    _default="${2:-n}"
    while :; do
        _answer="$(ask "$_prompt (y/n)" "$_default")"
        case "$(printf '%s' "$_answer" | tr '[:upper:]' '[:lower:]')" in
            y|yes|是|好) return 0 ;;
            n|no|否|不) return 1 ;;
            *) echo "请输入 y 或 n。" >&2 ;;
        esac
    done
}

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "缺少命令：$1" >&2
        exit 1
    fi
}

run_as_root() {
    if [ "$(id -u)" -eq 0 ]; then
        "$@"
        return
    fi
    if command -v sudo >/dev/null 2>&1; then
        sudo "$@"
        return
    fi
    err "需要 root 权限，但当前系统没有 sudo。请切换 root 后重试。"
    exit 1
}

install_docker_compose() {
    say "检测到 Docker 或 Docker Compose 不完整，准备自动安装 Docker Compose。"
    echo "脚本会安装 Docker Engine 与 Compose 插件，并可能修改系统软件源。" >&2

    if [ -S /var/run/docker.sock ] && ! command -v docker >/dev/null 2>&1; then
        say "检测到本机已有 Docker Socket，优先安装 Docker CLI 与 Compose 插件"
        install_static_docker_client
        return
    fi

    if command -v apt-get >/dev/null 2>&1 || command -v dnf >/dev/null 2>&1 || command -v yum >/dev/null 2>&1; then
        say "使用官方 Docker 安装脚本安装 Docker 与 Compose 插件"
        ensure_packages curl
        ensure_ca_certificates
        get_docker_sh="${TMPDIR:-/tmp}/nre-get-docker.$$"
        if curl -fsSL https://get.docker.com -o "$get_docker_sh" && run_as_root sh "$get_docker_sh"; then
            rm -f "$get_docker_sh"
        else
            rm -f "$get_docker_sh"
            # 官方脚本失败时：RPM 系优先用包管理器补装客户端，其余回退到静态二进制。
            if command -v dnf >/dev/null 2>&1; then
                warn "官方安装脚本失败，改用 dnf 安装 Docker 客户端与 Compose 插件。"
                run_as_root dnf install -y docker-cli docker-compose-plugin || install_static_docker_client
            elif command -v yum >/dev/null 2>&1; then
                warn "官方安装脚本失败，改用 yum 安装 Docker 客户端与 Compose 插件。"
                run_as_root yum install -y docker-cli docker-compose-plugin || install_static_docker_client
            else
                warn "官方安装脚本失败，改用静态 Docker CLI 与 Compose 插件后备安装。"
                install_static_docker_client
            fi
        fi
    elif command -v apk >/dev/null 2>&1; then
        say "使用 apk 安装 Docker CLI 与 Compose 插件"
        run_as_root apk add --no-cache docker-cli docker-cli-compose || install_static_docker_client
    else
        warn "未发现常见软件包管理器，改用静态 Docker CLI 与 Compose 插件后备安装。"
        install_static_docker_client
    fi

    if command -v systemctl >/dev/null 2>&1; then
        run_as_root systemctl enable --now docker || true
    fi
}

install_static_docker_client() {
    ensure_packages curl tar
    ensure_ca_certificates
    arch="$(uname -m)"
    case "$arch" in
        x86_64|amd64) docker_arch="x86_64"; compose_arch="x86_64" ;;
        aarch64|arm64) docker_arch="aarch64"; compose_arch="aarch64" ;;
        armv7l) docker_arch="armhf"; compose_arch="armv7" ;;
        *) err "暂不支持自动安装当前 CPU 架构的 Docker CLI：$arch"; exit 1 ;;
    esac

    tmp_dir="${TMPDIR:-/tmp}/nre-docker-client.$$"
    mkdir -p "$tmp_dir"
    _static_tmp="$tmp_dir"
    _cleanup_static_tmp() { rm -rf "$_static_tmp"; }
    # shellcheck disable=SC2064
    trap "_cleanup_static_tmp" EXIT

    cli_url="https://download.docker.com/linux/static/stable/${docker_arch}/docker-${docker_cli_version}.tgz"
    compose_url="https://github.com/docker/compose/releases/download/${docker_compose_version}/docker-compose-linux-${compose_arch}"

    say "下载 Docker CLI：${cli_url}"
    curl -fsSL "$cli_url" -o "$tmp_dir/docker.tgz"
    tar -xzf "$tmp_dir/docker.tgz" -C "$tmp_dir"
    run_as_root install -m 0755 "$tmp_dir/docker/docker" /usr/local/bin/docker

    say "下载 Docker Compose 插件：${compose_url}"
    run_as_root mkdir -p /usr/local/lib/docker/cli-plugins
    curl -fsSL "$compose_url" -o "$tmp_dir/docker-compose"
    run_as_root install -m 0755 "$tmp_dir/docker-compose" /usr/local/lib/docker/cli-plugins/docker-compose

    trap - EXIT
    rm -rf "$tmp_dir"
}

install_packages() {
    [ "$#" -gt 0 ] || return 0

    say "自动安装缺失依赖：$*"
    if command -v apt-get >/dev/null 2>&1; then
        apt_log="${TMPDIR:-/tmp}/nre-apt-install.$$"
        if run_as_root env DEBIAN_FRONTEND=noninteractive apt-get update -qq >"$apt_log" 2>&1 &&
            run_as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends -o=Dpkg::Use-Pty=0 "$@" >>"$apt_log" 2>&1; then
            rm -f "$apt_log"
        else
            cat "$apt_log" >&2
            rm -f "$apt_log"
            exit 1
        fi
    elif command -v dnf >/dev/null 2>&1; then
        run_as_root dnf install -y "$@"
    elif command -v yum >/dev/null 2>&1; then
        run_as_root yum install -y "$@"
    elif command -v apk >/dev/null 2>&1; then
        run_as_root apk add --no-cache "$@"
    else
        echo "当前系统缺少受支持的软件包管理器，无法自动安装依赖：$*" >&2
        exit 1
    fi
}

ensure_packages() {
    missing=""
    for cmd in "$@"; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            pkg="$cmd"
            case "$cmd" in
                mkdir|cut|tr|od|install) pkg="coreutils" ;;
            esac
            case " ${missing} " in
                *" ${pkg} "*) ;;
                *) missing="${missing} ${pkg}" ;;
            esac
        fi
    done
    [ -n "$missing" ] || return 0

    # shellcheck disable=SC2086
    install_packages $missing
}

ensure_ca_certificates() {
    if [ -f /etc/ssl/certs/ca-certificates.crt ] || [ -f /etc/pki/tls/certs/ca-bundle.crt ]; then
        return 0
    fi

    install_packages ca-certificates
    if command -v update-ca-certificates >/dev/null 2>&1; then
        run_as_root update-ca-certificates >/dev/null 2>&1 || true
    fi
}

docker_cmd() {
    if docker version >/dev/null 2>&1; then
        echo "docker"
        return
    fi
    if command -v sudo >/dev/null 2>&1 && sudo docker version >/dev/null 2>&1; then
        echo "sudo docker"
        return
    fi
    echo ""
}

compose_cmd() {
    docker_bin="$(docker_cmd)"
    # 优先使用官方插件形式：docker compose。
    if [ -n "$docker_bin" ] && $docker_bin compose version >/dev/null 2>&1; then
        echo "$docker_bin compose"
        return
    fi
    if command -v docker-compose >/dev/null 2>&1; then
        echo "docker-compose"
        return
    fi
    echo ""
}

ensure_docker_compose() {
    if ! command -v docker >/dev/null 2>&1 || [ -z "$(compose_cmd)" ]; then
        install_docker_compose >&2
    fi

    compose="$(compose_cmd)"
    if [ -z "$compose" ]; then
        err "Docker Compose 仍不可用，请检查 Docker 安装状态。"
        exit 1
    fi
    printf '%s' "$compose"
}

random_hex() {
    bytes="$1"
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex "$bytes"
        return
    fi
    if command -v od >/dev/null 2>&1; then
        od -An -N"$bytes" -tx1 /dev/urandom | tr -d ' \n'
        return
    fi
    err "需要 openssl 或 od 生成随机 token。"
    exit 1
}

write_env_value() {
    key="$1"
    value="$2"
    file="$3"
    if [ -f "$file" ] && grep -q "^${key}=" "$file"; then
        tmp="${file}.tmp.$$"
        grep -v "^${key}=" "$file" > "$tmp" || true
        mv "$tmp" "$file"
    fi
    printf '%s=%s\n' "$key" "$value" >> "$file"
}

configure_forwarded_headers_trust() {
    file="$1"
    value="$trust_forwarded_headers"
    if [ -z "$value" ]; then
        if [ -n "${public_url:-}" ] || [ -n "${domain:-}" ]; then
            value="true"
        else
            value="false"
        fi
    fi
    write_env_value "NRE_TRUST_FORWARDED_HEADERS" "$value" "$file"
}

delete_env_value() {
    key="$1"
    file="$2"
    if [ ! -f "$file" ] || ! grep -q "^${key}=" "$file"; then
        return 1
    fi
    tmp="${file}.tmp.$$"
    awk -v key="$key" 'BEGIN { FS="=" } $1 != key { print }' "$file" > "$tmp"
    mv "$tmp" "$file"
    return 0
}

env_value() {
    key="$1"
    file="$2"
    grep "^${key}=" "$file" 2>/dev/null | tail -n 1 | cut -d= -f2-
}

wait_panel_ready() {
    token="$1"
    for _ in 1 2 3 4 5 6 7 8 9 10 11 12; do
        if curl -fsS -H "X-Panel-Token: ${token}" "$panel_health_url" >/dev/null 2>&1 && \
            curl -fsS "$panel_root_url" >/dev/null 2>&1; then
            return 0
        fi
        sleep 5
    done
    return 1
}

json_escape() {
    printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

json_string_field() {
    printf '%s' "$1" | sed 's/,"/\
"/g' | sed -n 's/.*"'"$2"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

json_number_field() {
    printf '%s' "$1" | sed 's/,"/\
"/g' | sed -n 's/.*"'"$2"'"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' | head -n 1
}

panel_certificate_objects() {
    body="$1"
    printf '%s' "$body" | awk '
        {
            text = text $0
        }
        END {
            key = "\"certificates\""
            start = index(text, key)
            if (start == 0) {
                exit
            }
            text = substr(text, start + length(key))
            start = index(text, "[")
            if (start == 0) {
                exit
            }
            text = substr(text, start + 1)
            for (i = 1; i <= length(text); i++) {
                ch = substr(text, i, 1)
                if (in_object) {
                    object = object ch
                }
                if (in_string) {
                    if (escaped) {
                        escaped = 0
                    } else if (ch == "\\") {
                        escaped = 1
                    } else if (ch == "\"") {
                        in_string = 0
                    }
                    continue
                }
                if (ch == "\"") {
                    in_string = 1
                    continue
                }
                if (ch == "{") {
                    if (!in_object) {
                        in_object = 1
                        object = "{"
                        depth = 1
                    } else {
                        depth++
                    }
                    continue
                }
                if (ch == "}") {
                    if (in_object) {
                        depth--
                        if (depth == 0) {
                            print object
                            in_object = 0
                            object = ""
                        }
                    }
                    continue
                }
                if (!in_object && ch == "]") {
                    exit
                }
            }
        }
    '
}

panel_certificate_object() {
    body="$1"
    cert_id="$2"
    domain="$3"
    panel_certificate_objects "$body" | while IFS= read -r object; do
        if [ -n "$cert_id" ]; then
            object_id="$(json_number_field "$object" id)"
            [ "$object_id" = "$cert_id" ] || continue
        fi
        if [ -n "$domain" ]; then
            object_domain="$(json_string_field "$object" domain)"
            [ "$object_domain" = "$domain" ] || continue
        fi
        printf '%s' "$object"
        break
    done
}

fetch_panel_certificates() {
    token="$1"
    curl -sS \
        -H "X-Panel-Token: ${token}" \
        "${panel_api_base}/panel-api/agents/local/certificates"
}

create_panel_self_proxy_certificate() {
    token="$1"
    domain="$2"
    response_file="${TMPDIR:-/tmp}/nre-panel-cert-response.$$"
    error_file="${TMPDIR:-/tmp}/nre-panel-cert-error.$$"
    last_panel_self_proxy_error=""
    panel_self_proxy_certificate_id=""

    certificates="$(fetch_panel_certificates "$token" 2>/dev/null || true)"
    existing="$(panel_certificate_object "$certificates" "" "$domain")"
    existing_id="$(json_number_field "$existing" id)"
    if [ -n "$existing_id" ]; then
        say "发现已有面板自代理证书：${domain}（ID ${existing_id}），将复用"
        panel_self_proxy_certificate_id="$existing_id"
        return 0
    fi

    payload="$(printf '{"domain":"%s","enabled":true,"scope":"domain","issuer_mode":"master_cf_dns","target_agent_ids":["local"],"tags":["panel","bootstrap"],"usage":"https","certificate_type":"acme"}' "$(json_escape "$domain")")"
    say "正在创建面板自代理证书：${domain}"
    http_status="$(curl -sS \
        -o "$response_file" \
        -w "%{http_code}" \
        -H "Content-Type: application/json" \
        -H "X-Panel-Token: ${token}" \
        -d "$payload" \
        "${panel_api_base}/panel-api/agents/local/certificates" 2>"$error_file" || true)"
    response_body="$(cat "$response_file" 2>/dev/null || true)"
    curl_error="$(sed 's/[[:cntrl:]]/ /g' "$error_file" 2>/dev/null | cut -c 1-300 || true)"
    rm -f "$response_file" "$error_file"

    case "$http_status" in
        2??)
            cert_id="$(json_number_field "$response_body" id)"
            if [ -z "$cert_id" ]; then
                certificates="$(fetch_panel_certificates "$token" 2>/dev/null || true)"
                created="$(panel_certificate_object "$certificates" "" "$domain")"
                cert_id="$(json_number_field "$created" id)"
            fi
            if [ -n "$cert_id" ]; then
                panel_self_proxy_certificate_id="$cert_id"
                return 0
            fi
            last_panel_self_proxy_error="证书已创建，但未能从接口响应中读取证书 ID"
            return 1
            ;;
    esac

    response_summary="$(printf '%s' "$response_body" | sed 's/[[:cntrl:]]/ /g' | cut -c 1-500)"
    last_panel_self_proxy_error="HTTP ${http_status:-000}"
    if [ -n "$response_summary" ]; then
        last_panel_self_proxy_error="${last_panel_self_proxy_error}: ${response_summary}"
    elif [ -n "$curl_error" ]; then
        last_panel_self_proxy_error="${last_panel_self_proxy_error}: ${curl_error}"
    fi
    return 1
}

issue_panel_self_proxy_certificate() {
    token="$1"
    cert_id="$2"
    response_file="${TMPDIR:-/tmp}/nre-panel-cert-issue-response.$$"
    error_file="${TMPDIR:-/tmp}/nre-panel-cert-issue-error.$$"
    last_panel_self_proxy_error=""

    http_status="$(curl -sS \
        -X POST \
        -o "$response_file" \
        -w "%{http_code}" \
        -H "X-Panel-Token: ${token}" \
        "${panel_api_base}/panel-api/agents/local/certificates/${cert_id}/issue" 2>"$error_file" || true)"
    case "$http_status" in
        2??)
            rm -f "$response_file" "$error_file"
            return 0
            ;;
    esac

    response_body="$(sed 's/[[:cntrl:]]/ /g' "$response_file" 2>/dev/null | cut -c 1-500 || true)"
    curl_error="$(sed 's/[[:cntrl:]]/ /g' "$error_file" 2>/dev/null | cut -c 1-300 || true)"
    rm -f "$response_file" "$error_file"
    last_panel_self_proxy_error="HTTP ${http_status:-000}"
    if [ -n "$response_body" ]; then
        last_panel_self_proxy_error="${last_panel_self_proxy_error}: ${response_body}"
    elif [ -n "$curl_error" ]; then
        last_panel_self_proxy_error="${last_panel_self_proxy_error}: ${curl_error}"
    fi
    return 1
}

read_panel_certificate_state() {
    token="$1"
    cert_id="$2"
    domain="$3"
    certificates="$(fetch_panel_certificates "$token" 2>/dev/null || true)"
    object="$(panel_certificate_object "$certificates" "$cert_id" "$domain")"
    panel_certificate_status="$(json_string_field "$object" status)"
    panel_certificate_last_error="$(json_string_field "$object" last_error)"
    if [ -z "$object" ]; then
        panel_certificate_status="not_found"
        panel_certificate_last_error=""
    fi
}

wait_panel_self_proxy_certificate() {
    token="$1"
    domain="$2"
    cert_id="$3"
    timeout_seconds="${4:-300}"
    interval_seconds="${panel_cert_wait_interval:-5}"
    start_time="$(date +%s)"
    next_notice=30
    last_status=""
    last_error=""

    say "等待面板自代理证书签发完成（最多 ${timeout_seconds}s）：${domain}"
    while :; do
        read_panel_certificate_state "$token" "$cert_id" "$domain"
        last_status="$panel_certificate_status"
        last_error="$panel_certificate_last_error"
        if [ "$last_status" = "active" ] && [ -z "$last_error" ]; then
            say "面板自代理证书已签发完成：${domain}"
            return 0
        fi
        if [ "$last_status" = "error" ] || [ -n "$last_error" ]; then
            last_panel_self_proxy_error="证书签发失败，状态 ${last_status:-unknown}"
            if [ -n "$last_error" ]; then
                last_panel_self_proxy_error="${last_panel_self_proxy_error}: ${last_error}"
            fi
            return 1
        fi

        now="$(date +%s)"
        elapsed=$((now - start_time))
        if [ "$elapsed" -ge "$timeout_seconds" ]; then
            last_panel_self_proxy_error="等待证书签发超时（${timeout_seconds}s），最后状态 ${last_status:-unknown}"
            return 1
        fi
        if [ "$elapsed" -ge "$next_notice" ]; then
            say "证书仍在等待中，当前状态：${last_status:-unknown}（已等待 ${elapsed}s）"
            next_notice=$((next_notice + 30))
        fi
        sleep "$interval_seconds"
    done
}

prepare_panel_self_proxy_certificate() {
    token="$1"
    domain="$2"
    timeout_seconds="${3:-300}"

    create_panel_self_proxy_certificate "$token" "$domain" || return 1
    cert_id="$panel_self_proxy_certificate_id"
    read_panel_certificate_state "$token" "$cert_id" "$domain"
    if [ "$panel_certificate_status" = "issuing" ] && [ -z "$panel_certificate_last_error" ]; then
        say "面板自代理证书已在签发中：${domain}"
    elif [ "$panel_certificate_status" != "active" ] || [ -n "$panel_certificate_last_error" ]; then
        say "正在触发面板自代理证书签发：${domain}"
        issue_panel_self_proxy_certificate "$token" "$cert_id" || return 1
    fi
    wait_panel_self_proxy_certificate "$token" "$domain" "$cert_id" "$timeout_seconds"
}

create_panel_self_proxy() {
    token="$1"
    domain="$2"
    scheme="${3:-https}"
    defer_apply="${4:-0}"
    frontend="${scheme}://${domain}"
    payload="$(printf '{"frontend_url":"%s","backends":[{"url":"http://127.0.0.1:8080"}],"tags":["panel","bootstrap"]}' "$(json_escape "$frontend")")"
    response_file="${TMPDIR:-/tmp}/nre-panel-api-response.$$"
    error_file="${TMPDIR:-/tmp}/nre-panel-api-error.$$"
    last_panel_self_proxy_error=""

    say "正在创建面板自代理规则：${frontend} -> http://127.0.0.1:8080"
    http_status="$(curl -sS \
        -o "$response_file" \
        -w "%{http_code}" \
        -H "Content-Type: application/json" \
        -H "X-Panel-Token: ${token}" \
        -d "$payload" \
        "${panel_api_base}/panel-api/agents/local/rules" 2>"$error_file" || true)"
    case "$http_status" in
        2??)
        rm -f "$response_file" "$error_file"
        if [ "$defer_apply" -ne 1 ]; then
            curl -fsS -X POST -H "X-Panel-Token: ${token}" "${panel_api_base}/panel-api/agents/local/apply" >/dev/null 2>&1 || true
        fi
        return 0
        ;;
    esac

    response_body="$(sed 's/[[:cntrl:]]/ /g' "$response_file" 2>/dev/null | cut -c 1-500 || true)"
    curl_error="$(sed 's/[[:cntrl:]]/ /g' "$error_file" 2>/dev/null | cut -c 1-300 || true)"
    rm -f "$response_file" "$error_file"
    if printf '%s' "$response_body" | grep -q "frontend_url conflicts with existing rule"; then
        last_panel_self_proxy_error="已有规则占用 ${frontend}，将复用现有规则继续验证"
        if [ "$defer_apply" -ne 1 ]; then
            curl -fsS -X POST -H "X-Panel-Token: ${token}" "${panel_api_base}/panel-api/agents/local/apply" >/dev/null 2>&1 || true
        fi
        return 0
    fi
    last_panel_self_proxy_error="HTTP ${http_status:-000}"
    if [ -n "$response_body" ]; then
        last_panel_self_proxy_error="${last_panel_self_proxy_error}: ${response_body}"
    elif [ -n "$curl_error" ]; then
        last_panel_self_proxy_error="${last_panel_self_proxy_error}: ${curl_error}"
    fi
    return 1
}

is_panel_html() {
    grep -q '<div id="app"\|Nginx Reverse Emby Panel\|window.__NRE_PANEL_BASE__'
}

wait_public_panel_ready() {
    token="$1"
    url="$2"
    retry_apply="${3:-1}"
    attempt=1
    while [ "$attempt" -le "$public_panel_ready_attempts" ]; do
        if curl -fsSL --max-time 10 "$url" 2>/dev/null | is_panel_html; then
            return 0
        fi
        if [ "$retry_apply" -eq 1 ]; then
            curl -fsS -X POST -H "X-Panel-Token: ${token}" "${panel_api_base}/panel-api/agents/local/apply" >/dev/null 2>&1 || true
        fi
        attempt=$((attempt + 1))
        sleep 5
    done
    return 1
}

detect_public_ip() {
    _ip=""
    if command -v curl >/dev/null 2>&1; then
        _ip="$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)"
        if [ -z "$_ip" ]; then
            _ip="$(curl -fsS --max-time 5 https://ifconfig.me 2>/dev/null || true)"
        fi
    fi
    if [ -n "$_ip" ]; then
        printf '%s' "$_ip"
    else
        printf '<服务器IP>'
    fi
}

# 简易 JSON "字段":"取值" 提取（不依赖 jq，best-effort）。
cf_field() {
    printf '%s' "$1" | sed -n 's/.*"'"$2"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

print_cf_guide() {
    cat >&2 <<'EOF'
Cloudflare API Token（可选，推荐）：
  1) 打开 https://dash.cloudflare.com/profile/api-tokens
  2) Create Token → Create Custom Token（自定义令牌）
  3) 权限（三项都要有）：
       - 区域 / 区域 / 读取    (Zone / Zone / Read)
       - 区域 / DNS / 读取     (Zone / DNS / Read)
       - 区域 / DNS / 编辑     (Zone / DNS / Edit)
  4) 区域资源：包含 - 特定区域 - 你的域名
  5) 不要勾选客户端 IP 限制；不要使用账号级 Global API Key
  6) Continue to summary → Create Token → 复制 Token（只显示一次）

注意：Cloudflare 自带的 Edit zone DNS 模板缺少「区域/DNS/读取」，
      不能直接用，请按上面三项权限创建 Custom Token。
直接回车可跳过，脚本会改用 HTTP-01（需 80/443 公网可达）。
EOF
}

# 调用 Cloudflare API 在线校验 Token 是否有效（best-effort，网络不可达时跳过）。
# 返回 0 = 有效或无法判定；返回 1 = 明确无效。
verify_cf_token() {
    _vt="$1"
    if ! command -v curl >/dev/null 2>&1; then
        return 0
    fi
    _resp="$(curl -sS --max-time 15 \
        -H "Authorization: Bearer ${_vt}" \
        "https://api.cloudflare.com/client/v4/user/tokens/verify" 2>/dev/null || true)"
    if [ -z "$_resp" ]; then
        warn "无法连接 Cloudflare API，已跳过 Token 在线校验。"
        return 0
    fi
    _status="$(cf_field "$_resp" status)"
    if [ "$_status" = "active" ]; then
        say "Cloudflare Token 校验通过"
        return 0
    fi
    if printf '%s' "$_resp" | grep -q '"success":[[:space:]]*true'; then
        say "Cloudflare Token 校验通过"
        return 0
    fi
    _msg="$(cf_field "$_resp" message)"
    warn "Cloudflare Token 校验失败：${_msg:-token 无效或已过期}（常见原因：粘贴了 Global API Key）"
    return 1
}

# 收集 Cloudflare Token。优先级：--cf-token / CF_TOKEN 环境变量 > .env 已有 > 交互输入。
# 交互时直接粘贴，回车跳过；不再单独问「是否填写」。
collect_cf_token() {
    _cf="${CF_TOKEN:-$(env_value CF_TOKEN "$env_file")}"
    if [ -n "$_cf" ]; then
        say "复用已有 Cloudflare Token"
        verify_cf_token "$_cf" || warn "已有 Token 校验未通过，仍会写入，请稍后核对。"
        printf '%s' "$_cf"
        return
    fi
    if [ "$interactive" -eq 0 ]; then
        printf ''
        return
    fi
    print_cf_guide
    _attempt=0
    _entered=""
    while [ "$_attempt" -lt 3 ]; do
        _attempt=$((_attempt + 1))
        if [ "$_attempt" -eq 1 ]; then
            _entered="$(read_secret "粘贴 Cloudflare API Token（回车跳过）" "")"
        else
            _entered="$(read_secret "Token 无效，请重新粘贴（回车跳过）" "")"
        fi
        _entered="$(printf '%s' "$_entered" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
        if [ -z "$_entered" ]; then
            printf ''
            return
        fi
        case "$_entered" in
            *@*) warn "输入中包含 @，可能粘贴了邮箱 + Global API Key。应使用 API Token。" ;;
        esac
        if verify_cf_token "$_entered"; then
            printf '%s' "$_entered"
            return
        fi
    done
    warn "Token 仍未通过校验，将跳过 Cloudflare，改用 HTTP-01。"
    printf ''
}

ensure_packages mkdir grep sed cut tr curl

say "nginx-reverse-emby 一键部署"
echo "目录 ${install_dir} · 镜像 ${image} · 时区 ${timezone}" >&2
if [ "$interactive" -eq 0 ]; then
    echo "非交互模式：未提供的配置使用默认值或环境变量" >&2
fi

compose="$(ensure_docker_compose)"

mkdir -p "$install_dir"
cd "$install_dir"
mkdir -p data

if [ ! -f docker-compose.yaml ]; then
    say "下载 docker-compose.yaml"
    curl -fsSL "${repo_raw_base}/docker-compose.yaml" -o docker-compose.yaml
else
    say "复用已有 docker-compose.yaml"
fi

env_file=".env"
touch "$env_file"

api_token="${API_TOKEN:-$(env_value API_TOKEN "$env_file")}"
register_token="${MASTER_REGISTER_TOKEN:-$(env_value MASTER_REGISTER_TOKEN "$env_file")}"
[ -n "$api_token" ] || api_token="$(random_hex 32)"
[ -n "$register_token" ] || register_token="$(random_hex 32)"

write_env_value "API_TOKEN" "$api_token" "$env_file"
write_env_value "MASTER_REGISTER_TOKEN" "$register_token" "$env_file"
write_env_value "NRE_TIMEZONE" "$timezone" "$env_file"
write_env_value "NRE_IMAGE" "$image" "$env_file"

domain=""
panel_path=""
public_ip=""
cf_token=""
cf_enabled=0
panel_self_proxy_scheme=""
force_recreate=0

normalize_domain() {
    printf '%s' "$1" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//; s#^[a-zA-Z][a-zA-Z0-9+.-]*://##; s#/.*##; s#:.*##'
}

apply_domain_config() {
    _d="$1"
    [ -n "$_d" ] || return 1
    write_env_value "PANEL_BACKEND_HOST" "127.0.0.1" "$env_file"
    write_env_value "NRE_PUBLIC_URL" "https://${_d}" "$env_file"
    if delete_env_value "NRE_PANEL_PUBLIC_PATH" "$env_file"; then
        force_recreate=1
    fi
}

# 启用面板域名自代理（HTTPS 优先，失败回退 HTTP）。成功时设置 panel_self_proxy_scheme。
fallback_http_self_proxy() {
    _token="$1"
    _domain="$2"
    _reason="${3:-HTTPS 未就绪}"

    warn "${_reason}，尝试创建 HTTP 后备规则"
    if create_panel_self_proxy "$_token" "$_domain" "http" "0"; then
        panel_self_proxy_scheme="http"
        warn "已创建 HTTP 后备：http://${_domain}/ （修好证书/DNS 后请改回 HTTPS）"
        wait_public_panel_ready "$_token" "http://${_domain}/" "1" >/dev/null 2>&1 || true
        return 0
    fi
    [ -n "${last_panel_self_proxy_error:-}" ] && warn "HTTP 后备规则失败：${last_panel_self_proxy_error}"
    warn "请登录面板后手动添加：前端 http://${_domain}，后端 http://127.0.0.1:8080，节点 local"
    return 1
}

setup_domain_self_proxy() {
    _token="$1"
    _domain="$2"
    _use_cf="$3"

    if [ "$_use_cf" -eq 1 ]; then
        if ! prepare_panel_self_proxy_certificate "$_token" "$_domain" "$panel_cert_wait_timeout"; then
            _reason="HTTPS 证书准备失败"
            [ -n "${last_panel_self_proxy_error:-}" ] && _reason="${_reason}：${last_panel_self_proxy_error}"
            fallback_http_self_proxy "$_token" "$_domain" "$_reason"
            return
        fi
    fi

    if create_panel_self_proxy "$_token" "$_domain" "https" "0"; then
        panel_self_proxy_scheme="https"
        if [ "$_use_cf" -eq 1 ]; then
            say "HTTPS 面板自代理已创建"
        else
            say "面板自代理已创建，证书签发可能需要 1-3 分钟"
        fi
        if wait_public_panel_ready "$_token" "https://${_domain}/" "1"; then
            say "HTTPS 面板已可访问：https://${_domain}/"
        else
            warn "暂未确认 https://${_domain}/ 可访问，可稍后刷新或查看 ${compose} logs -f"
        fi
        return
    fi

    _reason="HTTPS 自代理失败"
    [ -n "${last_panel_self_proxy_error:-}" ] && _reason="${_reason}：${last_panel_self_proxy_error}"
    fallback_http_self_proxy "$_token" "$_domain" "${_reason}（常见原因：域名/DNS/CF Token/ACME）"
}

if [ -n "$public_url" ]; then
    write_env_value "NRE_PUBLIC_URL" "$public_url" "$env_file"
    if delete_env_value "NRE_PANEL_PUBLIC_PATH" "$env_file"; then
        force_recreate=1
    fi
    say "使用已有面板地址：${public_url}"
    if [ -n "${CF_TOKEN:-}" ]; then
        verify_cf_token "$CF_TOKEN" || warn "CF_TOKEN 校验未通过，仍会写入"
        write_env_value "ACME_DNS_PROVIDER" "cf" "$env_file"
        write_env_value "CF_TOKEN" "$CF_TOKEN" "$env_file"
        cf_enabled=1
    fi
elif [ "$interactive" -eq 1 ]; then
    # 一步输入域名：回车 = 临时 HTTP，无需先问「是否有域名」
    _domain_input="$(ask "面板域名（DNS 已指向本机；直接回车=临时 HTTP）" "")"
    domain="$(normalize_domain "$_domain_input")"
    # 去掉明显无效输入（非法字符 / 无点号主机名），避免误当成域名
    case "$domain" in
        ""|*[!A-Za-z0-9.-]*|.|*.) domain="" ;;
        .*) domain="" ;;
        *.*) ;;
        *) domain="" ;;
    esac
    if [ -n "$_domain_input" ] && [ -z "$domain" ]; then
        warn "域名「${_domain_input}」看起来无效，将改用临时 HTTP。示例：panel.example.com"
    fi
    if [ -n "$domain" ]; then
        apply_domain_config "$domain"
        cf_token="$(collect_cf_token)"
        if [ -n "$cf_token" ]; then
            write_env_value "ACME_DNS_PROVIDER" "cf" "$env_file"
            write_env_value "CF_TOKEN" "$cf_token" "$env_file"
            cf_enabled=1
        else
            warn "未配置 Cloudflare Token，将使用 HTTP-01（需 80/443 公网可达）"
        fi
    fi
fi

# 无域名（未提供 public_url 且未输入域名）→ HTTP 随机路径临时部署
if [ -z "$public_url" ] && [ -z "$domain" ]; then
    panel_path="/panel-$(random_hex 8)"
    write_env_value "PANEL_BACKEND_HOST" "0.0.0.0" "$env_file"
    write_env_value "NRE_PANEL_PUBLIC_PATH" "$panel_path" "$env_file"
    panel_root_url="http://127.0.0.1:8080${panel_path}"
    public_ip="$(detect_public_ip)"
    warn "临时 HTTP 部署：公网明文有风险，仅建议短期使用"
    warn "面板路径：${panel_path}"
fi

configure_forwarded_headers_trust "$env_file"

# 收紧 .env 权限：内含 token，不应被其他用户读取。
chmod 600 "$env_file" 2>/dev/null || true

# 部署前预览（默认直接开始；仅临时 HTTP 再确认一次，避免误暴露公网）
if [ -n "$domain" ]; then
    _access="https://${domain}"
    _listen="127.0.0.1:8080 + 面板自代理 HTTPS"
elif [ -n "$public_url" ]; then
    _access="${public_url}"
    _listen="127.0.0.1:8080（外部反代 / 已有 HTTPS）"
else
    [ -n "$public_ip" ] || public_ip="$(detect_public_ip)"
    _access="http://${public_ip}:8080${panel_path}"
    _listen="0.0.0.0:8080（临时公网 HTTP）"
fi
if [ "$cf_enabled" -eq 1 ]; then
    _cert="Cloudflare DNS-01"
elif [ -n "$domain" ] || [ -n "$public_url" ]; then
    _cert="HTTP-01（需 80/443）"
else
    _cert="暂无（临时 HTTP）"
fi

say "即将部署"
cat >&2 <<EOF
  访问 : ${_access}
  监听 : ${_listen}
  证书 : ${_cert}
  目录 : ${install_dir}
EOF

if [ "$interactive" -eq 1 ] && [ "$opt_yes" -eq 0 ] && [ -z "$domain" ] && [ -z "$public_url" ]; then
    if ! ask_yes_no "将以临时公网 HTTP 启动，是否继续" "y"; then
        warn "已取消。配置保留在 $(pwd)/${env_file}"
        exit 0
    fi
fi

say "启动容器"
# shellcheck disable=SC2086
if [ "$force_recreate" -eq 1 ]; then
    # shellcheck disable=SC2086
    $compose up -d --force-recreate
else
    # shellcheck disable=SC2086
    $compose up -d
fi

if wait_panel_ready "$api_token"; then
    say "控制面板已就绪"
else
    warn "健康检查未通过，稍后可执行：cd ${install_dir} && ${compose} logs -f"
fi

if [ -n "$domain" ]; then
    setup_domain_self_proxy "$api_token" "$domain" "$cf_enabled"
fi

# 结束摘要：只打印访问入口 + 登录密码；注册令牌仅在需要多节点时再提一句。
if [ -n "$domain" ]; then
    if [ "$panel_self_proxy_scheme" = "http" ]; then
        _final_url="http://${domain}/"
        _final_note="HTTPS 未就绪，当前为 HTTP 后备；修好证书/DNS 后请改回 HTTPS。"
    else
        _final_url="https://${domain}/"
        _final_note="若证书仍在签发，稍后再刷新即可。"
    fi
elif [ -n "$public_url" ]; then
    _final_url="$public_url"
    _final_note="请确认外部反代 / DNS 已指向本机 127.0.0.1:8080。"
else
    _final_url="http://${public_ip}:8080${panel_path}"
    _final_note="临时 HTTP，建议尽快绑定域名并开启 HTTPS。"
fi

cat <<EOF

部署完成

访问地址：
  ${_final_url}

登录密码（Panel token）：
  ${api_token}

${_final_note}
本机备用（SSH 隧道）：
  ssh -L 8080:127.0.0.1:8080 root@${public_ip:-<服务器IP>}
  http://127.0.0.1:8080${panel_path}

加入远程节点时，在面板「节点管理 → 加入节点」复制命令即可。
注册令牌（一般不必手抄）：${register_token}

常用命令：
  cd ${install_dir}
  ${compose} ps
  ${compose} logs -f
  ${compose} pull && ${compose} up -d
EOF
