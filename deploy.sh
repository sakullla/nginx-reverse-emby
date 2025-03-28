#!/bin/bash

set -e

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
  -h, --help                     显示帮助信息
EOF
    exit 0
}

# 初始化变量
you_domain=""
r_domain=""
you_frontend_port="443"
r_frontend_port=""
r_http_frontend="no"
no_tls="no"

# ===== 封装 URL 解析函数 =====
parse_url() {
    local url="$1"
    local __proto __host __port

    if [[ "$url" =~ ^(https?)://([^:/]+)(:([0-9]+))?$ ]]; then
        __proto="${BASH_REMATCH[1]}"
        __host="${BASH_REMATCH[2]}"
        __port="${BASH_REMATCH[4]}"

        eval "$2=\"$__host\""

        if [[ "$2" == "you_domain" ]]; then
            you_frontend_port="${__port:-$([[ "$__proto" == "https" ]] && echo 443 || echo 80)}"
            no_tls=$([[ "$__proto" == "http" ]] && echo "yes" || echo "no")
        elif [[ "$2" == "r_domain" ]]; then
            r_frontend_port="${__port:-$([[ "$__proto" == "https" ]] && echo 443 || echo 80)}"
            r_http_frontend=$([[ "$__proto" == "http" ]] && echo "yes" || echo "no")
        fi
    fi
}

# ===== 参数解析 =====
TEMP=$(getopt -o y:r:P:p:bfsh --long you-domain:,r-domain:,you-frontend-port:,r-frontend-port:,r-http-frontend,no-tls,help -n "$(basename "$0")" -- "$@")

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
        -h|--help) show_help; shift ;;
        --) shift; break ;;
        *) echo "错误: 未知参数 $1"; exit 1 ;;
    esac
done

# ===== 自动解析域名中的 URL 协议和端口 =====
[[ -n "$you_domain" ]] && parse_url "$you_domain" you_domain
[[ -n "$r_domain" ]] && parse_url "$r_domain" r_domain

