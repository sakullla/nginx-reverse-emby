#!/bin/bash

# ===================================================================================
#
#           Nginx Reverse Proxy Deployment Script (Sudo-aware & Feature-complete)
#
# ===================================================================================

# --- 脚本严格模式 ---
# set -e: 当任何命令失败时立即退出
# set -o pipefail: 管道中任何一个命令失败，整个管道都算失败
set -e
set -o pipefail

# --- 全局常量与变量 ---
readonly CONF_HOME="https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main"
SUDO='' # 将根据用户权限动态设置

# --- 权限检查与 Sudo 设置 ---
if [ "$(id -u)" -ne 0 ]; then
    if ! command -v sudo >/dev/null; then
        echo "错误: 此脚本需要以 root 权限运行，或者必须安装 'sudo'。" >&2
        exit 1
    fi
    SUDO='sudo'
    echo "信息: 检测到非 root 用户，将使用 'sudo' 获取权限。"
fi

# ===================================================================================
#                                 辅助函数定义
# ===================================================================================

# --- 错误处理函数 ---
handle_error() {
    local exit_code=$?
    local line_number=$1
    echo >&2
    echo "--------------------------------------------------------" >&2
    echo "错误: 脚本在第 $line_number 行意外中止。" >&2
    echo "退出码: $exit_code" >&2
    echo "--------------------------------------------------------" >&2
    exit "$exit_code"
}

# 注册错误处理的 trap
trap 'handle_error $LINENO' ERR

