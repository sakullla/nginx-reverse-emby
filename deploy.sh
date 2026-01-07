#!/bin/bash

# ===================================================================================
#
#           Nginx Reverse Proxy Deployment Script (China Optimized & Robust)
#
# ===================================================================================

# --- 脚本严格模式 ---
set -e
set -o pipefail

# --- 颜色定义 ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# --- 权限变量 ---
SUDO=''

# --- 权限检查 ---
if [ "$(id -u)" -ne 0 ]; then
    if ! command -v sudo >/dev/null; then
        echo -e "${RED}错误: 此脚本需要以 root 权限运行，或者必须安装 'sudo'。${NC}" >&2
        exit 1
    fi
    SUDO='sudo'
    echo -e "${YELLOW}信息: 检测到非 root 用户，将使用 'sudo' 获取权限。${NC}"
fi

# ===================================================================================
#                                 基础检测与环境设置
# ===================================================================================

# --- 检测是否在中国大陆 ---
is_in_china() {
    if [ -z "$_loc" ]; then
        if _loc=$(curl -m 3 -sL https://www.cloudflare.com/cdn-cgi/trace | grep '^loc=' | cut -d= -f2); then
            true
        elif _loc=$(curl -m 3 -sL http://www.qualcomm.cn/cdn-cgi/trace | grep '^loc=' | cut -d= -f2); then
            true
        else
            return 1
        fi
    fi
    [ "$_loc" = CN ]
}

# --- 设置全局变量 (将在解析参数后调用) ---
setup_env() {
    # [技巧] 使用字符串拼接定义基础 URL，防止被镜像站的自动替换机制修改 (Anti-Rewrite)
    local GH_RAW_HOST="raw.githubusercontent.com"
    local URL_PREFIX="https://${GH_RAW_HOST}"
    
    local RAW_URL_BASE="${URL_PREFIX}/sakullla/nginx-reverse-emby/main"
    local ACME_OFFICIAL_RAW="${URL_PREFIX}/acmesh-official/acme.sh/master/acme.sh"
    
    # 确定代理地址: 命令行参数 > 环境变量 > 自动检测
    local effective_gh_proxy="${manual_gh_proxy:-${GH_PROXY}}"
    if [[ -z "$effective_gh_proxy" ]] && is_in_china; then
        # 国内自动使用 gh.llkk.cc 代理
        effective_gh_proxy="https://gh.llkk.cc"
    fi

    # 确保代理地址以 / 结尾 (如果非空)
    if [[ -n "$effective_gh_proxy" && "$effective_gh_proxy" != */ ]]; then
        effective_gh_proxy="${effective_gh_proxy}/"
    fi

    if [[ -n "$effective_gh_proxy" ]]; then
        log_info "使用 GitHub 代理: ${effective_gh_proxy}"
        
        # 通过代理获取配置 URL
        CONF_HOME="${effective_gh_proxy}${RAW_URL_BASE}"
        ACME_INSTALL_URL="${effective_gh_proxy}${ACME_OFFICIAL_RAW}"
    else
        log_info "未使用 GitHub 代理，使用默认源..."
        CONF_HOME="${RAW_URL_BASE}"
        ACME_INSTALL_URL="${ACME_OFFICIAL_RAW}"
    fi

    readonly CONF_HOME
    readonly BACKUP_DIR="/etc/nginx/backup"
    readonly ACME_INSTALL_URL
}

# ===================================================================================
#                                 辅助函数
# ===================================================================================

# --- 日志函数 ---
log_info() { echo -e "${BLUE}[INFO]${NC} $1" >&2; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1" >&2; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $1" >&2; }

# --- 错误处理 ---
handle_error() {
    local exit_code=$?
    local line_number=$1
    echo >&2
    echo -e "${RED}--------------------------------------------------------${NC}" >&2
    echo -e "${RED}错误: 脚本在第 $line_number 行意外中止。${NC}" >&2
    echo -e "${RED}退出码: $exit_code${NC}" >&2
    echo -e "${RED}--------------------------------------------------------${NC}" >&2
    exit "$exit_code"
}
trap 'handle_error $LINENO' ERR

# --- 备份函数 ---
backup_file() {
    local file_path="$1"
    if [ -f "$file_path" ]; then
        $SUDO mkdir -p "$BACKUP_DIR"
        local file_name
        file_name=$(basename "$file_path")
        $SUDO cp "$file_path" "$BACKUP_DIR/$file_name"
        log_info "已备份文件 $file_path 至 $BACKUP_DIR/$file_name"
    fi
}

# --- 帮助信息 ---
show_help() {
    cat << EOF
用法: $(basename "$0") [选项]

一个强大且安全的 Nginx 反向代理部署脚本 (支持 sudo 和 IPv6)。

部署选项:
  -y, --you-domain <URL>         你的访问域名或完整 URL (支持 IPv6, 如: https://[2400::1]:443)
  -r, --r-domain <URL>           被代理的后端地址 (例如: http://127.0.0.1:8096)
  -m, --cert-domain <域名>       (可选) 手动指定 SSL 证书的主域名。
  -d, --parse-cert-domain        (可选) 自动提取根域名作为证书域名。
  -D, --dns <provider>           (可选) 使用 DNS API 模式申请证书 (例如: cf)。
  -R, --resolver <DNS>           (可选) 手动指定 DNS 解析服务器。
  -c, --template <URL>           (可选) 指定自定义 Nginx 配置文件模板。
  --gh-proxy <URL>               (可选) 指定 GitHub 加速代理。
  --cf-token <TOKEN>             Cloudflare API Token。
  --cf-account-id <ID>           Cloudflare Account ID。

管理选项:
  --remove <URL>                 移除指定域名的 Nginx 配置和证书。
  -Y, --yes                      非交互模式下自动确认移除。

其他:
  -h, --help                     显示此帮助信息。
EOF
    exit 0
}

# --- DNS 和 IPv6 检测 ---
has_ipv6() {
    ip -6 addr show scope global | grep -q inet6
}

get_resolver_host() {
    local system_dns
    system_dns=$(awk '/^nameserver/ { print ($2 ~ /:/ ? "["$2"]" : $2) }' /etc/resolv.conf 2>/dev/null | xargs)
    
    if [[ -n "$system_dns" ]]; then
        echo "$system_dns"
    else
        if is_in_china; then
            echo "223.5.5.5 119.29.29.29"
        else
            echo "1.1.1.1 8.8.8.8"
        fi
    fi
}

# --- URL 解析 (支持 IPv6) ---
parse_url() {
    local url="$1"
    local proto domain port path

    # 提取协议
    if [[ "$url" =~ ^(https?):// ]]; then
        proto="${BASH_REMATCH[1]}"
        url="${url#*://}"
    else
        echo "$url|||" # 无协议则认为无效或纯域名(暂不支持无协议输入)
        return
    fi

    # 提取域名/IP (支持 [IPv6])
    if [[ "$url" =~ ^\[([a-fA-F0-9:.]+)\] ]]; then
        # IPv6 格式 [xxxx:xxxx]
        domain="[${BASH_REMATCH[1]}]"
        url="${url#*]}" # 移除匹配到的 [ipv6]
    else
        # IPv4 或 域名 (提取直到 : / ? #)
        if [[ "$url" =~ ^([^/:?#]+) ]]; then
            domain="${BASH_REMATCH[1]}"
            url="${url#${domain}}"
        fi
    fi

    # 提取端口
    if [[ "$url" =~ ^:([0-9]+) ]]; then
        port="${BASH_REMATCH[1]}"
        url="${url#:${port}}"
    fi

    # 剩余部分为路径
    path="$url"

    echo "$proto|$domain|$port|$path"
}

# --- 下载文件 (带验证和重试) ---
download_with_verify() {
    local url="$1"
    local output="$2"
    local verify_keyword="$3"
    
    if curl -fsL "$url" -o "$output"; then
        if [[ -z "$verify_keyword" ]] || grep -q "$verify_keyword" "$output"; then
            return 0
        else
            log_error "下载的文件内容异常: $output"
            return 1
        fi
    else
        log_error "无法下载: $url"
        return 1
    fi
}

# --- 获取协议 ---
get_protocol() {
    [[ "$1" == "yes" ]] && echo "http" || echo "https"
}

# --- 是否为 IP 地址 (支持 IPv4 和 IPv6) ---
is_ip_address() {
    local addr="$1"
    # 移除可能存在的方括号
    local clean_addr="${addr#[}"
    clean_addr="${clean_addr%]}"

    # IPv4 检查
    if [[ "$clean_addr" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
        return 0
    fi
    
    # IPv6 检查 (简单启发式: 包含冒号)
    if [[ "$clean_addr" =~ : ]]; then
        return 0
    fi
    
    return 1
}

process_url_input() {
    local full_url="$1"
    local domain_type="$2" # "you" or "r"

    if [[ -z "$full_url" ]]; then return; fi

    local temp_domain temp_path temp_port temp_proto
    IFS='|' read -r temp_proto temp_domain temp_port temp_path < <(parse_url "$full_url")

    temp_proto=${temp_proto:-https}
    local default_port=$([[ "$temp_proto" == "http" ]] && echo 80 || echo 443)
    local is_http=$([[ "$temp_proto" == "http" ]] && echo "yes" || echo "no")
    
    if [[ "$domain_type" == "you" ]]; then
        you_domain="$temp_domain"
        you_domain_path="$temp_path"
        no_tls="$is_http"
        you_frontend_port="${temp_port:-$default_port}"
    elif [[ "$domain_type" == "r" ]]; then
        r_domain="$temp_domain"
        r_domain_path="$temp_path"
        r_http_frontend="$is_http"
        r_frontend_port="${temp_port:-$default_port}"
    fi
}

# ===================================================================================
#                                 核心逻辑
# ===================================================================================

# --- 1. 参数解析 ---
parse_arguments() {
    you_domain_full=""
    r_domain_full=""
    cert_domain=""
    manual_resolver=""
    parse_cert_domain="no"
    dns_provider=""
    cf_token=""
    cf_account_id=""
    domain_to_remove=""
    force_yes="no"
    template_domain_config_source=""
    manual_gh_proxy=""

    you_domain=""; you_domain_path=""; you_frontend_port=""; no_tls=""
    r_domain=""; r_domain_path=""; r_frontend_port=""; r_http_frontend=""

    local TEMP
    if ! TEMP=$(getopt -o y:r:m:R:dD:hYc: --long you-domain:,r-domain:,cert-domain:,resolver:,parse-cert-domain,dns:,cf-token:,cf-account-id:,gh-proxy:,remove:,yes,template-domain-config:,help -n "$(basename "$0")" -- "$@"); then
        exit 1
    fi
    eval set -- "$TEMP"
    unset TEMP

    while true; do
        case "$1" in
            -y|--you-domain) you_domain_full="$2"; shift 2 ;;
            -r|--r-domain) r_domain_full="$2"; shift 2 ;;
            -m|--cert-domain) cert_domain="$2"; shift 2 ;;
            -d|--parse-cert-domain) parse_cert_domain="yes"; shift ;;
            -D|--dns) dns_provider="$2"; shift 2 ;;
            -R|--resolver) manual_resolver="$2"; shift 2 ;;
            -c|--template-domain-config) template_domain_config_source="$2"; shift 2 ;;
            --gh-proxy) manual_gh_proxy="$2"; shift 2 ;;
            --cf-token) cf_token="$2"; shift 2 ;;
            --cf-account-id) cf_account_id="$2"; shift 2 ;;
            --remove) domain_to_remove="$2"; shift 2 ;;
            -Y|--yes) force_yes="yes"; shift ;;
            -h|--help) show_help; shift ;;
            --) shift; break ;;
            *) log_error "未知参数 $1"; exit 1 ;;
        esac
    done

    process_url_input "$you_domain_full" "you"
    process_url_input "$r_domain_full" "r"
}

