# 使用最新的 Nginx 镜像作为基础
FROM nginx:latest

# 将所有需要的文件一次性复制到一个临时目录中
COPY docker/ /tmp/docker/

# 在一个指令层中完成所有文件操作：
# 1. 删除默认配置
# 2. 创建模板目录
# 3. 将文件从临时目录移动到最终位置
# 4. 设置脚本的可执行权限
# 5. 清理临时目录
RUN rm -f /etc/nginx/conf.d/default.conf \
    && mkdir -p /etc/nginx/templates/ \
    && mv /tmp/docker/nginx.conf /etc/nginx/nginx.conf \
    && mv /tmp/docker/default.conf.template /etc/nginx/templates/default.conf \
    && mv /tmp/docker/25-dynamic-reverse-proxy.sh /docker-entrypoint.d/ \
    && chmod +x /docker-entrypoint.d/25-dynamic-reverse-proxy.sh \
    && rm -rf /tmp/docker

# 暴露 80 端口
EXPOSE 80