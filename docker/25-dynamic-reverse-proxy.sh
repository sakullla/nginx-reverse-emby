#!/bin/sh
set -e

# 定义模板文件的路径
TEMPLATE_FILE="/etc/nginx/templates/default.conf"

# 日志输出函数
entrypoint_log() {
    if [ -z "${NGINX_ENTRYPOINT_QUIET_LOGS:-}" ]; then
        echo "$@"
    fi
}

# 检查模板文件是否存在
if [ ! -f "$TEMPLATE_FILE" ]; then
    entrypoint_log "$0: 错误：模板文件 $TEMPLATE_FILE 未找到！"
    exit 1
fi

entrypoint_log "$0: 正在寻找 DOMAIN_N, PROXY_N, PORT_N 变量来生成 Nginx 配置。"
template_content=$(cat "$TEMPLATE_FILE")

i=1
while true; do
    # 构造环境变量名
    DOMAIN_VAR="DOMAIN_${i}"
    PROXY_VAR="PROXY_${i}"
    PORT_VAR="PORT_${i}"

    # 动态获取环境变量的值
    domain_val=$(eval echo "\$$DOMAIN_VAR")
    proxy_val=$(eval echo "\$$PROXY_VAR")
    port_val=$(eval echo "\$$PORT_VAR")

    # 只要 DOMAIN 和 PROXY 存在，就继续处理
    if [ -n "$domain_val" ] && [ -n "$proxy_val" ]; then
        entrypoint_log "$0: 找到配对：DOMAIN_$i=$domain_val, PROXY_$i=$proxy_val"

        # 提取纯域名和路径
        domain_name=$(echo "$domain_val" | sed -E 's|https?://([^/:]+).*|\1|')
        domain_path=$(echo "$domain_val" | sed -E 's|https?://[^/]+(.*)|\1|')
        if [ -z "$domain_path" ]; then
            domain_path="/"
        fi

        # 获取前端端口，如果未提供 PORT_N，则默认为 80
        if [ -z "$port_val" ]; then
            frontend_port="80"
            entrypoint_log "$0: PORT_$i 未设置，使用默认端口 80。"
        else
            frontend_port="$port_val"
            entrypoint_log "$0: PORT_$i=$frontend_port"
        fi

        # 获取解析器，如果没有则使用默认值
        resolver="${NGINX_LOCAL_RESOLVERS:-1.1.1.1}"

        # 替换模板中的变量
        generated_block=$(echo "$template_content" | \
            sed "s|\${frontend_port}|$frontend_port|g" | \
            sed "s|\${domain_name}|$domain_name|g" | \
            sed "s|\${resolver}|$resolver|g" | \
            sed "s|\${domain_path}|$domain_path|g" | \
            sed "s|\${proxy_target}|$proxy_val|g")

        # 构造唯一的配置文件名
        config_filename="${domain_name}.${frontend_port}.conf"

        # 将生成的块写入到它自己的文件中
        echo "$generated_block" > "/etc/nginx/conf.d/$config_filename"
        entrypoint_log "$0: Nginx 配置已生成: /etc/nginx/conf.d/$config_filename"

        i=$((i + 1))
    else
        # 当找不到连续的配对时停止
        break
    fi
done

if [ "$i" -eq 1 ]; then
    entrypoint_log "$0: 未找到 DOMAIN_N/PROXY_N 配对。未生成任何 Nginx 配置。"
else
    entrypoint_log "$0: 成功生成了 $((i-1)) 个 Nginx 配置。"
fi

exit 0