#!/bin/bash

set -e

# 解析命令行参数
declare -A args
while [[ "$#" -gt 0 ]]; do
    case "$1" in
        -y|--you_domain) args[you_domain]="$2"; shift 2 ;;
        -r|--r_domain) args[r_domain]="$2"; shift 2 ;;
        *) echo "未知参数: $1"; exit 1 ;;
    esac
done

# 交互式或参数传入
default_you_domain="you.example.com"
default_r_domain="r.example.com"

you_domain="${args[you_domain]:-$default_you_domain}"
r_domain="${args[r_domain]:-$default_r_domain}"

if [[ -z "${args[you_domain]}" || -z "${args[r_domain]}" ]]; then
    read -p "请输入你的域名 (默认: $default_you_domain): " input_you_domain
    read -p "请输入要反代的域名 (默认: $default_r_domain): " input_r_domain

    you_domain="${input_you_domain:-$default_you_domain}"
    r_domain="${input_r_domain:-$default_r_domain}"
fi

# 检查并安装 Nginx
echo "检查 Nginx 是否已安装..."
if ! command -v nginx &> /dev/null; then
    echo "Nginx 未安装，正在安装..."
    if [[ -f /etc/debian_version ]]; then
        apt install -y gnupg2 ca-certificates lsb-release debian-archive-keyring \
            && curl https://nginx.org/keys/nginx_signing.key | gpg --dearmor > /usr/share/keyrings/nginx-archive-keyring.gpg \
            && echo "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.gpg] http://nginx.org/packages/mainline/debian `lsb_release -cs` nginx" > /etc/apt/sources.list.d/nginx.list \
            && echo -e "Package: *\nPin: origin nginx.org\nPin: release o=nginx\nPin-Priority: 900\n" > /etc/apt/preferences.d/99nginx \
            && apt update -y && apt install -y nginx \
            && mkdir -p /etc/systemd/system/nginx.service.d \
            && echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" > /etc/systemd/system/nginx.service.d/override.conf \
            && systemctl daemon-reload && rm -f /etc/nginx/conf.d/default.conf \
            && systemctl enable --now nginx
    elif [[ -f /etc/os-release && $(grep -Ei 'ubuntu' /etc/os-release) ]]; then
        apt install -y gnupg2 ca-certificates lsb-release ubuntu-keyring \
            && curl https://nginx.org/keys/nginx_signing.key | gpg --dearmor > /usr/share/keyrings/nginx-archive-keyring.gpg \
            && echo "deb [signed-by=/usr/share/keyrings/nginx-archive-keyring.gpg] http://nginx.org/packages/mainline/ubuntu `lsb_release -cs` nginx" > /etc/apt/sources.list.d/nginx.list \
            && echo -e "Package: *\nPin: origin nginx.org\nPin: release o=nginx\nPin-Priority: 900\n" > /etc/apt/preferences.d/99nginx \
            && apt update -y && apt install -y nginx \
            && mkdir -p /etc/systemd/system/nginx.service.d \
            && echo -e "[Service]\nExecStartPost=/bin/sleep 0.1" > /etc/systemd/system/nginx.service.d/override.conf \
            && systemctl daemon-reload && rm -f /etc/nginx/conf.d/default.conf \
            && systemctl enable --now nginx
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

# 下载并复制 p.example.com.conf 并修改
echo "下载并创建 $you_domain 配置文件..."
curl -o "$you_domain.conf" https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/conf.d/p.example.com.conf
sed -i "s/p.example.com/$you_domain/g" "$you_domain.conf"
sed -i "s/emby.example.com/$r_domain/g" "$you_domain.conf"

# 移动配置文件到 /etc/nginx/conf.d/
echo "移动 $you_domain.conf 到 /etc/nginx/conf.d/"
mv "$you_domain.conf" /etc/nginx/conf.d/

# 检查并安装 acme.sh
echo "检查 acme.sh 是否已安装..."
if [[ ! -f "$HOME/.acme.sh/acme.sh" ]]; then
    echo "acme.sh 未安装，正在安装..."
    apt install -y socat
    curl https://get.acme.sh | sh
    ~/.acme.sh/acme.sh --upgrade --auto-upgrade
    ~/.acme.sh/acme.sh --set-default-ca --server letsencrypt
else
    echo "acme.sh 已安装，跳过安装步骤。"
fi

# 申请并安装 ECC 证书
echo "申请 ECC 证书..."
mkdir -p "/etc/nginx/certs/$you_domain"
~/.acme.sh/acme.sh --issue -d "$you_domain" --standalone --keylength ec-256
~/.acme.sh/acme.sh --install-cert -d "$you_domain" --ecc \
    --fullchain-file "/etc/nginx/certs/$you_domain/cert" \
    --key-file "/etc/nginx/certs/$you_domain/key" \
    --reloadcmd "nginx -s reload"

echo "反向代理设置完成！"