# --- 2. 交互模式 ---
prompt_interactive_mode() {
    if [[ -z "$you_domain" || -z "$r_domain" ]]; then
        if [ ! -t 0 ]; then
            log_error "无法进入交互模式。请提供 -y 和 -r 参数。"
            exit 1
        fi

        echo -e "\n${BLUE}--- 交互模式: 配置反向代理 ---${NC}"
        read -rp "请输入你的访问地址 (即本机的公网 IP 或域名, 例如 https://emby.mysite.com, https://11.22.33.44:8888 或 https://[2400::1]:8888):" input_you
        read -rp "请输入Emby 服务的完整地址 (被代理的服务地址, 例如 https://emby.server.com):" input_r

        process_url_input "$input_you" "you"
        process_url_input "$input_r" "r"

        if [[ -z "$you_domain" || -z "$r_domain" ]]; then
            log_error "域名信息不能为空。"
            exit 1
        fi
    fi
}

# --- 3. 显示摘要 ---
display_summary() {
    # 确定证书域名：IP > 手动指定 > 自动解析 > 默认
    if is_ip_address "$you_domain"; then
        format_cert_domain="${you_domain//[\[\]]/}"
        if [[ "$no_tls" != "yes" ]]; then
            log_info "检测到 IP 地址 (含 IPv6)，将申请 Let's Encrypt short-lived (短期) 证书。"
        fi
    elif [[ -n "$cert_domain" ]]; then
        format_cert_domain="$cert_domain"
    elif [[ "$parse_cert_domain" == "yes" && "$you_domain" == *.*.* ]]; then
        format_cert_domain="${you_domain#*.}"
        else
        format_cert_domain="${cert_domain:-$you_domain}"
    fi

    # 确定解析器
    if [[ -n "$manual_resolver" ]]; then
        resolver="$manual_resolver valid=60s"
    else
        # 修正: has_ipv6 返回 exit code, 不输出文本
        local ipv6_flag=$(has_ipv6 && echo "" || echo "ipv6=off")
        resolver="$(get_resolver_host) $ipv6_flag"
    fi

    local protocol=$(get_protocol "$no_tls")
    local r_protocol=$(get_protocol "$r_http_frontend")

    echo -e "\n${BLUE}🔧 Nginx 反代配置摘要${NC}"
    echo "──────────────────────────────────────────────"
    echo -e "➡️  前端访问: ${GREEN}${protocol}://${you_domain}:${you_frontend_port}${you_domain_path}${NC}"
    echo -e "⬅️  后端源站: ${YELLOW}${r_protocol}://${r_domain}:${r_frontend_port}${r_domain_path}${NC}"
    echo "──────────────────────────────────────────────"
    echo -e "📜 证书域名: ${format_cert_domain}"
    echo -e "🔒 TLS 状态: $([[ "$no_tls" == "yes" ]] && echo "${RED}禁用 (HTTP Only)${NC}" || echo "${GREEN}启用 (HTTPS)${NC}")"
    echo -e "🧠 DNS 解析: ${resolver}"
    echo -e "🌏 配置文件源: ${CONF_HOME}"
    echo "──────────────────────────────────────────────"
}

