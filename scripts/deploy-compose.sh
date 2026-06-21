#!/bin/sh
set -eu

# ---- 可配置默认值（环境变量可覆盖，便于自动化 / 非交互部署） ----
repo_raw_base="${NRE_REPO_RAW_BASE:-https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main}"
install_dir="${NRE_INSTALL_DIR:-nginx-reverse-emby}"
image="${NRE_IMAGE:-sakullla/nginx-reverse-emby:latest}"
timezone="${NRE_TIMEZONE:-Asia/Shanghai}"
public_url="${NRE_PUBLIC_URL:-}"
docker_cli_version="${NRE_DOCKER_CLI_VERSION:-29.5.3}"
docker_compose_version="${NRE_DOCKER_COMPOSE_VERSION:-v5.1.4}"
panel_health_url="${NRE_PANEL_HEALTH_URL:-http://127.0.0.1:8080/panel-api/info}"
panel_api_base="${NRE_PANEL_API_BASE:-http://127.0.0.1:8080}"

opt_noninteractive=0
opt_yes=0

usage() {
    cat <<'EOF'
用法：deploy-compose.sh [选项]

nginx-reverse-emby 新手 Docker Compose 部署脚本。
脚本会下载 docker-compose.yaml、生成随机 token、按需安装 Docker Compose，
并优先引导你用域名 + Cloudflare API Token 配置 HTTPS 面板自代理。

选项：
  --dir DIR            安装目录，默认 nginx-reverse-emby
  --image IMAGE        容器镜像，默认 sakullla/nginx-reverse-emby:latest
  --timezone TZ        面板时区，默认 Asia/Shanghai
  --public-url URL     已有 HTTPS 面板地址，例如 https://panel.example.com
  --cf-token TOKEN     直接提供 Cloudflare API Token（跳过交互输入并在线校验）
  --non-interactive    关闭所有交互提示，未提供的值回退到默认或环境变量
  --yes                跳过部署前的计划确认
  -h, --help           显示帮助

环境变量（同样可覆盖对应选项，便于 curl | sh 自动化）：
  NRE_REPO_RAW_BASE    docker-compose.yaml 下载地址前缀
  NRE_INSTALL_DIR / NRE_IMAGE / NRE_TIMEZONE / NRE_PUBLIC_URL
  API_TOKEN            已有面板 token；不设置则自动生成
  MASTER_REGISTER_TOKEN 已有 Agent 注册 token；不设置则自动生成
  CF_TOKEN             Cloudflare API Token；设置后自动启用 DNS-01 并在线校验
  ACME_DNS_PROVIDER    设为 cf 以启用 Cloudflare DNS 验证
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

env_value() {
    key="$1"
    file="$2"
    grep "^${key}=" "$file" 2>/dev/null | tail -n 1 | cut -d= -f2-
}

wait_panel_ready() {
    token="$1"
    for _ in 1 2 3 4 5 6 7 8 9 10 11 12; do
        if curl -fsS -H "X-Panel-Token: ${token}" "$panel_health_url" >/dev/null 2>&1; then
            return 0
        fi
        sleep 5
    done
    return 1
}

json_escape() {
    printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

create_panel_self_proxy() {
    token="$1"
    domain="$2"
    scheme="${3:-https}"
    frontend="${scheme}://${domain}"
    payload="$(printf '{"frontend_url":"%s","backends":[{"url":"http://127.0.0.1:8080"}],"tags":["panel","bootstrap"]}' "$(json_escape "$frontend")")"

    say "正在创建面板自代理规则：${frontend} -> http://127.0.0.1:8080"
    if curl -fsS \
        -H "Content-Type: application/json" \
        -H "X-Panel-Token: ${token}" \
        -d "$payload" \
        "${panel_api_base}/panel-api/agents/local/rules" >/dev/null; then
        curl -fsS -X POST -H "X-Panel-Token: ${token}" "${panel_api_base}/panel-api/agents/local/apply" >/dev/null 2>&1 || true
        return 0
    fi
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
    say "推荐使用 Cloudflare API Token 自动申请证书（DNS-01，无需提前开放 80 端口）"
    cat >&2 <<'EOF'
创建步骤：
  1) 打开 https://dash.cloudflare.com/profile/api-tokens
  2) 点击 Create Token -> Create Custom Token（自定义令牌）
  3) 权限（以下三项均为必须，缺一不可）：
       - 区域 / 区域 / 读取    (Zone / Zone / Read)
       - 区域 / DNS / 读取     (Zone / DNS / Read)
       - 区域 / DNS / 编辑     (Zone / DNS / Edit)
  4) 区域资源：包含 - 特定区域 - 你的域名
  5) 不要勾选客户端 IP 限制；不要使用账号级 Global API Key
  6) Continue to summary -> Create Token -> 复制生成的 Token（仅显示一次）

