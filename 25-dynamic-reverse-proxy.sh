#!/bin/sh
set -e

# 日志输出函数，用于打印信息
entrypoint_log() {
    if [ -z "${NGINX_ENTRYPOINT_QUIET_LOGS:-}" ]; then
        echo "$@"
    fi
}

entrypoint_log "$0: 正在寻找 DOMAIN_N 和 PROXY_N 变量来生成 Nginx 配置。"

# 将整个模板内容存储在一个变量中
template_content=$(cat <<'EOF'
server {
    listen ${you_frontend_port};
    listen [::]:${you_frontend_port};
    http2 on;

    server_name                ${you_domain}; # 填由 Nginx 加载的 SSL 证书中包含的域名，建议将域名指向服务端的 IP

    resolver                   ${resolver};
    resolver_timeout           5s;

    client_header_timeout      1h;
    keepalive_timeout          30m;
    client_header_buffer_size  8k;
    proxy_buffering on;
    proxy_buffer_size 2m;               # 缓冲区大小（根据 BDP 调整）
    proxy_buffers 32 2m;                # 16 个 1MB 缓冲区
    proxy_busy_buffers_size 4m;         # 允许最大缓冲区使用

    # 动态生成的反向代理 location 块
    location ${you_domain_path} {
        proxy_pass                            ${r_domain_full};
        proxy_set_header Host                 $proxy_host;

        proxy_http_version                    1.1;
        proxy_cache_bypass                    $http_upgrade;
        proxy_set_header Upgrade              $http_upgrade;
        proxy_set_header Connection           $connection_upgrade;

        proxy_ssl_server_name                 on;

        proxy_connect_timeout                 60s;
        proxy_send_timeout                    60s;
        proxy_read_timeout                    60s;

        proxy_redirect ~^(https?)://([^:/]+(?::\d+)?)(/.+)$ $http_x_forwarded_proto://$http_x_forwarded_host:$http_x_forwarded_port/backstream/$1/$2$3;
    }


    location ~  ^/backstream/(https?)/([^/]+)  {
        set $website                          $1://$2;
        rewrite ^/backstream/(https?)/([^/]+)(/.+)$  $3 break;
        proxy_pass                            $website; #如果重定向的地址是http这里需要替换为http

        proxy_set_header Host                 $proxy_host;

        proxy_http_version                    1.1;
        proxy_cache_bypass                    $http_upgrade;
        proxy_set_header Upgrade              $http_upgrade;
        proxy_set_header Connection           $connection_upgrade;

        proxy_ssl_server_name                 on;

        proxy_connect_timeout                 60s;
        proxy_send_timeout                    60s;
        proxy_read_timeout                    60s;

        proxy_redirect ~^(https?)://([^:/]+(?::\d+)?)(/.+)$ $http_x_forwarded_proto://$http_x_forwarded_host:$http_x_forwarded_port/backstream/$1/$2$3;

        proxy_intercept_errors on;
        error_page 307 = @handle_redirect;

    }

    location @handle_redirect {
        set $saved_redirect_location '$upstream_http_location';
        proxy_pass $saved_redirect_location;
        proxy_set_header Host                 $proxy_host;
        proxy_http_version                    1.1;
        proxy_cache_bypass                    $http_upgrade;

        proxy_ssl_server_name                 on;

        proxy_set_header Upgrade              $http_upgrade;
        proxy_set_header Connection           $connection_upgrade;

        proxy_connect_timeout                 60s;
        proxy_send_timeout                    60s;
        proxy_read_timeout                    60s;
    }
}
EOF
)

# 循环遍历查找环境变量
i=1
while true; do
    # 构造环境变量名
    DOMAIN_VAR="DOMAIN_${i}"
    PROXY_VAR="PROXY_${i}"

    # 动态获取环境变量的值
    domain_val=$(eval echo "\$$DOMAIN_VAR")
    proxy_val=$(eval echo "\$$PROXY_VAR")

    # 如果这两个变量都存在且不为空，则继续处理
    if [ -n "$domain_val" ] && [ -n "$proxy_val" ]; then
        entrypoint_log "$0: 找到配对：DOMAIN_$i=$domain_val, PROXY_$i=$proxy_val"

        # 确保 you_domain 是一个干净的域名并从中提取路径
        you_domain_clean=$(echo "$domain_val" | sed -E 's|https?://([^/:]+).*|\1|');

        you_domain_path=$(echo "$domain_val" | sed -E 's|https?://[^/]+(.*)|\1|')
        # 如果没有提供路径，则默认为 /
        if [ -z "$you_domain_path" ]; then
            you_domain_path="/"
        fi

        # 获取前端端口
        you_frontend_port="80"

        # 从全局变量获取解析器，如果没有则使用默认值
        # 使用从 15-local-resolvers.envsh 导出的环境变量
        resolver="${NGINX_LOCAL_RESOLVERS:-1.1.1.1}"

        # 替换模板中的变量并添加到最终的配置文件中
        # 这里的替换使用 | 作为分隔符，并转义模板中的 $ 和 { 以避免问题
        generated_block=$(echo "$template_content" | \
            sed "s|\${you_frontend_port}|$you_frontend_port|g" | \
            sed "s|\${you_domain}|$you_domain_clean|g" | \
            sed "s|\${resolver}|$resolver|g" | \
            sed "s|\${you_domain_path}|$you_domain_path|g" | \
            sed "s|\${r_domain_full}|$proxy_val|g")

        # 构造唯一的配置文件名
        you_domain_config_filename="${you_domain_clean}.${you_frontend_port}.conf"

        # 将生成的块写入到它自己的文件中
        echo "$generated_block" > "/etc/nginx/conf.d/$you_domain_config_filename"

        entrypoint_log "$0: 域名 $you_domain_clean 的 Nginx 配置已在 /etc/nginx/conf.d/$you_domain_config_filename 生成。"

        i=$((i + 1))
    else
        # 当找不到连续的配对时停止
        break
    fi
done

if [ "$i" -eq 1 ]; then
    entrypoint_log "$0: 未找到 DOMAIN_N/PROXY_N 配对。未生成任何 Nginx 配置。"
else
    entrypoint_log "$0: 成功生成了 $((i-1)) 个域名的 Nginx 配置。"
fi

exit 0