# --- 4. 依赖安装 ---
install_dependencies() {
    local OS_NAME PM GNUPG_PM

    if [ -f /etc/os-release ]; then
        source /etc/os-release
    else
        log_error "无法读取 /etc/os-release，不支持的系统。"
        exit 1
    fi

    # 严格按照原版 deploy.sh 的 case 逻辑，确保变量赋值一致
    case "$ID" in
      debian|devuan|kali) OS_NAME='debian'; PM='apt-get'; GNUPG_PM='gnupg2' ;;
      ubuntu) OS_NAME='ubuntu'; PM='apt-get'; GNUPG_PM=$([[ ${VERSION_ID%%.*} -lt 22 ]] && echo "gnupg2" || echo "gnupg") ;;
      centos|fedora|rhel|almalinux|rocky|amzn) OS_NAME='rhel'; PM=$(command -v dnf >/dev/null && echo "dnf" || echo "yum") ;;
      arch|archarm) OS_NAME='arch'; PM='pacman' ;;
      alpine) OS_NAME='alpine'; PM='apk' ;;
      *) echo "错误: 不支持的操作系统 '$ID'。" >&2; exit 1 ;;
    esac

    log_info "检查 Nginx..."
    if ! command -v nginx &> /dev/null; then
        log_info "Nginx 未安装，正在从官方源为 '$OS_NAME' 安装..."

        case "$OS_NAME" in
          debian|ubuntu)
              $SUDO "$PM" update
              $SUDO "$PM" install -y "$GNUPG_PM" ca-certificates lsb-release "${OS_NAME}-keyring"
              curl -sL https://nginx.org/keys/nginx_signing.key | $SUDO gpg --dearmor -o /usr/share/keyrings/nginx-archive-keyring.gpg
              echo "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.gpg] http://nginx.org/packages/mainline/$OS_NAME `lsb_release -cs` nginx" | $SUDO tee /etc/apt/sources.list.d/nginx.list > /dev/null
              echo -e "Package: *\nPin: origin nginx.org\nPin: release o=nginx\nPin-Priority: 900" | $SUDO tee /etc/apt/preferences.d/99nginx > /dev/null
              $SUDO "$PM" update
              $SUDO "$PM" install -y nginx
              $SUDO mkdir -p /etc/systemd/system/nginx.service.d
              echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" | $SUDO tee /etc/systemd/system/nginx.service.d/override.conf > /dev/null
              $SUDO systemctl daemon-reload
              $SUDO rm -f /etc/nginx/conf.d/default.conf
              $SUDO systemctl restart nginx
              ;;
          rhel)
              $SUDO "$PM" install -y yum-utils
              echo -e "[nginx-mainline]\nname=NGINX Mainline Repository\nbaseurl=https://nginx.org/packages/mainline/centos/\$releasever/\$basearch/\ngpgcheck=1\nenabled=1\ngpgkey=https://nginx.org/keys/nginx_signing.key" | $SUDO tee /etc/yum.repos.d/nginx.repo > /dev/null
              $SUDO "$PM" install -y nginx
              $SUDO mkdir -p /etc/systemd/system/nginx.service.d
              echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" | $SUDO tee /etc/systemd/system/nginx.service.d/override.conf > /dev/null
              $SUDO systemctl daemon-reload
              $SUDO rm -f /etc/nginx/conf.d/default.conf
              $SUDO systemctl restart nginx
              ;;
          arch)
              $SUDO "$PM" -Sy --noconfirm nginx-mainline
              $SUDO mkdir -p /etc/systemd/system/nginx.service.d
              echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" | $SUDO tee /etc/systemd/system/nginx.service.d/override.conf > /dev/null
              $SUDO systemctl daemon-reload
              $SUDO rm -f /etc/nginx/conf.d/default.conf
              $SUDO systemctl restart nginx
              ;;
          alpine)
              $SUDO "$PM" update
              $SUDO "$PM" add --no-cache nginx
              $SUDO rc-update add nginx default
              $SUDO rm -f /etc/nginx/conf.d/default.conf
              $SUDO rc-service nginx restart
              ;;
        esac
        log_success "Nginx 安装完成。"
    else
        log_info "Nginx 已安装。"
    fi

    # 补充安装依赖工具 (socat 等)
    if ! command -v socat &>/dev/null; then
        log_info "安装 socat 等辅助工具..."
        case "$OS_NAME" in
            debian|ubuntu|arch) $SUDO "$PM" install -y socat ;;
            *) $SUDO "$PM" install -y socat ;;
        esac
    fi

    if ! command -v crontab &>/dev/null; then
        log_info "检测到 crontab 缺失，正在安装 cron..."
        case "$OS_NAME" in
            debian|ubuntu) $SUDO "$PM" install -y cron ;;
            rhel) $SUDO "$PM" install -y cronie ;;
            arch) $SUDO "$PM" -S --noconfirm cronie ;;
            alpine) $SUDO "$PM" add --no-cache dcron ;;
        esac
    fi

    # acme.sh 安装逻辑
    ACME_SH="$HOME/.acme.sh/acme.sh"
    if [[ "$no_tls" != "yes" && ! -f "$ACME_SH" ]]; then
       log_info "正在为当前用户安装 acme.sh... (URL: $ACME_INSTALL_URL)"
       local TMP_INSTALL_SCRIPT="./acme.sh"
       trap "rm -f '$TMP_INSTALL_SCRIPT'" RETURN
       
       if download_with_verify "$ACME_INSTALL_URL" "$TMP_INSTALL_SCRIPT" "acme.sh"; then
           if sh "$TMP_INSTALL_SCRIPT" --install-online; then
               log_success "acme.sh 安装完成。"
               "$ACME_SH" --upgrade --auto-upgrade
               "$ACME_SH" --set-default-ca --server letsencrypt
           else
               log_error "acme.sh 安装脚本执行失败。"
               exit 1
           fi
       else
           exit 1
       fi
    fi
}