注意：Cloudflare 内置的 Edit zone DNS 模板只给「区域/区域/读取 + 区域/DNS/编辑」，
      缺少必需的「区域/DNS/读取」，不能直接使用，请按上面三项权限创建 Custom Token。
EOF
}

# 调用 Cloudflare API 在线校验 Token 是否有效（best-effort，网络不可达时跳过）。
# 返回 0 = 有效或无法判定；返回 1 = 明确无效。
verify_cf_token() {
    _vt="$1"
    if ! command -v curl >/dev/null 2>&1; then
        say "未检测到 curl，跳过 Cloudflare Token 在线校验。"
        return 0
    fi
    _resp="$(curl -sS --max-time 15 \
        -H "Authorization: Bearer ${_vt}" \
        "https://api.cloudflare.com/client/v4/user/tokens/verify" 2>/dev/null || true)"
    if [ -z "$_resp" ]; then
        warn "无法连接 Cloudflare API（可能网络受限），已跳过在线校验。"
        return 0
    fi
    _status="$(cf_field "$_resp" status)"
    if [ "$_status" = "active" ]; then
        say "Cloudflare Token 在线校验通过：token 有效（active）。"
        return 0
    fi
    if printf '%s' "$_resp" | grep -q '"success":[[:space:]]*true'; then
        say "Cloudflare Token 在线校验通过。"
        return 0
    fi
    _msg="$(cf_field "$_resp" message)"
    warn "Cloudflare Token 校验失败：${_msg:-返回未包含可识别信息}"
    warn "常见原因：粘贴了 Global API Key（应改用 API Token）、token 已撤销或过期。"
    warn "提示：在线校验只确认 token 本身有效，无法完全核实 DNS 编辑权限，部署后请留意证书签发日志。"
    return 1
}

# 收集 Cloudflare Token。优先级：--cf-token / CF_TOKEN 环境变量 > .env 已有 > 交互输入。
collect_cf_token() {
    _cf="${CF_TOKEN:-$(env_value CF_TOKEN "$env_file")}"
    if [ -n "$_cf" ]; then
        say "检测到已有 Cloudflare Token（来自 CF_TOKEN 或 .env），将复用并在线校验。"
        verify_cf_token "$_cf" || warn "已有 Token 校验未通过，仍会写入，请稍后核对。"
        printf '%s' "$_cf"
        return
    fi
    if [ "$interactive" -eq 0 ]; then
        printf ''
        return
    fi
    print_cf_guide
    if ! ask_yes_no "你现在是否要填写 Cloudflare API Token" "y"; then
        printf ''
        return
    fi
    _attempt=0
    _entered=""
    while [ "$_attempt" -lt 3 ]; do
        _attempt=$((_attempt + 1))
        _entered="$(read_secret "请粘贴 Cloudflare API Token（输入时不会回显）" "")"
        _entered="$(printf '%s' "$_entered" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
        if [ -z "$_entered" ]; then
            warn "未填写 Token。"
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
        if [ "$_attempt" -lt 3 ] && ask_yes_no "是否重新粘贴 Cloudflare Token" "y"; then
            continue
        fi
        printf '%s' "$_entered"
        return
    done
    printf '%s' "$_entered"
}