# --- 帮助信息函数 ---
show_help() {
    cat << EOF
用法: $(basename "$0") [选项]

一个强大且安全的 Nginx 反向代理部署脚本 (支持 sudo)。

选项:
  -y, --you-domain <域名或URL>   你的域名或完整 URL (例如: https://app.example.com/emby)
  -r, --r-domain <域名或URL>     反代 Emby 的域名或完整 URL (例如: http://127.0.0.1:8096)
  -m, --cert-domain <域名>       手动指定用于 SSL 证书的域名 (例如: example.com)，用于泛域名证书。优先级最高。
  -d, --parse-cert-domain         自动从访问域名中解析出根域名作为证书域名 (例如: 从 app.example.com 解析出 example.com)。
  -D, --dns <provider>            使用 DNS API 模式申请证书 (例如: cf)。这是申请泛域名证书的【必须】选项。
  -R, --resolver <DNS服务器>      手动指定 DNS 解析服务器 (例如: "8.8.8.8 1.1.1.1")
  -h, --help                      显示此帮助信息

EOF
    exit 0
}

# --- 网络和系统检测函数 ---
is_in_china() {
    if [ -z "$_loc" ]; then
        if ! _loc=$(curl -m 5 -sL http://www.qualcomm.cn/cdn-cgi/trace | grep '^loc=' | cut -d= -f2); then
            echo "警告: 无法确定地理位置，将使用默认 DNS。" >&2
            return 1
        fi
        echo "信息: 检测到地理位置为 $_loc。" >&2
    fi
    [ "$_loc" = CN ]
}

has_ipv6() {
    ip -6 addr show scope global | grep -q inet6
}

get_system_dns() {
    awk '/^nameserver/ { print ($2 ~ /:/ ? "["$2"]" : $2) }' /etc/resolv.conf | xargs
}

get_default_dns() {
    if is_in_china; then
        echo "223.5.5.5 119.29.29.29"
    else
        echo "1.1.1.1 8.8.8.8"
    fi
}

get_resolver_host() {
    local system_dns
    system_dns=$(get_system_dns)
    if [[ -n "$system_dns" ]]; then
        echo "$system_dns"
    else
        echo "$(get_default_dns)"
    fi
}

get_ipv6_flag() {
    if has_ipv6; then
        echo ""
    else
        echo "ipv6=off"
    fi
}

# --- URL 解析函数 (重构版，不再使用 eval) ---
parse_url() {
    local url="$1"
    # 此函数将通过 echo 输出结果，格式为: host|path|port|proto
    if [[ "$url" =~ ^(https?)://([^/:?#]+)(:([0-9]+))?(/[^?#]*)? ]]; then
        local proto="${BASH_REMATCH[1]}"
        local host="${BASH_REMATCH[2]}"
        local port="${BASH_REMATCH[4]}"
        local path="${BASH_REMATCH[5]}"
        echo "$host|$path|$port|$proto"
    else
        # 如果不匹配 URL，则假定整个字符串为域名
        echo "$url|||"
    fi
}

# ===================================================================================
#                                 核心逻辑函数
# ===================================================================================

# --- 1. 解析命令行参数 ---
parse_arguments() {
    # 初始化变量
    you_domain_full=""
    r_domain_full=""
    cert_domain=""
    manual_resolver=""
    parse_cert_domain="no"
    dns_provider=""
    you_domain=""; you_domain_path=""; you_frontend_port=""; no_tls=""
    r_domain=""; r_domain_path=""; r_frontend_port=""; r_http_frontend=""

    local TEMP
    TEMP=$(getopt -o y:r:m:R:dD:h --long you-domain:,r-domain:,cert-domain:,resolver:,parse-cert-domain,dns:,help -n "$(basename "$0")" -- "$@")
    if [ $? -ne 0 ]; then
        echo "错误: 参数解析失败。" >&2
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
            -h|--help) show_help; shift ;;
            --) shift; break ;;
            *) echo "错误: 未知参数 $1" >&2; exit 1 ;;
        esac
    done

    # 使用新的 parse_url 函数和 read 来安全地赋值
    if [[ -n "$you_domain_full" ]]; then
        local temp_port temp_proto
        IFS='|' read -r you_domain you_domain_path temp_port temp_proto < <(parse_url "$you_domain_full")
        if [[ "$temp_proto" == "http" ]]; then no_tls="yes"; else no_tls="no"; fi
        if [[ "$temp_proto" == "https" ]]; then you_frontend_port="${temp_port:-443}"; else you_frontend_port="${temp_port:-80}"; fi
    fi
    if [[ -n "$r_domain_full" ]]; then
        local temp_port temp_proto
        IFS='|' read -r r_domain r_domain_path temp_port temp_proto < <(parse_url "$r_domain_full")
        if [[ "$temp_proto" == "http" ]]; then r_http_frontend="yes"; else r_http_frontend="no"; fi
        if [[ "$temp_proto" == "https" ]]; then r_frontend_port="${temp_port:-443}"; else r_frontend_port="${temp_port:-80}"; fi
    fi
}

# --- 2. 交互模式 ---
prompt_interactive_mode() {
    if [[ -z "$you_domain" || -z "$r_domain" ]]; then
        # 检查是否在交互式终端中运行
        if [ ! -t 0 ]; then
            echo "--------------------------------------------------------" >&2
            echo -e "\e[1;31m错误: 无法进入交互模式。\e[0m" >&2
            echo "此脚本似乎是通过管道 (pipe) 执行的，无法读取键盘输入。" >&2
            echo "如果您想使用交互模式，请使用以下推荐命令：" >&2
            echo -e "\e[1;32mbash <(curl -sSL [脚本URL])\e[0m" >&2
            echo "--------------------------------------------------------" >&2
            exit 1
        fi

        echo -e "\n--- 交互模式: 配置反向代理 ---"
        local input_you_domain_full input_r_domain_full
        read -p "你的访问 URL (例如 https://app.your-domain.com): " input_you_domain_full
        read -p "被代理的 Emby URL (例如 http://127.0.0.1:8096): " input_r_domain_full

        if [[ -n "$input_you_domain_full" ]]; then
            local temp_port temp_proto
            IFS='|' read -r you_domain you_domain_path temp_port temp_proto < <(parse_url "$input_you_domain_full")
            if [[ "$temp_proto" == "http" ]]; then no_tls="yes"; else no_tls="no"; fi
            if [[ "$temp_proto" == "https" ]]; then you_frontend_port="${temp_port:-443}"; else you_frontend_port="${temp_port:-80}"; fi
        fi
        if [[ -n "$input_r_domain_full" ]]; then
            local temp_port temp_proto
            IFS='|' read -r r_domain r_domain_path temp_port temp_proto < <(parse_url "$input_r_domain_full")
            if [[ "$temp_proto" == "http" ]]; then r_http_frontend="yes"; else r_http_frontend="no"; fi
            if [[ "$temp_proto" == "https" ]]; then r_frontend_port="${temp_port:-443}"; else r_frontend_port="${temp_port:-80}"; fi
        fi

        if [[ -z "$you_domain" || -z "$r_domain" ]]; then
            echo "错误: 域名信息不能为空。" >&2
            exit 1
        fi
    fi
}

# --- 3. 显示摘要 ---
display_summary() {
    # 确定最终的证书域名
    if [[ -n "$cert_domain" ]]; then
        format_cert_domain="$cert_domain"
    elif [[ "$parse_cert_domain" == "yes" ]]; then
        if [[ "$you_domain" == *.*.* ]]; then
            format_cert_domain="${you_domain#*.}"
        else
            format_cert_domain="$you_domain"
        fi
    else
        format_cert_domain="$you_domain"
    fi

    # 确定最终的 DNS resolver
    if [[ -n "$manual_resolver" ]]; then
        resolver="$manual_resolver valid=60s"
    else
        resolver="$(get_resolver_host) $(get_ipv6_flag)"
    fi

    local protocol url
    protocol=$([[ "$no_tls" == "yes" ]] && echo "http" || echo "https")
    url="${protocol}://${you_domain}${you_frontend_port:+:$you_frontend_port}${you_domain_path}"

    local r_proto r_url
    r_proto=$([[ "$r_http_frontend" == "yes" ]] && echo "http" || echo "https")
    r_url="${r_proto}://${r_domain}${r_frontend_port:+:$r_frontend_port}${r_domain_path}"

    # 打印摘要
    echo -e "\n\e[1;34m🔧 Nginx 反代配置信息\e[0m"
    echo "──────────────────────────────────────────────"
    printf "➡️  访问地址 (From): %s\n" "$url"
    printf "⬅️  目标地址 (To):   %s\n" "$r_url"
    echo "──────────────────────────────────────────────"
    printf "📜 证书域名:         %s\n" "$format_cert_domain"
    printf "🔒 是否禁用 TLS:       %s\n" "$([[ "$no_tls" == "yes" ]] && echo "✅ 是" || echo "❌ 否")"
    printf "🧠 DNS 解析:          %s\n" "$resolver"
    echo "──────────────────────────────────────────────"
}

# --- 4. 安装依赖 (Nginx, acme.sh) ---
install_dependencies() {
    local OS_NAME PM GNUPG_PM

    source /etc/os-release
    case "$ID" in
      debian|devuan|kali) OS_NAME='debian'; PM='apt-get'; GNUPG_PM='gnupg2' ;;
      ubuntu) OS_NAME='ubuntu'; PM='apt-get'; GNUPG_PM=$([[ ${VERSION_ID%%.*} -lt 22 ]] && echo "gnupg2" || echo "gnupg") ;;
      centos|fedora|rhel|almalinux|rocky|amzn) OS_NAME='rhel'; PM=$(command -v dnf >/dev/null && echo "dnf" || echo "yum") ;;
      arch|archarm) OS_NAME='arch'; PM='pacman' ;;
      alpine) OS_NAME='alpine'; PM='apk' ;;
      *) echo "错误: 不支持的操作系统 '$ID'。" >&2; exit 1 ;;
    esac

    echo "INFO: 检查 Nginx..."
    if ! command -v nginx &> /dev/null; then
        echo "INFO: Nginx 未安装，正在从官方源为 '$OS_NAME' 安装..."

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
        echo "INFO: Nginx 安装完成。"
    else
        echo "INFO: Nginx 已安装。"
    fi

    ACME_SH="$HOME/.acme.sh/acme.sh"
    if [[ "$no_tls" != "yes" && ! -f "$ACME_SH" ]]; then
       echo "INFO: 正在为当前用户安装 acme.sh..."
       if ! command -v socat &> /dev/null; then
            source /etc/os-release
            case "$ID" in
                debian|ubuntu|arch) $SUDO "$PM" install -y socat cron ;;
                *) $SUDO "$PM" install -y socat cronie ;;
            esac
       fi
       curl https://get.acme.sh | sh -s
       "$ACME_SH" --upgrade --auto-upgrade
       "$ACME_SH" --set-default-ca --server letsencrypt
       echo "INFO: acme.sh 安装完成。"
    fi
}