# ===== 如果没有必要参数则进入交互模式 =====
if [[ -z "$you_domain" || -z "$r_domain" ]]; then
    echo -e "\n--- 交互模式: 配置反向代理 ---"
    echo "请按提示输入参数，或直接按 Enter 使用默认值"
    read -p "你的域名或者 IP [默认: you.example.com]: " input_you_domain
    read -p "反代Emby的域名 [默认: r.example.com]: " input_r_domain

    # 自动解析 input_you_domain
    if [[ "$input_you_domain" =~ ^(https?)://([^:/]+)(:([0-9]+))?$ ]]; then
        proto="${BASH_REMATCH[1]}"
        host="${BASH_REMATCH[2]}"
        port="${BASH_REMATCH[4]}"
        input_you_domain="$host"
        input_you_frontend_port="${port:-$([[ "$proto" == "https" ]] && echo 443 || echo 80)}"
        input_no_tls=$([[ "$proto" == "http" ]] && echo "yes" || echo "no")
    fi

    # 自动解析 input_r_domain
    if [[ "$input_r_domain" =~ ^(https?)://([^:/]+)(:([0-9]+))?$ ]]; then
        r_proto="${BASH_REMATCH[1]}"
        r_host="${BASH_REMATCH[2]}"
        r_port="${BASH_REMATCH[4]}"
        input_r_domain="$r_host"
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
    r_domain="${input_r_domain:-r.example.com}"
    you_frontend_port="${input_you_frontend_port:-443}"
    r_frontend_port="${input_r_frontend_port}"
    r_http_frontend="${input_r_http_frontend:-no}"
    no_tls="${input_no_tls:-no}"
fi

# 美化输出配置信息
protocol=$( [[ "$no_tls" == "yes" ]] && echo "http" || echo "https" )
url="${protocol}://${you_domain}:${you_frontend_port}"

echo -e "\n------ 配置信息 ------"
echo "🌍 访问地址: ${url}"
echo "📌 你的域名: ${you_domain}"
echo "🖥️  你的前端访问端口: ${you_frontend_port}"
echo "🔄 反代 Emby 的域名: ${r_domain}"
echo "🎯 反代 Emby 前端端口: ${r_frontend_port:-未指定}"
echo "🛠️  使用 HTTP 连接反代 Emby 前端: $( [[ "$r_http_frontend" == "yes" ]] && echo "✅ 是" || echo "❌ 否" )"
echo "🔒 禁用 TLS: $( [[ "$no_tls" == "yes" ]] && echo "✅ 是" || echo "❌ 否" )"
echo "----------------------"


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
        && systemctl enable --now nginx
    elif [[ "$OS_NAME" == "rhel" ]]; then
      $PM install -y yum-utils \
          && echo -e "[nginx-mainline]\nname=NGINX Mainline Repository\nbaseurl=https://nginx.org/packages/mainline/centos/\$releasever/\$basearch/\ngpgcheck=1\nenabled=1\ngpgkey=https://nginx.org/keys/nginx_signing.key" > /etc/yum.repos.d/nginx.repo \
          && $PM install -y nginx \
          && mkdir -p /etc/systemd/system/nginx.service.d \
          && echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" > /etc/systemd/system/nginx.service.d/override.conf \
          && systemctl daemon-reload && rm -f /etc/nginx/conf.d/default.conf \
          && systemctl enable --now nginx
    elif [[ "$OS_NAME" == "arch" ]]; then
      $PM -Sy --noconfirm nginx-mainline \
          && mkdir -p /etc/systemd/system/nginx.service.d \
          && echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" > /etc/systemd/system/nginx.service.d/override.conf \
          && systemctl daemon-reload && rm -f /etc/nginx/conf.d/default.conf \
          && systemctl enable --now nginx
    elif [[ "$OS_NAME" == "alpine" ]]; then
      $PM update && $PM add --no-cache nginx-mainline \
          && rc-update add nginx default && rm -f /etc/nginx/conf.d/default.conf \
          && rc-service nginx start
    else
        echo "不支持的操作系统，请手动安装 Nginx" >&2
        exit 1
    fi
else
    echo "Nginx 已安装，跳过安装步骤。"
fi


# 下载并复制 nginx.conf
echo "下载并复制 nginx 配置文件..."
curl -o /etc/nginx/nginx.conf https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/nginx.conf

you_domain_config="$you_domain"
download_domain_config="p.example.com"

# 如果 $no_tls 选择使用 HTTP，则选择下载对应的模板
if [[ "$no_tls" == "yes" ]]; then
    you_domain_config="$you_domain.$you_frontend_port"
    download_domain_config="p.example.com.no_tls"
fi

# 下载并复制 p.example.com.conf 并修改
echo "下载并创建 $you_domain_config 配置文件..."
curl -o "$you_domain_config.conf" "https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/conf.d/$download_domain_config.conf"

# 如果 you_frontend_port 不为空， 则替换端口
if [[ -n "$you_frontend_port" ]]; then
    sed -i "s/443/$you_frontend_port/g" "$you_domain_config.conf"
fi

# 如果 r_http_frontend 选择使用 HTTP，先替换 https://emby.example.com
if [[ "$r_http_frontend" == "yes" ]]; then
    sed -i "s/https:\/\/emby.example.com/http:\/\/emby.example.com/g" "$you_domain_config.conf"
fi

# 如果 r_frontend_port 不为空，修改 emby.example.com 加上端口
if [[ -n "$r_frontend_port" ]]; then
    sed -i "s/emby.example.com/emby.example.com:$r_frontend_port/g" "$you_domain_config.conf"
fi

# 替换域名信息
sed -i "s/p.example.com/$you_domain/g" "$you_domain_config.conf"
sed -i "s/emby.example.com/$r_domain/g" "$you_domain_config.conf"


# 移动配置文件到 /etc/nginx/conf.d/
echo "移动 $you_domain_config.conf 到 /etc/nginx/conf.d/"
if [[ "$OS_NAME" == "ubuntu" ]]; then
  rsync -av "$you_domain_config.conf" /etc/nginx/conf.d/
else
  mv -f "$you_domain_config.conf" /etc/nginx/conf.d/
fi


if [[ "$no_tls" != "yes" ]]; then
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
