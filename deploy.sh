#!/bin/bash

set -e
confhome="https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main"

# 显示帮助信息
show_help() {
    cat << EOF
用法: $(basename "$0") [选项]

选项:
  -y, --you-domain <域名>        你的域名或IP (例如: example.com)
  -r, --r-domain <域名>          反代 Emby 的域名 (例如: backend.com)
  -P, --you-frontend-port <端口>  你的前端访问端口 (默认: 443)
  -p, --r-frontend-port <端口>    反代 Emby 前端端口 (默认: 空)
  -f, --r-http-frontend          反代 Emby 使用 HTTP 作为前端访问 (默认: 否)
  -s, --no-tls                   禁用 TLS (默认: 否)
  -m, --cert-domain              TLS的证书域名，配置后需要自己将证书放到对应位置
  -d, --parse-cert-domain        简单的从证书中解析出证书域名
  -h, --help                     显示帮助信息
EOF
    exit 0
}


is_in_china() {
    if [ -z "$_loc" ]; then
        # www.cloudflare.com/dash.cloudflare.com 国内访问的是美国服务器，而且部分地区被墙
        # 没有ipv6 www.visa.cn
        # 没有ipv6 www.bose.cn
        # 没有ipv6 www.garmin.com.cn
        # 备用 www.prologis.cn
        # 备用 www.autodesk.com.cn
        # 备用 www.keysight.com.cn
        if ! _loc=$(curl -L http://www.qualcomm.cn/cdn-cgi/trace | grep '^loc=' | cut -d= -f2 | grep .); then
            error_and_exit "Can not get location."
        fi
        echo "Location: $_loc" >&2
    fi
    [ "$_loc" = CN ]
}

has_ipv6() {
  ip -6 addr show scope global | grep -q inet6
}

# 提取系统 DNS（排除回环地址、IPv6），作为 resolver 优先值
get_system_dns() {
  awk '/^nameserver/ {print $2}' /etc/resolv.conf \
    | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' \
    | grep -vE '^(127\.|0\.|255\.)' \
    | xargs
}

# 根据国家选择默认公共 DNS
get_default_dns() {
  if is_in_china; then
    echo "223.5.5.5 119.29.29.29"
  else
    echo "1.1.1.1 8.8.8.8"
  fi
}

# 合并 resolver 值
get_resolver_host() {
  local system_dns
  system_dns=$(get_system_dns)

  if [[ -n "$system_dns" ]]; then
    echo "$system_dns valid=60s"
  else
    echo "$(get_default_dns) valid=60s"
  fi
}

# IPv6 设置
get_ipv6_flag() {
  has_ipv6 && echo "" || echo "ipv6=off"
}


# 初始化变量
you_domain=""
you_domain_path=""
r_domain=""
r_domain_path=""
cert_domain=""
parse_cert_domain="no"
you_frontend_port="443"
r_frontend_port=""
r_http_frontend="no"
no_tls="no"

# ===== 封装 URL 解析函数 =====
parse_url() {
    local url="$1"
    local __proto __host __port __path

    if [[ "$url" =~ ^(https?)://([^/:?#]+)(:([0-9]+))?(/[^?#]*)? ]]; then
        __proto="${BASH_REMATCH[1]}"
        __host="${BASH_REMATCH[2]}"
        __port="${BASH_REMATCH[4]}"
        __path="${BASH_REMATCH[5]}"

        eval "$2=\"$__host\""
        eval "$2_path=\"$__path\""

        if [[ "$2" == "you_domain" ]]; then
            you_frontend_port="${__port:-$([[ "$__proto" == "https" ]] && echo 443 || echo 80)}"
            no_tls=$([[ "$__proto" == "http" ]] && echo "yes" || echo "no")
            # 如果 parse_cert_domain 有值，设置 cert_domain
            if [[ "$parse_cert_domain" == "yes" ]]; then
                cert_domain=$(echo "$__host" | awk -F. '{n=split($0, a, "."); if (n >= 2) print a[n-1]"."a[n]; else print $0}')
            fi
        elif [[ "$2" == "r_domain" ]]; then
            r_frontend_port="${__port:-$([[ "$__proto" == "https" ]] && echo 443 || echo 80)}"
            r_http_frontend=$([[ "$__proto" == "http" ]] && echo "yes" || echo "no")
        fi
    fi
}

# ===== 参数解析 =====
TEMP=$(getopt -o y:r:P:p:bfshmd --long you-domain:,r-domain:,you-frontend-port:,r-frontend-port:,r-http-frontend,no-tls,help,cert-domain:,parse-cert-domain -n "$(basename "$0")" -- "$@")

if [ $? -ne 0 ]; then
    echo "参数解析失败，请检查输入的参数。"
    exit 1
fi

eval set -- "$TEMP"

while true; do
    case "$1" in
        -y|--you-domain) you_domain="$2"; shift 2 ;;
        -r|--r-domain) r_domain="$2"; shift 2 ;;
        -P|--you-frontend-port) you_frontend_port="$2"; shift 2 ;;
        -p|--r-frontend-port) r_frontend_port="$2"; shift 2 ;;
        -f|--r-http-frontend) r_http_frontend="yes"; shift ;;
        -s|--no-tls) no_tls="yes"; shift ;;
        -m|--cert-domain ) cert_domain="$2"; shift 2 ;;
        -d|--parse-cert-domain  ) parse_cert_domain="yes"; shift ;;
        -h|--help) show_help; shift ;;
        --) shift; break ;;
        *) echo "错误: 未知参数 $1"; exit 1 ;;
    esac