# --- 获取模板内容 ---
get_template_content() {
    if [[ -n "$template_domain_config_source" ]]; then
        if [[ "$template_domain_config_source" == http* ]]; then
            curl -sL "$template_domain_config_source"
        elif [ -f "$template_domain_config_source" ]; then
            cat "$template_domain_config_source"
        else
            log_error "指定的模板无效。"
            return 1
        fi
    else
        local tpl_name=$([[ "$no_tls" == "yes" ]] && echo "p.example.com.no_tls.conf" || echo "p.example.com.conf")
        log_info "下载模板: $tpl_name (源: $CONF_HOME/conf.d/$tpl_name)..."
        curl -sL "$CONF_HOME/conf.d/$tpl_name"
    fi
}

# --- 5. 生成配置 ---
generate_nginx_config() {
    log_info "准备生成 Nginx 配置文件..."

    local main_conf="/etc/nginx/nginx.conf"
    if [ ! -f "$main_conf" ] || grep -q "include /etc/nginx/conf.d/\*.conf;" "$main_conf"; then
        backup_file "$main_conf"
        log_info "更新主配置文件 $main_conf..."
        if ! curl -sL "$CONF_HOME/nginx.conf" | $SUDO tee "$main_conf" > /dev/null; then
            log_error "下载 nginx.conf 失败，请检查网络或代理设置。"
            exit 1
        fi
    fi

    local template_content
    template_content=$(get_template_content) || exit 1
    [[ -z "$template_content" ]] && { log_error "获取配置模板失败。"; exit 1; }

    export you_domain_path_rewrite=""
    if [[ -n "$you_domain_path" && "$you_domain_path" != "/" ]]; then
        local target_path="${r_domain_path:-/}"
        export you_domain_path_rewrite="rewrite ^${you_domain_path}(.*)\$ ${target_path}\$1 break;"
    fi

    export you_domain you_frontend_port resolver format_cert_domain
    export you_domain_path="${you_domain_path:-/}"
    
    local r_proto=$(get_protocol "$r_http_frontend")
    local r_port_str=$([[ -n "$r_frontend_port" ]] && echo ":$r_frontend_port" || echo "")
    export r_domain_full="${r_proto}://${r_domain}${r_port_str}"

    local vars='$you_domain $you_frontend_port $resolver $format_cert_domain $you_domain_path $you_domain_path_rewrite $r_domain_full'

    local clean_you_domain="${you_domain//[\[\]]/}"
    local conf_filename="${clean_you_domain}.${you_frontend_port}.conf"
    local conf_path="/etc/nginx/conf.d/$conf_filename"

    backup_file "$conf_path"
    
    echo "$template_content" | envsubst "$vars" | $SUDO tee "$conf_path" > /dev/null
    log_success "配置文件已生成: $conf_path"
}

