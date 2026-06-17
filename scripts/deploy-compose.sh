#!/bin/sh
set -eu

repo_raw_base="${NRE_REPO_RAW_BASE:-https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main}"
install_dir="${NRE_INSTALL_DIR:-nginx-reverse-emby}"
image="${NRE_IMAGE:-sakullla/nginx-reverse-emby:latest}"
timezone="${NRE_TIMEZONE:-Asia/Shanghai}"
public_url="${NRE_PUBLIC_URL:-}"
docker_cli_version="${NRE_DOCKER_CLI_VERSION:-29.5.3}"
docker_compose_version="${NRE_DOCKER_COMPOSE_VERSION:-v5.1.4}"
panel_health_url="${NRE_PANEL_HEALTH_URL:-http://127.0.0.1:8080/panel-api/info}"
panel_api_base="${NRE_PANEL_API_BASE:-http://127.0.0.1:8080}"

usage() {
    cat <<'EOF'
用法：deploy-compose.sh [选项]

nginx-reverse-emby 新手 Docker Compose 部署脚本。

选项：
  --dir DIR          安装目录，默认 nginx-reverse-emby
  --image IMAGE      容器镜像，默认 sakullla/nginx-reverse-emby:latest
  --timezone TZ      面板时区，默认 Asia/Shanghai
  --public-url URL   已有 HTTPS 面板地址，例如 https://panel.example.com
  -h, --help         显示帮助

环境变量：
  NRE_REPO_RAW_BASE  docker-compose.yaml 下载地址前缀
  API_TOKEN          已有面板 token；不设置则自动生成
  MASTER_REGISTER_TOKEN 已有 Agent 注册 token；不设置则自动生成
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

say() {
    printf '\n[NRE] %s\n' "$*" >&2
}

warn() {
    printf '\n[注意] %s\n' "$*" >&2
}

ask() {
    prompt="$1"
    default="${2:-}"
    if [ -n "$default" ]; then
        printf '%s [%s]: ' "$prompt" "$default" >&2
    else
        printf '%s: ' "$prompt" >&2
    fi
    IFS= read -r answer || answer=""
    if [ -z "$answer" ]; then
        printf '%s' "$default"
    else
        printf '%s' "$answer"
    fi
}

ask_yes_no() {
    prompt="$1"
    default="${2:-n}"
    while :; do
        answer="$(ask "$prompt (y/n)" "$default")"
        case "$(printf '%s' "$answer" | tr '[:upper:]' '[:lower:]')" in
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
    echo "需要 root 权限，但当前系统没有 sudo。请切换 root 后重试。" >&2
    exit 1
}

install_docker_compose() {
    say "检测到 Docker 或 Docker Compose 不完整，准备自动安装。"
    echo "脚本会安装 Docker Engine 与 Compose 插件，并可能修改系统软件源。" >&2

    if [ -S /var/run/docker.sock ] && ! command -v docker >/dev/null 2>&1; then
        say "检测到本机已有 Docker Socket，优先安装 Docker CLI 与 Compose 插件"
        install_static_docker_client
        return
    fi

    if command -v apt-get >/dev/null 2>&1; then
        say "使用官方 Docker 安装脚本安装 Docker 与 Compose 插件"
        ensure_packages curl
        ensure_ca_certificates
        if curl -fsSL https://get.docker.com -o /tmp/nre-get-docker.sh && run_as_root sh /tmp/nre-get-docker.sh; then
            :
        else
            warn "官方安装脚本失败，改用静态 Docker CLI 与 Compose 插件后备安装。"
            install_static_docker_client
        fi
    elif command -v dnf >/dev/null 2>&1; then
        say "使用官方 Docker 安装脚本安装 Docker 与 Compose 插件"
        ensure_packages curl
        ensure_ca_certificates
        if curl -fsSL https://get.docker.com -o /tmp/nre-get-docker.sh && run_as_root sh /tmp/nre-get-docker.sh; then
            :
        else
            warn "官方安装脚本失败，改用 dnf 安装 Docker 客户端与 Compose 插件。"
            run_as_root dnf install -y docker-cli docker-compose-plugin || install_static_docker_client
        fi
    elif command -v yum >/dev/null 2>&1; then
        say "使用官方 Docker 安装脚本安装 Docker 与 Compose 插件"
        ensure_packages curl
        ensure_ca_certificates
        if curl -fsSL https://get.docker.com -o /tmp/nre-get-docker.sh && run_as_root sh /tmp/nre-get-docker.sh; then
            :
        else
            warn "官方安装脚本失败，改用 yum 安装 Docker 客户端与 Compose 插件。"
            run_as_root yum install -y docker-cli docker-compose-plugin || install_static_docker_client
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
        *) echo "暂不支持自动安装当前 CPU 架构的 Docker CLI：$arch" >&2; exit 1 ;;
    esac

    tmp_dir="${TMPDIR:-/tmp}/nre-docker-client.$$"
    mkdir -p "$tmp_dir"
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
        echo "Docker Compose 仍不可用，请检查 Docker 安装状态。" >&2
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
    echo "需要 openssl 或 od 生成随机 token。" >&2
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
    ip=""
    if command -v curl >/dev/null 2>&1; then
        ip="$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)"
    fi
    if [ -n "$ip" ]; then
        printf '%s' "$ip"
    else
        printf '<服务器IP>'
    fi
}