done

# ===== 自动解析域名中的 URL 协议和端口 =====
[[ -n "$you_domain" ]] && parse_url "$you_domain" you_domain you_domain_path
[[ -n "$r_domain" ]] && parse_url "$r_domain" r_domain r_domain_path

# ===== 如果没有必要参数则进入交互模式 =====
if [[ -z "$you_domain" || -z "$r_domain" ]]; then
    echo -e "\n--- 交互模式: 配置反向代理 ---"
    echo "请按提示输入参数，或直接按 Enter 使用默认值"
    read -p "你的域名或者 IP [默认: you.example.com]: " input_you_domain
    read -p "反代Emby的域名 [默认: r.example.com]: " input_r_domain

    # 自动解析 input_you_domain
    if [[ "$input_you_domain" =~ ^(https?)://([^/:?#]+)(:([0-9]+))?(/[^?#]*)? ]]; then
        proto="${BASH_REMATCH[1]}"
        host="${BASH_REMATCH[2]}"
        port="${BASH_REMATCH[4]}"
        path="${BASH_REMATCH[5]}"
        input_you_domain="$host"
        input_you_domain_path="$path"
        input_you_frontend_port="${port:-$([[ "$proto" == "https" ]] && echo 443 || echo 80)}"
        input_no_tls=$([[ "$proto" == "http" ]] && echo "yes" || echo "no")
    fi

    # 自动解析 input_r_domain
    if [[ "$input_r_domain" =~ ^(https?)://([^/:?#]+)(:([0-9]+))?(/[^?#]*)? ]]; then
        r_proto="${BASH_REMATCH[1]}"
        r_host="${BASH_REMATCH[2]}"
        r_port="${BASH_REMATCH[4]}"
        r_path="${BASH_REMATCH[5]}"
        input_r_domain="$r_host"
        input_r_domain_path="$r_path"
        input_r_frontend_port="${r_port:-$([[ "$r_proto" == "https" ]] && echo 443 || echo 80)}"
        input_r_http_frontend=$([[ "$r_proto" == "http" ]] && echo "yes" || echo "no")
    fi

    if [[ -z "$input_you_frontend_port" ]]; then
        read -p "你的前端访问端口 [默认: 443]: " input_you_frontend_port
    fi

    if [[ -z "$input_no_tls" ]]; then
          read -p "是否禁用TLS? (yes/no) [默认: no]: " input_no_tls
    fi

    if [[ -z "$input_r_frontend_port"  ]]; then
        read -p "反代Emby前端端口 [默认: 空]: " input_r_frontend_port
    fi

    if [[ -z "$input_r_http_frontend" ]]; then
        read -p "是否使用HTTP连接反代Emby前端? (yes/no) [默认: no]: " input_r_http_frontend
    fi

    # 最终赋值
    you_domain="${input_you_domain:-you.example.com}"
    you_domain_path="${input_you_domain_path}"
    r_domain="${input_r_domain:-r.example.com}"
    r_domain_path="${input_r_domain_path}"
    you_frontend_port="${input_you_frontend_port:-443}"
    r_frontend_port="${input_r_frontend_port}"
    r_http_frontend="${input_r_http_frontend:-no}"
    no_tls="${input_no_tls:-no}"
fi

# 美化输出配置信息
protocol=$( [[ "$no_tls" == "yes" ]] && echo "http" || echo "https" )
url="${protocol}://${you_domain}:${you_frontend_port}${you_domain_path}"


# 最终导出
resolver="$(get_resolver_host) $(get_ipv6_flag)"


echo -e "\n\e[1;34m🔧 Emby 反代配置信息\e[0m"
echo "──────────────────────────────────────"
printf "🌍 访问地址: %s\n" "$url"
printf "📌 你的域名: %s\n" "$you_domain"
printf "📜 证书域名: %s\n" "$cert_domain"
printf "🖥️  前端访问端口: %s\n" "$you_frontend_port"
printf "🔄 反代 Emby 域名: %s\n" "$r_domain"
printf "🎯 Emby 前端端口: %s\n" "${r_frontend_port:-未指定}"
printf "🛠️  使用 HTTP 反代 Emby: %s\n" "$( [[ "$r_http_frontend" == "yes" ]] && echo "✅ 是" || echo "❌ 否" )"
printf " 🔒禁用 TLS: %s\n" "$( [[ "$no_tls" == "yes" ]] && echo "✅ 是" || echo "❌ 否" )"
printf "🧠 DNS 配置: %s\n" "$resolver"
echo "──────────────────────────────────────"


check_dependencies() {

  if [[ ! -f '/etc/os-release' ]]; then
    echo "error: Don't use outdated Linux distributions."
    return 1
  fi
  source /etc/os-release
  if [ -z "$ID" ]; then
      echo -e "Unsupported Linux OS Type"
      exit 1
  fi

  case "$ID" in
  debian|devuan|kali)
      OS_NAME='debian'
      PM='apt'
      GNUPG_PM='gnupg2'
      ;;
  ubuntu)
      OS_NAME='ubuntu'
      PM='apt'
      GNUPG_PM=$([[ ${VERSION_ID%%.*} -lt 22 ]] && echo "gnupg2" || echo "gnupg")
      ;;
  centos|fedora|rhel|almalinux|rocky|amzn)
      OS_NAME='rhel'
      PM=$(command -v dnf >/dev/null && echo "dnf" || echo "yum")
      ;;
  arch|archarm)
      OS_NAME='arch'
      PM='pacman'
      ;;
  alpine)
      OS_NAME='alpine'
      PM='apk'
      ;;
  *)
      OS_NAME="$ID"
      PM='apt'
      ;;
  esac
}

