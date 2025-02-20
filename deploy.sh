#!/bin/bash

set -e

# 初始化变量
you_domain=""
r_domain=""
http_backend="no"  # 默认使用 HTTPS
http_frontend="no"  # 默认前端也使用 HTTPS
frontend_port=""  # 默认无端口

# 显示帮助信息
show_help() {
    echo "用法:  [选项]"
    echo "  -y, --you_domain      指定前端域名 (例如: example.com)"
    echo "  -r, --r_domain        指定反代emby域名 (例如: backend.com)"
    echo "  -b, --http_backend    反代emby使用 HTTP 连接到后端 (默认: 否)"
    echo "  -f, --http_frontend   反代emby使用 HTTP 作为前端访问 (默认: 否)"
    echo "  -p, --frontend_port   反代emby指定前端端口 (例如: 8443, 默认: 空)"
    echo "  -h, --help            显示此帮助信息"
    exit 0
}

# 初始化变量
you_domain=""
r_domain=""
http_backend="no"  # 默认使用 HTTPS
http_frontend="no"  # 默认前端也使用 HTTPS
frontend_port=""  # 默认无端口

# 解析参数
while [[ "$#" -gt 0 ]]; do
    case "$1" in
        -y|--you_domain)
            shift
            you_domain="$1"
            ;;
        -r|--r_domain)
            shift
            r_domain="$1"
            ;;
        -b|--http_backend)
            http_backend="yes"
            ;;
        -f|--http_frontend)
            http_frontend="yes"
            ;;
        -p|--frontend_port)
            shift
            frontend_port="$1"
            ;;
        -h|--help)
            show_help
            ;;
        *)
            echo "未知参数: $1"
            exit 1
            ;;
    esac
    shift
done

# 交互模式
if [[ -z "$you_domain" || -z "$r_domain" ]]; then
    echo "--- 交互模式: 配置反向代理 ---"
    echo "输入参数或直接按 Enter 使用默认值。"
    read -p "请输入你的域名 (默认: you.example.com): " input_you_domain
    read -p "请输入要反代emby的域名 (默认: r.example.com): " input_r_domain
    read -p "反代emby后端推流地址是否使用 HTTP? (默认: no, 输入 yes 则使用 HTTP): " input_http_backend
    read -p "反代emby前端访问地址是否使用 HTTP? (默认: no, 输入 yes 则使用 HTTP): " input_http_frontend
    read -p "反代emby前端端口号 (默认: 空, 例如 8443): " input_frontend_port

    you_domain="${input_you_domain:-you.example.com}"
    r_domain="${input_r_domain:-r.example.com}"
    http_backend="${input_http_backend:-no}"
    http_frontend="${input_http_frontend:-no}"
    frontend_port="${input_frontend_port}"
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

# 如果 http_frontend 选择使用 HTTP，先替换 https://emby.example.com
if [[ "$http_frontend" == "yes" ]]; then
    sed -i "s/https:\/\/emby.example.com/http:\/\/emby.example.com/g" "$you_domain.conf"
fi

# 如果 frontend_port 不为空，修改 emby.example.com 加上端口
if [[ -n "$frontend_port" ]]; then
    sed -i "s/emby.example.com/emby.example.com:$frontend_port/g" "$you_domain.conf"
fi

# 替换域名信息
sed -i "s/p.example.com/$you_domain/g" "$you_domain.conf"
sed -i "s/emby.example.com/$r_domain/g" "$you_domain.conf"

# 如果 http_backend 选择使用 HTTP，替换 https://$website
if [[ "$http_backend" == "yes" ]]; then
    sed -i "s/https:\/\/\$website/http:\/\/\$website/g" "$you_domain.conf"
fi

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

# 申请证书
set +e
output=$(~/.acme.sh/acme.sh --issue -d "$you_domain" --standalone --keylength ec-256 2>&1)
status=$?
echo "$output" | grep -q "Domains not changed"
cert_status=$?
echo "$output"
set -e

# 如果申请失败（非零退出状态），则退出脚本
if [ $status -ne 0 ] && [ $cert_status -ne 0 ]; then
    echo "证书申请失败，请检查错误信息！"
    exit 1
fi

# 安装证书
~/.acme.sh/acme.sh --install-cert -d "$you_domain" --ecc \
    --fullchain-file "/etc/nginx/certs/$you_domain/cert" \
    --key-file "/etc/nginx/certs/$you_domain/key" \
    --reloadcmd "nginx -s reload"

echo "重新加载 Nginx..."
nginx -s reload

echo "反向代理设置完成！"