# --- 6. 证书申请 ---
issue_certificate() {
    if [[ "$no_tls" == "yes" ]]; then 
        log_info "检测到非 TLS 配置，跳过证书申请步骤。"
        return 
    fi

    ACME_SH="$HOME/.acme.sh/acme.sh"
    # 直接使用 format_cert_domain (无括号) 构建路径
    local cert_path_base="/etc/nginx/certs/$format_cert_domain"
    local reload_cmd="$SUDO nginx -s reload"
    
    local issue_extra_args=""
    
    # 针对 IP 证书 (含 IPv6) 的特殊处理
    local is_ip=false

    if is_ip_address "$you_domain"; then
        is_ip=true
        log_info "检测到 IP 地址，将配置为 short-lived (短期) 证书模式..."
        [[ -n "$dns_provider" ]] && { log_warn "IP 证书不支持 DNS 验证，已自动切换为 Standalone 模式。"; dns_provider=""; }
        issue_extra_args="--certificate-profile shortlived --days 6"
    fi

    # 检查证书是否已存在 (使用 format_cert_domain 查询)
    if ! "$ACME_SH" --info -d "$format_cert_domain" --ecc 2>/dev/null | grep -q RealFullChainPath; then
        log_info "证书不存在，开始申请..."
        $SUDO mkdir -p "$cert_path_base"

        if [[ -n "$dns_provider" ]]; then
            issue_certificate_dns
        else
            issue_certificate_standalone "$is_ip"
        fi
        log_success "证书申请成功。"
    else
        log_info "证书已由 acme.sh 管理，将跳过申请步骤，直接进行安装/更新。"
    fi

    # 安装证书
    $SUDO mkdir -p "$cert_path_base"
    log_info "正在安装证书到 Nginx 目录..."
    # 使用 format_cert_domain (无括号) 安装
    "$ACME_SH" --install-cert -d "$format_cert_domain" --ecc \
        --fullchain-file "$cert_path_base/cert" \
        --key-file "$cert_path_base/key" \
        --reloadcmd "$reload_cmd"
    
    log_success "证书安装并部署完成。"
}