ensure_packages mkdir grep sed cut tr curl

say "欢迎使用 nginx-reverse-emby 新手部署脚本"
echo "安装目录：${install_dir}" >&2
echo "镜像：${image}" >&2
echo "时区：${timezone}" >&2

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
if [ -n "$public_url" ]; then
    write_env_value "NRE_PUBLIC_URL" "$public_url" "$env_file"
elif ask_yes_no "你是否已经有域名，并且 DNS 已经解析到这台服务器" "y"; then
    domain="$(ask "请输入面板域名，例如 panel.example.com" "")"
    while [ -z "$domain" ]; do
        domain="$(ask "域名不能为空，请重新输入" "")"
    done
    write_env_value "PANEL_BACKEND_HOST" "127.0.0.1" "$env_file"
    write_env_value "NRE_PUBLIC_URL" "https://${domain}" "$env_file"

    say "推荐使用 Cloudflare API Token 自动申请证书"
    echo "操作指引：Cloudflare 控制台 -> 右上角头像 -> My Profile -> API Tokens -> Create Token。" >&2
    echo "选择 Custom Token；权限必须包含：区域 / 区域 / 读取，区域 / DNS / 读取，区域 / DNS / 编辑。" >&2
    echo "区域资源选择：特定区域 / 你的域名；不要限制客户端 IP；不要使用 Global API Key。" >&2
    if ask_yes_no "你现在是否要填写 Cloudflare API Token" "y"; then
        cf_token="$(ask "请粘贴 Cloudflare API Token" "")"
        if [ -n "$cf_token" ]; then
            write_env_value "ACME_DNS_PROVIDER" "cf" "$env_file"
            write_env_value "CF_TOKEN" "$cf_token" "$env_file"
        else
            warn "未填写 Cloudflare Token，将回退到 HTTP-01。请确保 80/443 端口公网可访问。"
        fi
    else
        warn "未配置 Cloudflare Token，将回退到 HTTP-01。请确保 80/443 端口公网可访问。"
    fi
else
    panel_path="/panel-$(random_hex 8)"
    write_env_value "PANEL_BACKEND_HOST" "0.0.0.0" "$env_file"
    write_env_value "NRE_PANEL_PUBLIC_PATH" "$panel_path" "$env_file"
    public_ip="$(detect_public_ip)"
    warn "你选择了没有域名的 HTTP 部署。公网 HTTP 会暴露 token 传输风险，只建议临时使用。"
    warn "脚本已为面板生成随机访问路径：${panel_path}"
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
