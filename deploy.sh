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
    # 这样写，简单的文本查找替换工具无法匹配到完整的 URL，因此不会被改写
    local GH_RAW_HOST="raw.githubusercontent.com"
    local URL_PREFIX="https://${GH_RAW_HOST}"
    
    local RAW_URL_BASE="${URL_PREFIX}/sakullla/nginx-reverse-emby/main"
    local ACME_OFFICIAL_RAW="${URL_PREFIX}/acmesh-official/acme.sh/master/acme.sh"
    
    # 确定代理地址: 命令行参数 > 环境变量 > 自动检测
    local effective_gh_proxy=""
    
    if [[ -n "$manual_gh_proxy" ]]; then
        effective_gh_proxy="$manual_gh_proxy"
    elif [[ -n "$GH_PROXY" ]]; then
        effective_gh_proxy="$GH_PROXY"
    elif is_in_china; then
        # 默认使用 gh.llkk.cc 代理
        effective_gh_proxy="https://gh.llkk.cc/"
    fi

    # 确保代理地址以 / 结尾 (如果非空)
    if [[ -n "$effective_gh_proxy" && "$effective_gh_proxy" != */ ]]; then
        effective_gh_proxy="${effective_gh_proxy}/"
    fi

    if [[ -n "$effective_gh_proxy" ]]; then
        echo -e "${BLUE}[INFO]${NC} 使用 GitHub 代理: ${effective_gh_proxy}"
        
        # 再次检查 RAW_URL_BASE 是否以官方前缀开头
        # 理论上因为上面的拼接写法，这里一定是纯净的，可以直接叠加代理
        if [[ "$RAW_URL_BASE" == "${URL_PREFIX}"* ]]; then
            CONF_HOME="${effective_gh_proxy}${RAW_URL_BASE}"
        else
            # 极少数情况：如果前缀变量都被改写了，那就直接用，不叠加
            CONF_HOME="${RAW_URL_BASE}"
        fi
        
        if [[ "$ACME_OFFICIAL_RAW" == "${URL_PREFIX}"* ]]; then
            ACME_INSTALL_URL="${effective_gh_proxy}${ACME_OFFICIAL_RAW}"
        else
            ACME_INSTALL_URL="${ACME_OFFICIAL_RAW}"
        fi
    else
        echo -e "${BLUE}[INFO]${NC} 未使用 GitHub 代理，使用默认源..."
        CONF_HOME="${RAW_URL_BASE}"
        # 如果不使用代理，通常推荐使用 get.acme.sh 官方短链
        ACME_INSTALL_URL="https://get.acme.sh"
    fi

    readonly CONF_HOME
    readonly BACKUP_DIR="/etc/nginx/backup"
    readonly ACME_INSTALL_URL
}

# ===================================================================================
#                                 辅助函数
# ===================================================================================

# --- 日志函数 (仅输出到屏幕) ---
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

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

一个强大且安全的 Nginx 反向代理部署脚本 (支持 sudo)。

