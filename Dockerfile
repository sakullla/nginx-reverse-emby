# 使用最新的 Nginx 镜像作为基础
FROM nginx:latest

# 删除 Nginx 默认配置文件，避免冲突
RUN rm -f /etc/nginx/conf.d/default.conf

# 复制自定义的 nginx.conf 文件
COPY nginx.conf /etc/nginx/nginx.conf

# 创建一个专门存放配置模板的目录
RUN mkdir -p /etc/nginx/templates/

# 复制你的动态配置脚本到入口点目录
COPY 25-dynamic-reverse-proxy.sh /docker-entrypoint.d/
# 复制你的配置文件模板到专门的模板目录
COPY conf.d/p.example.com.no_tls.conf /etc/nginx/templates/

# 确保动态配置脚本是可执行的
RUN chmod +x /docker-entrypoint.d/25-dynamic-reverse-proxy.sh

# 暴露内部端口，供 Traefik 访问
EXPOSE 80