check_dependencies

# 检查并安装 Nginx
echo "检查 Nginx 是否已安装..."
if ! command -v nginx &> /dev/null; then
    echo "Nginx 未安装，正在安装..."

    if [[ "$OS_NAME" == "debian" || "$OS_NAME" == "ubuntu" ]]; then
      $PM install -y "$GNUPG_PM" ca-certificates lsb-release "$OS_NAME-keyring" \
        && curl https://nginx.org/keys/nginx_signing.key | gpg --dearmor > /usr/share/keyrings/nginx-archive-keyring.gpg \
        && echo "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.gpg] http://nginx.org/packages/mainline/$OS_NAME `lsb_release -cs` nginx" > /etc/apt/sources.list.d/nginx.list \
        && echo -e "Package: *\nPin: origin nginx.org\nPin: release o=nginx\nPin-Priority: 900\n" > /etc/apt/preferences.d/99nginx \
        && $PM update && $PM install -y nginx \
        && mkdir -p /etc/systemd/system/nginx.service.d \
        && echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" > /etc/systemd/system/nginx.service.d/override.conf \
        && systemctl daemon-reload && rm -f /etc/nginx/conf.d/default.conf \
        && systemctl restart nginx
    elif [[ "$OS_NAME" == "rhel" ]]; then
      $PM install -y yum-utils \
          && echo -e "[nginx-mainline]\nname=NGINX Mainline Repository\nbaseurl=https://nginx.org/packages/mainline/centos/\$releasever/\$basearch/\ngpgcheck=1\nenabled=1\ngpgkey=https://nginx.org/keys/nginx_signing.key" > /etc/yum.repos.d/nginx.repo \
          && $PM install -y nginx \
          && mkdir -p /etc/systemd/system/nginx.service.d \
          && echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" > /etc/systemd/system/nginx.service.d/override.conf \
          && systemctl daemon-reload && rm -f /etc/nginx/conf.d/default.conf \
          && systemctl restart nginx
    elif [[ "$OS_NAME" == "arch" ]]; then
      $PM -Sy --noconfirm nginx-mainline \
          && mkdir -p /etc/systemd/system/nginx.service.d \
          && echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" > /etc/systemd/system/nginx.service.d/override.conf \
          && systemctl daemon-reload && rm -f /etc/nginx/conf.d/default.conf \
          && systemctl restart nginx
    elif [[ "$OS_NAME" == "alpine" ]]; then
      $PM update && $PM add --no-cache nginx-mainline \
          && rc-update add nginx default && rm -f /etc/nginx/conf.d/default.conf \
          && rc-service nginx restart
    else
        echo "不支持的操作系统，请手动安装 Nginx" >&2
        exit 1
    fi
