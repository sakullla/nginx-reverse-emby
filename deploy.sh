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

部署选项:
  -y, --you-domain <域名或URL>   你的域名或完整 URL (例如: https://app.example.com/emby)
  -r, --r-domain <域名或URL>     反代 Emby 的域名或完整 URL (例如: http://127.0.0.1:8096)
  -m, --cert-domain <域名>       手动指定用于 SSL 证书的域名 (例如: example.com)，用于泛域名证书。优先级最高。
  -d, --parse-cert-domain         自动从访问域名中解析出根域名作为证书域名 (例如: 从 app.example.com 解析出 example.com)。
  -D, --dns <provider>            使用 DNS API 模式申请证书 (例如: cf)。这是申请泛域名证书的【必须】选项。
  -R, --resolver <DNS服务器>      手动指定 DNS 解析服务器 (例如: "8.8.8.8 1.1.1.1")
  -c, --template-domain-config <路径或URL> 指定一个自定义的 Nginx 配置文件模板。
  --cf-token <TOKEN>              (当 --dns cf 时) 您的 Cloudflare API Token。
  --cf-account-id <ID>            (当 --dns cf 时) 您的 Cloudflare Account ID。

移除选项:
  --remove <域名或URL>         移除指定域名或 URL 的所有相关配置和证书。
  -Y, --yes                       在非交互模式下，自动确认移除操作。

其他选项:
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

# --- URL 输入处理辅助函数 ---
process_url_input() {
    local full_url="$1"
    local domain_type="$2" # "you" or "r"

    if [[ -z "$full_url" ]]; then
        return
    fi

    local temp_domain temp_path temp_port temp_proto
    IFS='|' read -r temp_domain temp_path temp_port temp_proto < <(parse_url "$full_url")

    if [[ "$domain_type" == "you" ]]; then
        you_domain="$temp_domain"
        you_domain_path="$temp_path"
        if [[ "$temp_proto" == "http" ]]; then
            no_tls="yes"
        else
            no_tls="no"
        fi
        if [[ "$no_tls" == "no" ]]; then
            you_frontend_port="${temp_port:-443}"
        else
            you_frontend_port="${temp_port:-80}"
        fi
    elif [[ "$domain_type" == "r" ]]; then
        r_domain="$temp_domain"
        r_domain_path="$temp_path"
        if [[ "$temp_proto" == "http" ]]; then
            r_http_frontend="yes"
        else
            r_http_frontend="no"
        fi
        if [[ "$r_http_frontend" == "no" ]]; then
            r_frontend_port="${temp_port:-443}"
        else
            r_frontend_port="${temp_port:-80}"
        fi
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
    cf_token=""
    cf_account_id=""
    domain_to_remove=""
    force_yes="no"
    template_domain_config_source=""
    you_domain=""; you_domain_path=""; you_frontend_port=""; no_tls=""
    r_domain=""; r_domain_path=""; r_frontend_port=""; r_http_frontend=""

    local TEMP
    TEMP=$(getopt -o y:r:m:R:dD:hYc: --long you-domain:,r-domain:,cert-domain:,resolver:,parse-cert-domain,dns:,cf-token:,cf-account-id:,remove:,yes,template-domain-config:,help -n "$(basename "$0")" -- "$@")
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
            -c|--template-domain-config) template_domain_config_source="$2"; shift 2 ;;
            --cf-token) cf_token="$2"; shift 2 ;;
            --cf-account-id) cf_account_id="$2"; shift 2 ;;
            --remove) domain_to_remove="$2"; shift 2 ;;
            -Y|--yes) force_yes="yes"; shift ;;
            -h|--help) show_help; shift ;;
            --) shift; break ;;
            *) echo "错误: 未知参数 $1" >&2; exit 1 ;;
        esac
    done

    # 使用新的辅助函数处理 URL 输入
    process_url_input "$you_domain_full" "you"
    process_url_input "$r_domain_full" "r"
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

        process_url_input "$input_you_domain_full" "you"
        process_url_input "$input_r_domain_full" "r"

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

    local template_content
    if [[ -n "$template_domain_config_source" ]]; then
        echo "INFO: 使用用户提供的模板: $template_domain_config_source"
        if [[ "$template_domain_config_source" == http* ]]; then
            template_content=$(curl -sL "$template_domain_config_source")
        elif [ -f "$template_domain_config_source" ]; then
            template_content=$(cat "$template_domain_config_source")
        else
            echo "错误: 提供的模板 '$template_domain_config_source' 既不是有效的 URL 也不是本地文件。" >&2
            exit 1
        fi
    else
        local default_template_name
        if [[ "$no_tls" == "yes" ]]; then
            default_template_name="p.example.com.no_tls.conf"
        else
            default_template_name="p.example.com.conf"
        fi
        echo "INFO: 使用默认模板: $default_template_name"
        template_content=$(curl -sL "$CONF_HOME/conf.d/$default_template_name")
    fi

    local -a subst_var_names=()
    export you_domain; subst_var_names+=("you_domain")
    export you_domain_path="${you_domain_path:-/}"; subst_var_names+=("you_domain_path")
    export you_frontend_port; subst_var_names+=("you_frontend_port")
    export resolver; subst_var_names+=("resolver")
    export format_cert_domain; subst_var_names+=("format_cert_domain")

    # [优化] 重写规则生成逻辑
    export you_domain_path_rewrite=""
    # 仅当访问路径不是根目录时才生成重写规则
    if [[ -n "$you_domain_path" && "$you_domain_path" != "/" ]]; then
        # 如果后端路径为空，则默认为根目录 "/"
        local target_path="${r_domain_path:-/}"
        # 构造重写规则，注意 \$1 用于将 $1 传递给 Nginx
        export you_domain_path_rewrite="rewrite ^${you_domain_path}(.*)\$ ${target_path}\$1 break;"
    fi
    subst_var_names+=("you_domain_path_rewrite")

    local r_proto=$([[ "$r_http_frontend" == "yes" ]] && echo "http" || echo "https")
    local r_port_str=$([[ -n "$r_frontend_port" ]] && echo ":$r_frontend_port" || echo "")
    export r_domain_full="${r_proto}://${r_domain}${r_port_str}"
    subst_var_names+=("r_domain_full")

    local subst_vars
    subst_vars=$(for var in "${subst_var_names[@]}"; do printf " \${%s}" "$var"; done)

    local you_domain_config_filename="${you_domain}.${you_frontend_port}.conf"
    echo "$template_content" | envsubst "$subst_vars" | $SUDO tee "/etc/nginx/conf.d/$you_domain_config_filename" > /dev/null

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
    local main_domain_to_issue="$you_domain" # 默认申请单域名

    if [[ -n "$dns_provider" ]]; then
        # --- DNS API 模式 ---
        local acme_dns_provider="dns_${dns_provider}"

        if [[ "$is_wildcard" == "yes" ]]; then
            main_domain_to_issue="$format_cert_domain"
            issue_params=(--issue --dns "$acme_dns_provider" -d "$format_cert_domain" -d "*.$format_cert_domain")
            echo "INFO: 准备使用 DNS API ($acme_dns_provider) 为 '$format_cert_domain' 和 '*.$format_cert_domain' 申请泛域名证书..."
        else
            issue_params=(--issue --dns "$acme_dns_provider" -d "$you_domain")
            echo "INFO: 准备使用 DNS API ($acme_dns_provider) 为 '$you_domain' 申请证书..."
        fi

        # 引导用户配置 API 密钥
        if [[ "$dns_provider" == "cf" ]]; then
            if [[ -n "$cf_token" && -n "$cf_account_id" ]]; then
                export CF_Token="$cf_token"
                export CF_Account_ID="$cf_account_id"
                echo "INFO: 使用通过命令行参数传入的 Cloudflare API 凭据。"
            elif [ ! -t 0 ]; then
                echo "错误: 在非交互模式下，必须通过 --cf-token 和 --cf-account-id 参数提供 Cloudflare API 凭据。" >&2
                exit 1
            else
                echo "--------------------------------------------------------"
                echo -e "\e[1;33m需要配置 DNS API 密钥\e[0m"
                echo "示例: 对于 Cloudflare (cf)，您需要提供 CF_Token 和 CF_Account_ID。"
                read -p "请输入您的 Cloudflare Token: " CF_Token
                read -p "请输入您的 Cloudflare Account ID: " CF_Account_ID
                export CF_Token
                export CF_Account_ID
                echo "--------------------------------------------------------"
            fi
        else
            if [ ! -t 0 ]; then
                 echo "错误: 在非交互模式下，请先手动导出您 DNS 提供商 ('$dns_provider') 所需的环境变量。" >&2
                 exit 1
            fi
            echo "--------------------------------------------------------"
            echo -e "\e[1;33m需要配置 DNS API 密钥\e[0m"
            echo "请参考 acme.sh 官方文档，手动导出您 DNS 提供商 ('$dns_provider') 所需的环境变量。"
            echo "https://github.com/acmesh-official/acme.sh/wiki/dnsapi"
            read -p "配置完成后，请按 Enter 键继续..."
            echo "--------------------------------------------------------"
        fi

    else
        # --- Standalone HTTP 模式 ---
        if [[ "$is_wildcard" == "yes" ]]; then
            echo "错误: 泛域名证书 (*.$format_cert_domain) 必须使用 DNS API 模式进行申请。" >&2
            echo "请使用 --dns <provider> 参数 (例如 --dns cf) 并提供 API 密钥。" >&2
            exit 1
        fi
        issue_params=(--issue --standalone -d "$you_domain")
        echo "INFO: 准备使用 Standalone 模式为 '$you_domain' 申请证书..."
    fi

    # [修正] 恢复先检查后申请的逻辑
    if ! "$ACME_SH" --info -d "$main_domain_to_issue" 2>/dev/null | grep -q RealFullChainPath; then
        echo "INFO: 证书不存在，开始申请..."
        $SUDO mkdir -p "$cert_path_base"

        # 执行申请
        "$ACME_SH" "${issue_params[@]}" --keylength ec-256 --force || {
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
        echo "INFO: 证书已由 acme.sh 管理，将跳过申请步骤，直接进行安装/更新。"
    fi

    # 安装证书
    echo "INFO: 正在安装证书到 Nginx 目录 '$cert_path_base'..."
    "$ACME_SH" --install-cert -d "$main_domain_to_issue" --ecc \
        --fullchain-file "$cert_path_base/cert" \
        --key-file "$cert_path_base/key" \
        --reloadcmd "$SUDO nginx -s reload"

    echo "INFO: 证书安装并部署完成。"
}