ensure_packages mkdir grep sed cut tr curl

say "欢迎使用 nginx-reverse-emby 新手部署脚本"
echo "安装目录：${install_dir}" >&2
echo "镜像：${image}" >&2
echo "时区：${timezone}" >&2
if [ "$interactive" -eq 0 ]; then
    echo "运行模式：非交互（未提供的配置将使用默认值或环境变量）" >&2
fi

compose="$(ensure_docker_compose)"

mkdir -p "$install_dir"
cd "$install_dir"
mkdir -p data

if [ ! -f docker-compose.yaml ]; then
    say "下载 docker-compose.yaml"
    curl -fsSL "${repo_raw_base}/docker-compose.yaml" -o docker-compose.yaml
else
    say "发现已有 docker-compose.yaml，将继续复用"
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

if [ -n "$public_url" ]; then
    write_env_value "NRE_PUBLIC_URL" "$public_url" "$env_file"
    say "已写入 NRE_PUBLIC_URL=${public_url}"
    if [ -n "${CF_TOKEN:-}" ]; then
        verify_cf_token "$CF_TOKEN" || warn "CF_TOKEN 校验未通过，仍会写入。"
        write_env_value "ACME_DNS_PROVIDER" "cf" "$env_file"
        write_env_value "CF_TOKEN" "$CF_TOKEN" "$env_file"
        cf_enabled=1
    fi
elif [ "$interactive" -eq 1 ] && ask_yes_no "你是否已经有域名，并且 DNS 已经解析到这台服务器" "y"; then
    domain="$(ask "请输入面板域名，例如 panel.example.com" "")"
    _tries=0
    while [ -z "$domain" ]; do
        _tries=$((_tries + 1))
        if [ "$_tries" -ge 5 ]; then
            warn "多次未输入域名，将改用无域名的 HTTP 临时部署。"
            break
        fi
        domain="$(ask "域名不能为空，请重新输入" "")"
    done

    if [ -n "$domain" ]; then
        # 规范化：去掉首尾空白 / scheme / 路径 / 端口
        domain="$(printf '%s' "$domain" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//; s#^[a-zA-Z][a-zA-Z0-9+.-]*://##; s#/.*##; s#:.*##')"
        write_env_value "PANEL_BACKEND_HOST" "127.0.0.1" "$env_file"
        write_env_value "NRE_PUBLIC_URL" "https://${domain}" "$env_file"

        cf_token="$(collect_cf_token)"
        if [ -n "$cf_token" ]; then
            write_env_value "ACME_DNS_PROVIDER" "cf" "$env_file"
            write_env_value "CF_TOKEN" "$cf_token" "$env_file"
            cf_enabled=1
        else
            warn "未配置 Cloudflare Token，将回退到 HTTP-01。请确保 80/443 端口公网可访问。"
        fi
    fi
fi

# 无域名（未提供 public_url 且未输入域名）→ HTTP 随机路径临时部署
if [ -z "$public_url" ] && [ -z "$domain" ]; then
    panel_path="/panel-$(random_hex 8)"
    write_env_value "PANEL_BACKEND_HOST" "0.0.0.0" "$env_file"
    write_env_value "NRE_PANEL_PUBLIC_PATH" "$panel_path" "$env_file"
    public_ip="$(detect_public_ip)"
    warn "你选择了没有域名的 HTTP 部署。公网 HTTP 会暴露 token 传输风险，只建议临时使用。"
    warn "脚本已为面板生成随机访问路径：${panel_path}"
fi

# 收紧 .env 权限：内含 token，不应被其他用户读取。
chmod 600 "$env_file" 2>/dev/null || true