else
    echo "Nginx 已安装，跳过安装步骤。"
fi


# 下载并复制 nginx.conf
echo "下载并复制 nginx 配置文件..."
echo "下载地址 $confhome/nginx.conf"
curl -o /etc/nginx/nginx.conf "$confhome/nginx.conf"

you_domain_config="$you_domain.$you_frontend_port"
download_domain_config="p.example.com"
default_you_frontend_port=443

# 如果 $no_tls 选择使用 HTTP，则选择下载对应的模板
if [[ "$no_tls" == "yes" ]]; then
    download_domain_config="p.example.com.no_tls"
    default_you_frontend_port=80
fi

# 下载并复制 p.example.com.conf 并修改
echo "下载并创建 $you_domain_config.conf 到 /etc/nginx/conf.d/"

# 反代域名
export you_domain=${you_domain}

# resolver
export resolver=${resolver}
# 反代端口
if [[ -n "$you_frontend_port" ]]; then
    export you_frontend_port=${you_frontend_port}
else
    export you_frontend_port=${default_you_frontend_port}
fi

# 如果 $you_domain_path 不为空，加上重写path的指令
if [[ -n "$you_domain_path" ]]; then
  export you_domain_path_rewrite="rewrite ^${you_domain_path}/(.*)$ ${r_domain_path}/\$1 break;"
else
  export you_domain_path_rewrite=""
fi

export you_domain_path=${you_domain_path:-/}

# 如果 r_http_frontend 选择使用 HTTP，先替换 https://emby.example.com
# 构造 r_domain_full: 包括协议、端口（可选）
# 判断协议
if [[ "$r_http_frontend" == "yes" ]]; then
  proto="http"
else
  proto="https"
fi

# 如果 r_frontend_port 不为空，修改 emby.example.com 加上端口
if [[ -n "$r_frontend_port" ]]; then
  port=":$r_frontend_port"
else
  port=""
fi

# 最终拼接代理的emby域名
r_domain_full="${proto}://${r_domain}${port}${r_domain_path}"
export r_domain_full=${r_domain_full}

# 替换域名信息

# 如果 $cert_domain 不为空，则替换证书路径
if [[ -n "$cert_domain" ]]; then
  export format_cert_domain=${cert_domain}
else
  export format_cert_domain=${you_domain}
fi

readarray -t vars < <(env | cut -d= -f1)
subst_vars=$(printf '${%s} ' "${vars[@]}")
curl -s "$confhome/conf.d/$download_domain_config.conf" | envsubst "$subst_vars" > "/etc/nginx/conf.d/${you_domain_config}.conf"


if [[ -z "$cert_domain" && "$no_tls" != "yes" ]]; then
    ACME_SH="$HOME/.acme.sh/acme.sh"

    # 检查并安装 acme.sh
   echo "检查 acme.sh 是否已安装..."
   if [[ ! -f "$ACME_SH" ]]; then
       echo "acme.sh 未安装，正在安装..."
       apt install -y socat cron
       curl https://get.acme.sh | sh
       "$ACME_SH" --upgrade --auto-upgrade
       "$ACME_SH" --set-default-ca --server letsencrypt
   else
       echo "acme.sh 已安装，跳过安装步骤。"
   fi

    # 申请并安装 ECC 证书
    if ! "$ACME_SH" --info -d "$you_domain" | grep -q RealFullChainPath; then
        echo "ECC 证书未申请，正在申请..."
        mkdir -p "/etc/nginx/certs/$you_domain"

        "$ACME_SH" --issue -d "$you_domain" --standalone --keylength ec-256 || {
            echo "证书申请失败，请检查错误信息！"
            rm -f "/etc/nginx/conf.d/$you_domain_config.conf"
            exit 1
        }
    else
        echo "ECC 证书已申请，跳过申请步骤。"
    fi

    # 安装证书
    echo "安装证书..."
    "$ACME_SH" --install-cert -d "$you_domain" --ecc \
        --fullchain-file "/etc/nginx/certs/$you_domain/cert" \
        --key-file "/etc/nginx/certs/$you_domain/key" \
        --reloadcmd "nginx -s reload" --force

    echo "证书安装完成！"
fi


echo "重新加载 Nginx..."
nginx -s reload

echo "反向代理设置完成！"