# --- 证书申请：DNS 模式 ---
issue_certificate_dns() {
    local dns_arg="dns_${dns_provider}"
    # 使用 format_cert_domain
    local domains_arg="-d $format_cert_domain"
    
    # 泛域名逻辑：如果不是 IP 且与 you_domain 不同（通常不会触发，因为 display_summary 已经处理了 logic）
    # 但为了兼容逻辑，保留判断。注意 format_cert_domain 是纯净的。
    [[ "$format_cert_domain" != "$you_domain" && ! $(is_ip_address "$you_domain") ]] && domains_arg="$domains_arg -d *.$format_cert_domain"
    
    if [[ "$dns_provider" == "cf" ]]; then
        [[ -n "$cf_token" ]] && export CF_Token="$cf_token"
        [[ -n "$cf_account_id" ]] && export CF_Account_ID="$cf_account_id"
        
        if [[ -z "$CF_Token" || -z "$CF_Account_ID" ]] && [ -t 0 ]; then
            echo -e "${YELLOW}请输入 Cloudflare API 凭据:${NC}"
            read -rp "Token: " CF_Token
            read -rp "Account ID: " CF_Account_ID
            export CF_Token CF_Account_ID
        fi
    fi

    log_info "使用 DNS 模式 ($dns_provider) 申请证书..."
    "$ACME_SH" --issue --dns "$dns_arg" $domains_arg --keylength ec-256 || {
        log_error "证书申请失败。"
        exit 1
    }
}