# --- [新增] 移除函数 ---
remove_domain_config() {
    local remove_url="$domain_to_remove"
    echo "INFO: 正在为 '$remove_url' 查找相关配置..."

    # 精确解析域名和端口
    local domain port temp_path temp_proto
    IFS='|' read -r domain temp_path port temp_proto < <(parse_url "$remove_url")

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

    # 构造精确的配置文件名
    local nginx_conf_file="/etc/nginx/conf.d/${domain}.${port}.conf"

    if ! $SUDO [ -f "$nginx_conf_file" ]; then
        echo "错误: 未找到与 '$domain' 在端口 '$port' 上的 Nginx 配置文件: $nginx_conf_file" >&2
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
    echo -e "\e[1;31m警告: 即将执行破坏性操作！\e[0m"
    echo "将要为 '$domain' (端口: $port) 移除以下内容:"
    echo "  - Nginx 配置文件: $nginx_conf_file"

    local is_wildcard_setup="no"
    if [[ "$uses_tls" == "yes" && "$domain" != "$remove_cert_domain" ]]; then
        is_wildcard_setup="yes"
    fi

    if [[ "$uses_tls" == "yes" ]]; then
        if [[ "$is_wildcard_setup" == "no" ]]; then
            if [ -d "$cert_dir" ]; then
                echo "  - Nginx 证书目录: $cert_dir"
            fi
            ACME_SH="$HOME/.acme.sh/acme.sh"
            if [ -f "$ACME_SH" ]; then
                 echo "  - acme.sh 证书记录 (针对域名: $domain)"
            fi
        else
            echo -e "\e[1;33m  - 注意: 检测到泛域名证书配置，将不会删除共享的证书文件。\e[0m"
        fi
    fi
    echo "--------------------------------------------------------"

    # [修正] 智能确认流程
    if [ ! -t 0 ]; then # 非交互模式
        if [[ "$force_yes" != "yes" ]]; then
            echo "错误: 在非交互模式下，移除操作必须使用 '-Y' 或 '--yes' 参数进行确认。" >&2
            exit 1
        fi
        echo "INFO: 检测到 '--yes' 参数，将自动执行移除操作。"
    else # 交互模式
        read -p "此操作不可逆，请输入 'yes' 确认移除: " confirmation
        if [[ "$confirmation" != "yes" ]]; then
            echo "操作已取消。"
            exit 0
        fi
    fi

    echo "INFO: 开始移除..."
    $SUDO rm -f "$nginx_conf_file"
    echo "INFO: Nginx 配置文件已删除。"

    if [[ "$uses_tls" == "yes" ]]; then
        if [[ "$is_wildcard_setup" == "no" ]]; then
            if [ -d "$cert_dir" ]; then
                $SUDO rm -rf "$cert_dir"
                echo "INFO: Nginx 证书目录已删除。"
            fi

            ACME_SH="$HOME/.acme.sh/acme.sh"
            if [ -f "$ACME_SH" ]; then
                "$ACME_SH" --remove -d "$domain" --ecc || echo "警告: 从 acme.sh 移除证书失败，可能记录已不存在。"
                echo "INFO: acme.sh 证书记录已移除。"
            fi
        else
            echo "INFO: 证书目录和 acme.sh 记录未被删除。"
            echo "如果您确认不再需要此泛域名证书，请手动执行以下命令进行清理："
            echo "  $HOME/.acme.sh/acme.sh --remove -d '$remove_cert_domain' --ecc"
            echo "  $SUDO rm -rf '$cert_dir'"
        fi
    fi

    echo "INFO: 正在检查 Nginx 配置并执行重载..."
    $SUDO nginx -t
    $SUDO nginx -s reload

    echo -e "\n\e[1;32m✅ 域名 '$domain' 的相关配置已成功移除！\e[0m"
}


# ===================================================================================
#                                 主函数
# ===================================================================================
main() {
    parse_arguments "$@"

    if [[ -n "$domain_to_remove" ]]; then
        remove_domain_config
        exit 0
    fi

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
