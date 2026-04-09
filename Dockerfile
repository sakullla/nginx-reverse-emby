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

FROM golang:1.24-bookworm AS go-builder
WORKDIR /src/go-agent
COPY go-agent/ ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/nre-agent-linux-amd64 ./cmd/nre-agent && \
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /out/nre-agent-linux-arm64 ./cmd/nre-agent && \
    CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o /out/nre-agent-darwin-amd64 ./cmd/nre-agent && \
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o /out/nre-agent-darwin-arm64 ./cmd/nre-agent
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /out/nre-agent ./cmd/nre-agent

FROM debian:bookworm-slim AS go-agent-runtime
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates; \
    rm -rf /var/lib/apt/lists/*
COPY --from=go-builder /out/nre-agent /usr/local/bin/nre-agent
ENTRYPOINT ["/usr/local/bin/nre-agent"]

FROM node:24-trixie-slim AS control-plane-runtime
ENV NODE_ENV=production \
    PANEL_BACKEND_HOST=0.0.0.0 \
    PANEL_BACKEND_PORT=3000
WORKDIR /opt/nginx-reverse-emby
COPY scripts/ ./scripts/
COPY examples/ ./examples/
COPY panel/backend/ ./panel/backend/
COPY --from=frontend-builder /build/dist ./panel/frontend/dist/
COPY --from=backend-builder /build/node_modules ./panel/backend/node_modules/
COPY --from=go-builder /out/nre-agent-linux-amd64 ./panel/public/agent-assets/nre-agent-linux-amd64
COPY --from=go-builder /out/nre-agent-linux-arm64 ./panel/public/agent-assets/nre-agent-linux-arm64
COPY --from=go-builder /out/nre-agent-darwin-amd64 ./panel/public/agent-assets/nre-agent-darwin-amd64
COPY --from=go-builder /out/nre-agent-darwin-arm64 ./panel/public/agent-assets/nre-agent-darwin-arm64
RUN set -eux; \
    find ./scripts -type f -name '*.sh' -exec sed -i 's/\r$//' {} +; \
    chmod +x ./panel/backend/server.js ./scripts/*.sh ./panel/public/agent-assets/*; \
    mkdir -p ./panel/data

VOLUME ["/opt/nginx-reverse-emby/panel/data"]
EXPOSE 3000
CMD ["node", "/opt/nginx-reverse-emby/panel/backend/server.js"]