# --- 5. 生成 Nginx 配置 ---
generate_nginx_config() {
    echo "INFO: 正在生成 Nginx 配置文件..."
    curl -sL "$CONF_HOME/nginx.conf" | $SUDO tee /etc/nginx/nginx.conf > /dev/null

    local download_domain_config
    if [[ "$no_tls" == "yes" ]]; then
        download_domain_config="p.example.com.no_tls.conf"
    else
        download_domain_config="p.example.com.conf"
    fi

    local -a subst_var_names=()
    export you_domain; subst_var_names+=("you_domain")
    export you_domain_path="${you_domain_path:-/}"; subst_var_names+=("you_domain_path")
    export you_frontend_port; subst_var_names+=("you_frontend_port")
    export resolver; subst_var_names+=("resolver")
    export format_cert_domain; subst_var_names+=("format_cert_domain")

    if [[ -n "$you_domain_path" && "$you_domain_path" != "/" ]]; then
      export you_domain_path_rewrite="rewrite ^${you_domain_path}(.*)$ ${r_domain_path:-\/}\$1 break;"
    else
      export you_domain_path_rewrite=""
    fi
    subst_var_names+=("you_domain_path_rewrite")

    local r_proto=$([[ "$r_http_frontend" == "yes" ]] && echo "http" || echo "https")
    local r_port_str=$([[ -n "$r_frontend_port" ]] && echo ":$r_frontend_port" || echo "")
    export r_domain_full="${r_proto}://${r_domain}${r_port_str}"
    subst_var_names+=("r_domain_full")

    local subst_vars
    subst_vars=$(for var in "${subst_var_names[@]}"; do printf " \${%s}" "$var"; done)

    local you_domain_config_filename="${you_domain}.${you_frontend_port}.conf"
    curl -sL "$CONF_HOME/conf.d/$download_domain_config" | envsubst "$subst_vars" | $SUDO tee "/etc/nginx/conf.d/$you_domain_config_filename" > /dev/null

    echo "INFO: 配置文件 '/etc/nginx/conf.d/$you_domain_config_filename' 已生成。"
}

