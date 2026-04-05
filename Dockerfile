FROM node:24-trixie-slim AS node-base

FROM node-base AS frontend-builder
WORKDIR /build
COPY panel/frontend/package*.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY panel/frontend/ ./
RUN npm run build

FROM node-base AS backend-builder
WORKDIR /build
ENV NODE_ENV=production
COPY panel/backend/package*.json ./
COPY panel/backend/prisma ./prisma/
COPY panel/backend/prisma.config.ts ./prisma.config.ts
RUN --mount=type=cache,target=/root/.npm npm ci --omit=dev
RUN npx prisma generate

FROM nginx:1.29.7-trixie

COPY docker/ /tmp/docker/
COPY scripts/ /opt/nginx-reverse-emby/scripts/
COPY examples/ /opt/nginx-reverse-emby/examples/
COPY --from=frontend-builder /build/dist /opt/nginx-reverse-emby/panel/frontend/
COPY --from=backend-builder /usr/local/bin/node /usr/local/bin/node
COPY --from=backend-builder /build/node_modules /opt/nginx-reverse-emby/panel/backend/node_modules
COPY panel/backend/server.js panel/backend/http-rule-request-headers.js panel/backend/relay-listener-normalize.js panel/backend/version-policy-normalize.js panel/backend/storage.js panel/backend/storage-json.js panel/backend/storage-sqlite.js panel/backend/storage-prisma-core.js panel/backend/storage-prisma-worker.js /opt/nginx-reverse-emby/panel/backend/
COPY panel/backend/prisma/ /opt/nginx-reverse-emby/panel/backend/prisma/

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends socat cron; \
    rm -rf /var/lib/apt/lists/*; \
    find /tmp/docker /opt/nginx-reverse-emby/scripts -type f -name '*.sh' -exec sed -i 's/\r$//' {} +; \
    rm -f /etc/nginx/conf.d/default.conf; \
    mkdir -p /etc/nginx/templates /etc/nginx/conf.d/dynamic /etc/nginx/stream-conf.d/dynamic /opt/nginx-reverse-emby/panel/data /opt/nginx-reverse-emby/nginx; \
    mv /tmp/docker/nginx.conf /opt/nginx-reverse-emby/nginx/nginx.conf.template; \
    mv /tmp/docker/agent.nginx.conf.template /opt/nginx-reverse-emby/nginx/agent.nginx.conf.template; \
    mv /tmp/docker/default.conf.template /etc/nginx/templates/default.conf; \
    mv /tmp/docker/default.direct.no_tls.conf.template /etc/nginx/templates/default.direct.no_tls.conf; \
    mv /tmp/docker/default.direct.tls.conf.template /etc/nginx/templates/default.direct.tls.conf; \
    mv /tmp/docker/panel.conf.template /opt/nginx-reverse-emby/panel/panel.conf.template; \
    mv /tmp/docker/10-nginx-config.sh /docker-entrypoint.d/10-nginx-config.sh; \
    mv /tmp/docker/15-panel-config.sh /docker-entrypoint.d/15-panel-config.sh; \
    mv /tmp/docker/20-panel-backend.sh /docker-entrypoint.d/20-panel-backend.sh; \
    mv /tmp/docker/25-dynamic-reverse-proxy.sh /docker-entrypoint.d/25-dynamic-reverse-proxy.sh; \
    mv /tmp/docker/30-acme-renew.sh /docker-entrypoint.d/30-acme-renew.sh; \
    chmod +x /docker-entrypoint.d/10-nginx-config.sh /docker-entrypoint.d/15-panel-config.sh /docker-entrypoint.d/20-panel-backend.sh /docker-entrypoint.d/25-dynamic-reverse-proxy.sh /docker-entrypoint.d/30-acme-renew.sh; \
    chmod +x /opt/nginx-reverse-emby/panel/backend/server.js /opt/nginx-reverse-emby/scripts/*.sh; \
    rm -rf /tmp/docker

# 统一数据持久化卷
VOLUME ["/opt/nginx-reverse-emby/panel/data"]

EXPOSE 3000 80 443 8080