部署选项:
  -y, --you-domain <域名或URL>   你的访问域名或完整 URL (例如: https://app.example.com 或 http://1.2.3.4)
  -r, --r-domain <域名或URL>     被代理的后端地址 (例如: http://127.0.0.1:8096)
  -m, --cert-domain <域名>       (可选) 手动指定 SSL 证书的主域名，用于泛域名证书。
  -d, --parse-cert-domain        (可选) 自动从 -y 域名中提取根域名作为证书域名。
  -D, --dns <provider>           (可选) 使用 DNS API 模式申请证书 (例如: cf)。泛域名必须使用此项。
  -R, --resolver <DNS服务器>      (可选) 手动指定 DNS 解析服务器 (例如: "8.8.8.8 1.1.1.1")
  -c, --template <路径或URL>      (可选) 指定自定义 Nginx 配置文件模板。
  --gh-proxy <URL>               (可选) 指定 GitHub 加速代理 (例如: https://gh.llkk.cc/)。
  --cf-token <TOKEN>             Cloudflare API Token (配合 --dns cf)。
  --cf-account-id <ID>           Cloudflare Account ID (配合 --dns cf)。

管理选项:
  --remove <域名或URL>            移除指定域名的 Nginx 配置和证书。
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

# --- URL 解析 ---
parse_url() {
    local url="$1"
    if [[ "$url" =~ ^(https?)://([^/:?#]+)(:([0-9]+))?(/[^?#]*)? ]]; then
        echo "${BASH_REMATCH[1]}|${BASH_REMATCH[2]}|${BASH_REMATCH[4]}|${BASH_REMATCH[5]}"
    else
        echo "$url|||"
    fi
}

process_url_input() {
    local full_url="$1"
    local domain_type="$2" # "you" or "r"

    if [[ -z "$full_url" ]]; then return; fi

    local temp_domain temp_path temp_port temp_proto
    IFS='|' read -r temp_proto temp_domain temp_port temp_path < <(parse_url "$full_url")

    if [[ -z "$temp_proto" ]]; then temp_proto="https"; fi 
    
    if [[ "$domain_type" == "you" ]]; then
        you_domain="$temp_domain"
        you_domain_path="$temp_path"
        if [[ "$temp_proto" == "http" ]]; then
            no_tls="yes"
            you_frontend_port="${temp_port:-80}"
        else
            no_tls="no"
            you_frontend_port="${temp_port:-443}"
        fi
    elif [[ "$domain_type" == "r" ]]; then
        r_domain="$temp_domain"
        r_domain_path="$temp_path"
        if [[ "$temp_proto" == "http" ]]; then
            r_http_frontend="yes"
            r_frontend_port="${temp_port:-80}"
        else
            r_http_frontend="no"
            r_frontend_port="${temp_port:-443}"
        fi
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
        read -rp "请输入你的访问 URL (例如 https://emby.mysite.com): " input_you
        read -rp "请输入后端 Emby URL (例如 http://127.0.0.1:8096): " input_r

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
    # [优化] 逻辑合并：优先级 IP > 手动指定 > 自动解析 > 默认
    if [[ "$you_domain" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
        # 1. 如果是 IP 地址
        format_cert_domain="$you_domain"
        if [[ "$no_tls" != "yes" ]]; then
            log_info "检测到 IP 地址 ($you_domain)，将申请 Let's Encrypt short-lived (短期) 证书。"
        fi
    elif [[ -n "$cert_domain" ]]; then
        # 2. 如果手动指定了证书域名 (-m)
        format_cert_domain="$cert_domain"
    elif [[ "$parse_cert_domain" == "yes" ]]; then
        # 3. 如果开启了自动解析 (-d)
        if [[ "$you_domain" == *.*.* ]]; then
             format_cert_domain="${you_domain#*.}"
        else
             format_cert_domain="$you_domain"
        fi
    else
        # 4. 默认情况
        format_cert_domain="$you_domain"
    fi

    if [[ -n "$manual_resolver" ]]; then
        resolver="$manual_resolver valid=60s"
    else
        local ipv6_flag=""
        if ! has_ipv6; then ipv6_flag="ipv6=off"; fi
        resolver="$(get_resolver_host) $ipv6_flag"
    fi

    local protocol=$([[ "$no_tls" == "yes" ]] && echo "http" || echo "https")
    # [修复] 显式判断后端协议，修复 http://no... 的显示错误
    local r_protocol=$([[ "$r_http_frontend" == "yes" ]] && echo "http" || echo "https")

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

# --- 4. 依赖安装 (完全还原原版 deploy.sh 逻辑) ---
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
            debian|ubuntu|arch) $SUDO "$PM" install -y socat cron ;;
            *) $SUDO "$PM" install -y socat cronie ;;
        esac
    fi

    # acme.sh 安装逻辑
    ACME_SH="$HOME/.acme.sh/acme.sh"
    if [[ "$no_tls" != "yes" && ! -f "$ACME_SH" ]]; then
       log_info "正在为当前用户安装 acme.sh..."
       log_info "使用安装URL: $ACME_INSTALL_URL"
       
       # [修改] 改为先下载脚本到临时文件，再执行安装，避免 curl 管道错误
       local TMP_INSTALL_SCRIPT="/tmp/acme_install.sh"
       if curl -fsL "$ACME_INSTALL_URL" -o "$TMP_INSTALL_SCRIPT"; then
           # 检查下载的文件是否包含 acme.sh 关键字，避免下载到 HTML 错误页
           if grep -q "acme.sh" "$TMP_INSTALL_SCRIPT"; then
                if sh "$TMP_INSTALL_SCRIPT" --install-online; then
                    log_success "acme.sh 安装完成。"
                    rm -f "$TMP_INSTALL_SCRIPT"
                    "$ACME_SH" --upgrade --auto-upgrade
                    # 默认使用 letsencrypt，支持 IP 证书 (2025+ policy)
                    "$ACME_SH" --set-default-ca --server letsencrypt
                else
                    log_error "acme.sh 安装脚本执行失败。"
                    rm -f "$TMP_INSTALL_SCRIPT"
                    exit 1
                fi
           else
               log_error "下载的 acme.sh 安装脚本内容异常，可能是网络问题或代理错误。"
               log_error "下载内容预览: $(head -n 5 "$TMP_INSTALL_SCRIPT")"
               rm -f "$TMP_INSTALL_SCRIPT"
               exit 1
           fi
       else
           log_error "无法下载 acme.sh 安装脚本，请检查网络连接。"
           exit 1
       fi
    fi
}

# --- 5. 生成配置 ---
generate_nginx_config() {
    log_info "准备生成 Nginx 配置文件..."

    local main_conf="/etc/nginx/nginx.conf"
    if [ ! -f "$main_conf" ] || grep -q "include /etc/nginx/conf.d/\*.conf;" "$main_conf"; then
        backup_file "$main_conf"
        log_info "更新主配置文件 $main_conf (源: $CONF_HOME/nginx.conf)..."
        if ! curl -sL "$CONF_HOME/nginx.conf" | $SUDO tee "$main_conf" > /dev/null; then
            log_error "下载 nginx.conf 失败，请检查网络或代理设置。"
            exit 1
        fi
    fi

    local template_content
    if [[ -n "$template_domain_config_source" ]]; then
        if [[ "$template_domain_config_source" == http* ]]; then
            template_content=$(curl -sL "$template_domain_config_source")
        elif [ -f "$template_domain_config_source" ]; then
            template_content=$(cat "$template_domain_config_source")
        else
            log_error "指定的模板无效。"
            exit 1
        fi
    else
        local tpl_name="p.example.com.conf"
        [[ "$no_tls" == "yes" ]] && tpl_name="p.example.com.no_tls.conf"
        
        log_info "下载模板: $tpl_name (源: $CONF_HOME/conf.d/$tpl_name)..."
        template_content=$(curl -sL "$CONF_HOME/conf.d/$tpl_name")
    fi

    if [[ -z "$template_content" ]]; then
        log_error "获取配置模板失败。"
        exit 1
    fi

    export you_domain_path_rewrite=""
    if [[ -n "$you_domain_path" && "$you_domain_path" != "/" ]]; then
        local target_path="${r_domain_path:-/}"
        export you_domain_path_rewrite="rewrite ^${you_domain_path}(.*)\$ ${target_path}\$1 break;"
    fi

    export you_domain you_frontend_port resolver format_cert_domain
    export you_domain_path="${you_domain_path:-/}"
    
    local r_proto=$([[ "$r_http_frontend" == "yes" ]] && echo "http" || echo "https")
    local r_port_str=$([[ -n "$r_frontend_port" ]] && echo ":$r_frontend_port" || echo "")
    export r_domain_full="${r_proto}://${r_domain}${r_port_str}"

    local vars='$you_domain $you_frontend_port $resolver $format_cert_domain $you_domain_path $you_domain_path_rewrite $r_domain_full'

    local conf_filename="${you_domain}.${you_frontend_port}.conf"
    local conf_path="/etc/nginx/conf.d/$conf_filename"

    backup_file "$conf_path"
    
    echo "$template_content" | envsubst "$vars" | $SUDO tee "$conf_path" > /dev/null
    log_success "配置文件已生成: $conf_path"
}

# --- 6. 证书申请 (还原 RealFullChainPath 逻辑) ---
issue_certificate() {
    if [[ "$no_tls" == "yes" ]]; then 
        log_info "检测到非 TLS 配置，跳过证书申请步骤。"
        return 
    fi

    ACME_SH="$HOME/.acme.sh/acme.sh"
    local cert_path_base="/etc/nginx/certs/$format_cert_domain"
    local reload_cmd="$SUDO nginx -s reload"
    
    local issue_extra_args=""
    # [新增] 针对 IP 证书的特殊处理：强制 short-lived 逻辑（清理 DNS 参数，强制 Standalone）
    if [[ "$you_domain" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
        log_info "检测到 IP 地址，将配置为 short-lived (短期) 证书模式..."
        if [[ -n "$dns_provider" ]]; then
            log_warn "IP 证书不支持 DNS 验证，已自动切换为 Standalone 模式。"
            dns_provider=""
        fi
        # 强制添加 IP 证书所需参数
        issue_extra_args="--certificate-profile shortlived --days 6"
    fi

    # 使用 grep -q RealFullChainPath 判断证书是否已签发
    if ! "$ACME_SH" --info -d "$format_cert_domain" --ecc 2>/dev/null | grep -q RealFullChainPath; then
        log_info "证书不存在，开始申请..."
        $SUDO mkdir -p "$cert_path_base"

        if [[ -n "$dns_provider" ]]; then
            # --- DNS 模式 ---
            local dns_arg="dns_${dns_provider}"
            local domains_arg="-d $format_cert_domain"
            if [[ "$format_cert_domain" != "$you_domain" ]]; then
                domains_arg="$domains_arg -d *.$format_cert_domain"
            fi
            
            if [[ "$dns_provider" == "cf" ]]; then
                if [[ -n "$cf_token" ]]; then export CF_Token="$cf_token"; fi
                if [[ -n "$cf_account_id" ]]; then export CF_Account_ID="$cf_account_id"; fi
                
                if [[ -z "$CF_Token" || -z "$CF_Account_ID" ]] && [ -t 0 ]; then
                    echo -e "${YELLOW}请输入 Cloudflare API 凭据:${NC}"
                    read -rp "Token: " CF_Token
                    read -rp "Account ID: " CF_Account_ID
                    export CF_Token CF_Account_ID
                fi
            fi

            log_info "使用 DNS 模式 ($dns_provider) 申请证书..."
            if ! "$ACME_SH" --issue --dns "$dns_arg" $domains_arg --keylength ec-256; then
                log_error "证书申请失败。"
                exit 1
            fi
        else
            # --- Standalone 模式 ---
            if [[ "$format_cert_domain" != "$you_domain" ]]; then
                log_error "泛域名证书必须使用 DNS 模式申请。"
                exit 1
            fi

            log_info "使用 Standalone 模式申请证书..."
            
            # 检测端口占用 (Robust)
            local nginx_stopped=0
            if lsof -i :80 | grep -q LISTEN || netstat -nlp | grep -q ':80 .*LISTEN'; then
                log_warn "端口 80 正在被占用，尝试停止 Nginx..."
                if lsof -i :80 | grep -q nginx || netstat -nlp | grep ':80 ' | grep -q nginx; then
                    $SUDO systemctl stop nginx || $SUDO service nginx stop
                    nginx_stopped=1
                    sleep 2
                else
                    log_error "端口 80 被非 Nginx 进程占用，请先释放端口。"
                    exit 1
                fi
            fi

            # [修改] 申请命令增加 issue_extra_args (仅在 IP 模式下有值)
            if ! "$ACME_SH" --issue --standalone -d "$you_domain" --keylength ec-256 $issue_extra_args; then
                log_error "证书申请失败。请检查域名/IP解析是否正确，或防火墙是否放行 80 端口。"
                if [ $nginx_stopped -eq 1 ]; then $SUDO systemctl start nginx; fi
                exit 1
            fi
            
            if [ $nginx_stopped -eq 1 ]; then
                log_info "恢复 Nginx..."
                $SUDO systemctl start nginx || $SUDO service nginx start
            fi
        fi
        log_success "证书申请成功。"
    else
        log_info "证书已由 acme.sh 管理，将跳过申请步骤，直接进行安装/更新。"
    fi

    # 安装证书
    log_info "正在安装证书到 Nginx 目录..."
    "$ACME_SH" --install-cert -d "$format_cert_domain" --ecc \
        --fullchain-file "$cert_path_base/cert" \
        --key-file "$cert_path_base/key" \
        --reloadcmd "$reload_cmd"
    
    log_success "证书安装并部署完成。"
}

# --- 7. 移除配置 ---
remove_domain_config() {
    local target="$domain_to_remove"
    log_info "准备移除: $target"

    local temp_domain temp_port
    IFS='|' read -r _ temp_domain temp_port _ < <(parse_url "$target")
    
    # 根据是否指定端口来精确匹配配置文件
    local conf_pattern
    if [[ -n "$temp_port" ]]; then
        # 指定了端口，精确匹配
        conf_pattern="/etc/nginx/conf.d/${temp_domain}.${temp_port}.conf"
        log_info "精确匹配: ${temp_domain}:${temp_port}"
    else
        # 未指定端口，匹配所有该域名的配置
        conf_pattern="/etc/nginx/conf.d/${temp_domain}.*.conf"
        log_info "匹配该域名的所有端口"
    fi
    
    local conf_files
    conf_files=$(ls $conf_pattern 2>/dev/null || true)

    if [[ -z "$conf_files" ]]; then
        if [[ -n "$temp_port" ]]; then
            log_warn "未找到与 $temp_domain:$temp_port 相关的配置文件。"
        else
            log_warn "未找到与 $temp_domain 相关的配置文件。"
        fi
        exit 0
    fi

    echo -e "${YELLOW}将移除以下文件:${NC}"
    echo "$conf_files"
    
    if [[ "$force_yes" != "yes" ]]; then
        read -rp "确认移除? [y/N] " confirm
        if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
            log_info "操作取消。"
            exit 0
        fi
    fi

    for f in $conf_files; do
        $SUDO rm -f "$f"
        log_success "已删除: $f"
    done

    log_warn "证书文件可能位于 /etc/nginx/certs/ 下，请根据需要手动清理。"
    
    $SUDO nginx -t && $SUDO nginx -s reload
    log_success "配置移除完成，Nginx 已重载。"
}

# ===================================================================================
#                                 主流程
# ===================================================================================

main() {
    parse_arguments "$@"

    if [[ -n "$domain_to_remove" ]]; then
        remove_domain_config
        exit 0
    fi

    # 调用环境设置，因为依赖解析后的参数
    setup_env

    prompt_interactive_mode
    display_summary
    install_dependencies
    generate_nginx_config
    issue_certificate
    
    log_info "测试 Nginx 配置..."
    if $SUDO nginx -t; then
        $SUDO nginx -s reload
        log_success "部署成功！"
        local protocol=$([[ "$no_tls" == "yes" ]] && echo "http" || echo "https")
        echo -e "${GREEN}访问地址: ${protocol}://${you_domain}:${you_frontend_port}${you_domain_path}${NC}"
    else
        log_error "Nginx 配置测试失败。"
        exit 1
    fi
}

main "$@"