# --- 6. 申请 SSL 证书 ---
issue_certificate() {
    if [[ "$no_tls" == "yes" ]]; then
        echo "INFO: 已禁用 TLS，跳过证书申请。"
        return
    fi

    ACME_SH="$HOME/.acme.sh/acme.sh"
    local cert_path_base="/etc/nginx/certs/$format_cert_domain"
    local cert_file_path="$cert_path_base/cert"

    local is_wildcard="no"
    if [[ "$format_cert_domain" != "$you_domain" ]]; then
        is_wildcard="yes"
    fi

    # 场景 1: 泛域名场景，且用户已手动放置证书
    if [[ "$is_wildcard" == "yes" ]] && [ -f "$cert_file_path" ]; then
        echo "INFO: 检测到证书目录 '$cert_path_base' 已存在，将假定您已手动配置了正确的 (泛)域名证书。"
        echo "INFO: 跳过证书申请和安装步骤。"
        return
    fi

    # 决定申请模式
    local issue_params=()
    local main_domain_to_check="$you_domain"

    if [[ -n "$dns_provider" ]]; then
        # --- DNS API 模式 ---
        if [[ "$is_wildcard" == "yes" ]]; then
            main_domain_to_check="$format_cert_domain"
            issue_params=(--issue --dns "$dns_provider" -d "$format_cert_domain" -d "*.$format_cert_domain")
            echo "INFO: 准备使用 DNS API 为 '$format_cert_domain' 和 '*.$format_cert_domain' 申请泛域名证书..."
        else
            issue_params=(--issue --dns "$dns_provider" -d "$you_domain")
            echo "INFO: 准备使用 DNS API 为 '$you_domain' 申请证书..."
        fi

        # 引导用户配置 API 密钥
        echo "--------------------------------------------------------"
        echo -e "\e[1;33m需要配置 DNS API 密钥\e[0m"
        echo "acme.sh 需要 API 密钥来自动修改您的 DNS 记录以完成验证。"
        echo "请参考 acme.sh 的官方文档获取您 DNS 提供商所需的变量："
        echo "https://github.com/acmesh-official/acme.sh/wiki/dnsapi"
        echo ""
        if [[ "$dns_provider" == "cf" ]]; then
            echo "示例: 对于 Cloudflare (cf)，您需要提供 CF_Token 和 CF_Account_ID。"
            read -p "请输入您的 Cloudflare Token: " CF_Token
            read -p "请输入您的 Cloudflare Account ID: " CF_Account_ID
            export CF_Token
            export CF_Account_ID
        else
            echo "请手动导出您 DNS 提供商 ('$dns_provider') 所需的环境变量。"
            read -p "配置完成后，请按 Enter 键继续..."
        fi
        echo "--------------------------------------------------------"

    else
        # --- Standalone HTTP 模式 ---
        if [[ "$is_wildcard" == "yes" ]]; then
            echo "--------------------------------------------------------" >&2
            echo -e "\e[1;33m警告: 证书配置不匹配\e[0m" >&2
            echo "您的 Nginx 配置需要一个泛域名证书 (*.$format_cert_domain)，但该证书目前不存在。" >&2
            echo "泛域名证书必须使用 DNS API 模式进行申请。" >&2
            echo "请使用 --dns <provider> 参数 (例如 --dns cf) 并提供 API 密钥后重试。" >&2
            echo "--------------------------------------------------------" >&2
            exit 1
        fi
        issue_params=(--issue --standalone -d "$you_domain")
        echo "INFO: 准备使用 Standalone 模式为 '$you_domain' 申请证书..."
    fi

    # 检查证书是否已由 acme.sh 管理
    if ! "$ACME_SH" --info -d "$main_domain_to_check" 2>/dev/null | grep -q RealFullChainPath; then
        echo "INFO: 证书不存在，开始申请..."
        $SUDO mkdir -p "$cert_path_base"

        # 执行申请
        "$ACME_SH" "${issue_params[@]}" --keylength ec-256 || {
            echo "错误: 证书申请失败。" >&2
            if [[ -z "$dns_provider" ]]; then
                echo "对于 Standalone 模式，请检查：" >&2
                echo "1. 域名 ('$you_domain') 是否已正确解析到本服务器的公网 IP 地址。" >&2
                echo "2. 服务器的防火墙 (或云服务商安全组) 是否已放行 TCP 80 端口。" >&2
                echo "3. 80 端口当前可能被 Nginx 或其他程序占用。请手动停止相关服务后重试。" >&2
            else
                echo "对于 DNS 模式，请检查：" >&2
                echo "1. 您提供的 API 密钥是否正确且拥有修改 DNS 的权限。" >&2
                echo "2. acme.sh 是否支持您的 DNS 提供商 ('$dns_provider')。" >&2
            fi

            local you_domain_config_filename="${you_domain}.${you_frontend_port}.conf"
            echo "INFO: 正在清理本次生成的 Nginx 配置文件: $you_domain_config_filename" >&2
            $SUDO rm -f "/etc/nginx/conf.d/$you_domain_config_filename"

            exit 1
        }
        echo "INFO: 证书申请成功。"
    else
        echo "INFO: 证书已由 acme.sh 管理，跳过申请步骤。"
    fi

    # 安装证书
    echo "INFO: 正在安装证书到 Nginx 目录 '$cert_path_base'..."
    "$ACME_SH" --install-cert -d "$main_domain_to_check" --ecc \
        --fullchain-file "$cert_path_base/cert" \
        --key-file "$cert_path_base/key" \
        --reloadcmd "$SUDO nginx -s reload" --force

    echo "INFO: 证书安装并部署完成。"
}


# ===================================================================================
#                                 主函数
# ===================================================================================
main() {
    parse_arguments "$@"
    prompt_interactive_mode
    display_summary
    install_dependencies
    generate_nginx_config
    issue_certificate

    echo "INFO: 正在检查 Nginx 配置并执行最终重载..."
    $SUDO nginx -t
    $SUDO nginx -s reload

    echo -e "\n\e[1;32m✅ 恭喜！Nginx 反向代理部署成功！\e[0m"
}

# --- 脚本执行入口 ---
main "$@"