# 部署前预览与确认
if [ -n "$domain" ]; then
    _access="https://${domain}"
    _listen="127.0.0.1:8080（由面板自代理对外提供 HTTPS）"
elif [ -n "$public_url" ]; then
    _access="${public_url}"
    _listen="127.0.0.1:8080（由你已有的反代 / HTTPS 对外提供服务）"
else
    [ -n "$public_ip" ] || public_ip="$(detect_public_ip)"
    _access="http://${public_ip}:8080${panel_path}"
    _listen="0.0.0.0:8080（公网 HTTP，仅临时使用）"
fi
if [ "$cf_enabled" -eq 1 ]; then
    _cert="Cloudflare DNS-01（已写入 Token）"
else
    _cert="HTTP-01（需 80/443 公网可达）或稍后在面板配置"
fi

say "部署计划"
cat >&2 <<EOF
  安装目录 : ${install_dir}
  镜像     : ${image}
  时区     : ${timezone}
  面板访问 : ${_access}
  监听方式 : ${_listen}
  证书方式 : ${_cert}
EOF

if [ "$interactive" -eq 1 ] && [ "$opt_yes" -eq 0 ]; then
    if ! ask_yes_no "确认按以上计划开始部署" "y"; then
        warn "已取消部署。已生成的配置保留在 $(pwd)/${env_file}。"
        exit 0
    fi
fi

say "启动控制面板容器"
# shellcheck disable=SC2086
$compose up -d

if wait_panel_ready "$api_token"; then
    say "控制面板已启动"
else
    warn "面板暂未通过健康检查。可以稍后运行：cd ${install_dir} && ${compose} logs -f"
fi

if [ -n "$domain" ]; then
    if create_panel_self_proxy "$api_token" "$domain" "https"; then
        say "面板自代理规则已创建，证书申请可能需要 1-3 分钟"
    else
        warn "HTTPS 自代理规则创建失败，通常是域名、DNS、Cloudflare Token 权限或 ACME 校验失败。"
        if create_panel_self_proxy "$api_token" "$domain" "http"; then
            warn "已创建 HTTP 后备自代理规则：http://${domain} -> http://127.0.0.1:8080。请修复证书/DNS/Cloudflare 后在面板中改为 HTTPS。"
        else
            warn "HTTP 后备规则也创建失败。请登录面板后手动添加：前端 http://${domain}，后端 http://127.0.0.1:8080，节点 local。"
        fi
    fi
fi

cat <<EOF

部署完成。

面板 token：
  ${api_token}

Agent 注册 token：
  ${register_token}

EOF

if [ -n "$domain" ]; then
    cat <<EOF
推荐访问地址：
  https://${domain}

如果证书还在签发中，请等待几分钟后刷新；也可以临时用 SSH 隧道访问：
  ssh -L 8080:127.0.0.1:8080 root@<服务器IP>
  http://127.0.0.1:8080

EOF
elif [ -n "$public_url" ]; then
    cat <<EOF
推荐访问地址：
  ${public_url}

面板本机仍监听 127.0.0.1:8080，请确认你的反向代理 / DNS 已把该地址指向本机。
公网暂时走不通时，可临时用 SSH 隧道访问：
  ssh -L 8080:127.0.0.1:8080 root@<服务器IP>
  http://127.0.0.1:8080

EOF
else
    cat <<EOF
HTTP 临时访问地址：
  http://${public_ip}:8080${panel_path}

更安全的首次访问方式：
  ssh -L 8080:127.0.0.1:8080 root@${public_ip}
  http://127.0.0.1:8080${panel_path}

后续建议准备域名并配置 Cloudflare Token，再在面板添加自代理 HTTPS 规则。

EOF
fi

cat <<EOF
常用命令：
  cd ${install_dir}
  ${compose} ps
  ${compose} logs -f
  ${compose} pull && ${compose} up -d
EOF
