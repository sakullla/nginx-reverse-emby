FROM node:24-bookworm-slim AS node-runtime

FROM node:24-alpine AS frontend-builder

WORKDIR /build

COPY panel/frontend/package*.json ./
RUN npm ci

COPY panel/frontend/ ./
RUN npm run build

FROM nginx:latest

COPY docker/ /tmp/docker/
COPY panel/backend/ /opt/nginx-reverse-emby/panel/backend/
COPY --from=frontend-builder /build/dist /opt/nginx-reverse-emby/panel/frontend/
COPY --from=node-runtime /usr/local/bin/node /usr/local/bin/node

RUN set -eux; \
    if command -v apk >/dev/null 2>&1; then \
        apk add --no-cache curl socat openssl ca-certificates cronie; \
    elif command -v apt-get >/dev/null 2>&1; then \
        apt-get update; \
        apt-get install -y --no-install-recommends curl socat openssl ca-certificates cron; \
        rm -rf /var/lib/apt/lists/*; \
    else \
        echo "Unsupported base image package manager" >&2; \
        exit 1; \
    fi; \
    rm -f /etc/nginx/conf.d/default.conf; \
    mkdir -p /etc/nginx/templates /etc/nginx/conf.d/dynamic /opt/nginx-reverse-emby/panel/data; \
    mv /tmp/docker/nginx.conf /etc/nginx/nginx.conf; \
    mv /tmp/docker/default.conf.template /etc/nginx/templates/default.conf; \
    mv /tmp/docker/default.direct.no_tls.conf.template /etc/nginx/templates/default.direct.no_tls.conf; \
    mv /tmp/docker/default.direct.tls.conf.template /etc/nginx/templates/default.direct.tls.conf; \
    mv /tmp/docker/panel.conf.template /opt/nginx-reverse-emby/panel/panel.conf.template; \
    mv /tmp/docker/15-panel-config.sh /docker-entrypoint.d/15-panel-config.sh; \
    mv /tmp/docker/20-panel-backend.sh /docker-entrypoint.d/20-panel-backend.sh; \
    mv /tmp/docker/25-dynamic-reverse-proxy.sh /docker-entrypoint.d/25-dynamic-reverse-proxy.sh; \
    mv /tmp/docker/30-acme-renew.sh /docker-entrypoint.d/30-acme-renew.sh; \
    chmod +x /docker-entrypoint.d/15-panel-config.sh /docker-entrypoint.d/20-panel-backend.sh /docker-entrypoint.d/25-dynamic-reverse-proxy.sh /docker-entrypoint.d/30-acme-renew.sh; \
    chmod +x /opt/nginx-reverse-emby/panel/backend/server.js; \
    rm -rf /tmp/docker

# 统一数据持久化卷
VOLUME ["/opt/nginx-reverse-emby/panel/data"]

EXPOSE 3000 80 443 8080