# --- 证书申请：Standalone 模式 (支持 IPv6) ---
issue_certificate_standalone() {
    local is_ip_mode="$1"
    
    # 泛域名检查：如果不是 IP，且 format_cert_domain 不等于 you_domain (说明是 *.xxx)，则不能用 standalone
    if [[ "$is_ip_mode" != "true" && "$format_cert_domain" != "$you_domain" ]]; then
        log_error "泛域名证书必须使用 DNS 模式申请。"
        exit 1
    fi

    log_info "使用 Standalone 模式申请证书..."
    
    # 针对 IPv6，acme.sh 需要纯 IP 地址 (不带方括号)
    # 针对普通域名，保留原样
    local acme_domain="$you_domain"
    local listen_arg=""
    
    if [[ "$is_ip_mode" == "true" ]]; then
        # 针对 IPv6 添加 --listen-v6
        if [[ "$you_domain" =~ : ]]; then
            listen_arg="--listen-v6"
            log_info "检测到 IPv6 地址，添加 --listen-v6 参数..."
        fi
    fi
    
    # 使用 format_cert_domain (无括号) 进行申请
    # 添加 --force 以防止 key 已存在导致的错误
    if ! "$ACME_SH" --issue --standalone -d "$format_cert_domain" --keylength ec-256 $issue_extra_args $listen_arg; then
        log_error "证书申请失败。请检查域名/IP解析是否正确，或防火墙是否放行 80 端口。"
        exit 1
    fi
}

