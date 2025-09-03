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

entrypoint_log "$0: 正在寻找 PROXY_RULE_N 变量来生成 Nginx 配置。"
template_content=$(cat "$TEMPLATE_FILE")

config_count=0
i=1
while true; do
    # 构造环境变量名
    RULE_VAR="PROXY_RULE_${i}"

    # 动态获取环境变量的值
    rule_val=$(eval echo "\$$RULE_VAR")

    # 如果找到了规则，就继续处理
    if [ -n "$rule_val" ]; then
        entrypoint_log "$0: 找到规则 PROXY_RULE_$i=$rule_val"

        # 使用逗号分割前端和后端 URL
        frontend_url=$(echo "$rule_val" | cut -d',' -f1)
        backend_url=$(echo "$rule_val" | cut -d',' -f2-)

        if [ -z "$frontend_url" ] || [ -z "$backend_url" ]; then
            entrypoint_log "$0: 警告：跳过格式错误的规则 '$rule_val'"
            i=$((i + 1))
            continue
        fi

        entrypoint_log "$0: 正在处理: $frontend_url -> $backend_url"

        # 提取纯域名和路径
        domain_name=$(echo "$frontend_url" | sed -E 's|https?://([^/:]+).*|\1|')
        domain_path=$(echo "$frontend_url" | sed -E 's|https?://[^/]+(.*)|\1|')
        if [ -z "$domain_path" ]; then
            domain_path="/"
        fi


        # 从前端 URL 中提取端口，如果不存在则默认为 80
        frontend_port=$(echo "$frontend_url" | sed -nE 's|https?://[^/:]+:([0-9]+).*|\1|p')
        if [ -z "$frontend_port" ]; then
            frontend_port="80"
        fi
         entrypoint_log "$0: Nginx domain_name: $domain_name domain_path: $domain_path nginx-resolver: $NGINX_LOCAL_RESOLVERS"
        # 获取解析器
        resolver="${NGINX_LOCAL_RESOLVERS:-1.1.1.1}"

        # 替换模板中的变量
        generated_block=$(echo "$template_content" | \
            sed "s|\${frontend_port}|$frontend_port|g" | \
            sed "s|\${domain_name}|$domain_name|g" | \
            sed "s|\${resolver}|$resolver|g" | \
            sed "s|\${domain_path}|$domain_path|g" | \
            sed "s|\${proxy_target}|$backend_url|g")

        # 构造唯一的配置文件名
        config_filename="${domain_name}.${frontend_port}.conf"

        # 将生成的块写入到它自己的文件中
        echo "$generated_block" > "/etc/nginx/conf.d/$config_filename"
        entrypoint_log "$0: Nginx 配置已生成: /etc/nginx/conf.d/$config_filename"
        config_count=$((config_count + 1))

        i=$((i + 1))
    else
        # 当找不到连续的配对时停止
        break
    fi
done

if [ "$config_count" -eq 0 ]; then
    entrypoint_log "$0: 未找到 PROXY_RULE_N 变量。未生成任何 Nginx 配置。"
else
    entrypoint_log "$0: 成功生成了 $config_count 个 Nginx 配置。"
fi

exit 0