# --- 7. 移除配置 ---
remove_domain_config() {
    local remove_url="$domain_to_remove"
    log_info "正在为 '$remove_url' 查找相关配置..."

    # 精确解析域名和端口
    # 注意：parse_url 返回格式为 proto|domain|port|path
    local domain port temp_path temp_proto
    IFS='|' read -r temp_proto domain port temp_path < <(parse_url "$remove_url")

    # 处理 IPv6 域名中的方括号 (用于匹配文件名)
    local clean_domain="${domain//[\[\]]/}"

    # 如果未解析出协议，则假定为 https
    if [[ -z "$temp_proto" ]]; then
        temp_proto="https"
    fi

    # 根据协议决定默认端口
    if [[ "$temp_proto" == "https" ]]; then
        port="${port:-443}"
    else
        port="${port:-80}"
    fi

    # 构造精确的配置文件名 (使用 clean_domain)
    local nginx_conf_file="/etc/nginx/conf.d/${clean_domain}.${port}.conf"

    if ! $SUDO [ -f "$nginx_conf_file" ]; then
        log_error "未找到与 '$domain' ($clean_domain) 在端口 '$port' 上的 Nginx 配置文件: $nginx_conf_file"
        # 找不到文件时，不强制退出，可能用户只是想清理残留证书，或者文件已经被删了一部分
        # return 1 
        # 但为了逻辑严谨，若连配置文件都没有，后续的逻辑依据也没了，这里还是退出比较好。
        exit 1
    fi

    # 智能判断是否使用 TLS
    local uses_tls="no"
    local remove_cert_domain=""
    local cert_dir=""

    if $SUDO grep -q "ssl_certificate" "$nginx_conf_file"; then
        uses_tls="yes"
        # [优化] 从 Nginx 配置中直接推断证书域名
        local cert_full_path
        cert_full_path=$($SUDO awk "/ssl_certificate / {print \$2}" "$nginx_conf_file" | head -n 1 | sed 's/;//')
        local cert_parent_dir
        cert_parent_dir=$(dirname "$cert_full_path")
        remove_cert_domain=$(basename "$cert_parent_dir")
        cert_dir="/etc/nginx/certs/$remove_cert_domain"
    fi

    echo "--------------------------------------------------------"
    echo -e "${RED}警告: 即将执行破坏性操作！${NC}"
    echo "将要为 '$domain' (端口: $port) 移除以下内容:"
    echo "  - Nginx 配置文件: $nginx_conf_file"

    local is_wildcard_setup="no"
    # 如果证书目录名与当前域名(去括号后)不一致，通常认为是共享/泛域名
    if [[ "$uses_tls" == "yes" && "$clean_domain" != "$remove_cert_domain" ]]; then
        is_wildcard_setup="yes"
    fi

    if [[ "$uses_tls" == "yes" ]]; then
        if [[ "$is_wildcard_setup" == "no" ]]; then
            if [ -d "$cert_dir" ]; then
                echo "  - Nginx 证书目录: $cert_dir"
            fi
            ACME_SH="$HOME/.acme.sh/acme.sh"
            if [ -f "$ACME_SH" ]; then
                 echo "  - acme.sh 证书记录 (针对域名: $remove_cert_domain)"
            fi
        else
            echo -e "${YELLOW}  - 注意: 检测到泛域名或共享证书配置 ($remove_cert_domain)，将不会删除共享的证书文件。${NC}"
        fi
    fi
    echo "--------------------------------------------------------"

    # [修正] 智能确认流程
    if [ ! -t 0 ]; then # 非交互模式
        if [[ "$force_yes" != "yes" ]]; then
            log_error "在非交互模式下，移除操作必须使用 '-Y' 或 '--yes' 参数进行确认。"
            exit 1
        fi
        log_info "检测到 '--yes' 参数，将自动执行移除操作。"
    else # 交互模式
        if [[ "$force_yes" != "yes" ]]; then
            read -rp "此操作不可逆，请输入 'yes' 确认移除: " confirmation
            if [[ "$confirmation" != "yes" ]]; then
                log_info "操作已取消。"
                exit 0
            fi
        fi
    fi

    log_info "开始移除..."
    $SUDO rm -f "$nginx_conf_file"
    log_info "Nginx 配置文件已删除。"

    if [[ "$uses_tls" == "yes" ]]; then
        if [[ "$is_wildcard_setup" == "no" ]]; then
            if [ -d "$cert_dir" ]; then
                $SUDO rm -rf "$cert_dir"
                log_info "Nginx 证书目录已删除。"
            fi

            ACME_SH="$HOME/.acme.sh/acme.sh"
            if [ -f "$ACME_SH" ]; then
                # 使用 remove_cert_domain (它来自配置文件路径，最准确)
                "$ACME_SH" --remove -d "$remove_cert_domain" --ecc >/dev/null 2>&1 || log_warn "从 acme.sh 移除证书失败，可能记录已不存在。"
                log_info "acme.sh 证书记录已移除。"
            fi
        else
            log_info "证书目录和 acme.sh 记录未被删除。"
            echo "如果您确认不再需要此证书，请手动执行以下命令进行清理："
            echo "  $HOME/.acme.sh/acme.sh --remove -d '$remove_cert_domain' --ecc"
            echo "  $SUDO rm -rf '$cert_dir'"
        fi
    fi

    log_info "正在检查 Nginx 配置并执行重载..."
    if test_and_reload_nginx; then
        log_success "域名 '$domain' 的相关配置已成功移除！"
    else
        log_error "Nginx 配置测试失败，请检查配置文件。"
    fi
}

# ===================================================================================
#                                 主流程
# ===================================================================================

test_and_reload_nginx() {
    log_info "测试 Nginx 配置..."
    if $SUDO nginx -t; then
        # 增加判断，如果 nginx 没运行，尝试启动而不是 reload
        if pgrep -x "nginx" >/dev/null; then
            $SUDO nginx -s reload
        else
            if command -v systemctl >/dev/null; then
                $SUDO systemctl restart nginx
            else
                $SUDO rc-service nginx restart
            fi
        fi
        return 0
    else
        log_error "Nginx 配置测试失败。"
        return 1
    fi
}

main() {
    parse_arguments "$@"

    if [[ -n "$domain_to_remove" ]]; then
        remove_domain_config
        exit 0
    fi

    setup_env
    prompt_interactive_mode
    display_summary
    install_dependencies
    generate_nginx_config
    issue_certificate
    
    if test_and_reload_nginx; then
        log_success "部署成功！"
        local protocol=$(get_protocol "$no_tls")
        echo -e "${GREEN}访问地址: ${protocol}://${you_domain}:${you_frontend_port}${you_domain_path}${NC}"
    else
        exit 1
    fi
}

main "$@"