FROM node:24-trixie-slim AS node-base

FROM node-base AS frontend-builder
WORKDIR /build
COPY panel/frontend/package*.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY panel/frontend/ ./
RUN npm run build

FROM golang:1.26.2-trixie AS go-builder
WORKDIR /src/go-agent
COPY go-agent/go.mod go-agent/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
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

FROM golang:1.26.2-trixie AS backend-go-builder
WORKDIR /src
COPY go-agent/go.mod go-agent/go.sum ./go-agent/
COPY panel/backend-go/go.mod panel/backend-go/go.sum ./panel/backend-go/
WORKDIR /src/panel/backend-go
RUN --mount=type=cache,target=/go/pkg/mod go mod download
WORKDIR /src
COPY go-agent/ ./go-agent/
COPY panel/backend-go/ ./panel/backend-go/
WORKDIR /src/panel/backend-go
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -o /out/nre-control-plane ./cmd/nre-control-plane

FROM debian:trixie-slim AS go-agent-runtime
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates; \
    rm -rf /var/lib/apt/lists/*
COPY --from=go-builder /out/nre-agent /usr/local/bin/nre-agent
ENTRYPOINT ["/usr/local/bin/nre-agent"]

FROM debian:trixie-slim AS control-plane-runtime
ENV PANEL_BACKEND_HOST=0.0.0.0 \
    PANEL_BACKEND_PORT=8080
WORKDIR /opt/nginx-reverse-emby
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates; \
    rm -rf /var/lib/apt/lists/*
COPY scripts/ ./scripts/
COPY --from=frontend-builder /build/dist ./panel/frontend/dist/
COPY --from=backend-go-builder /out/nre-control-plane /usr/local/bin/nre-control-plane
COPY --from=go-builder /out/nre-agent-linux-amd64 ./panel/public/agent-assets/nre-agent-linux-amd64
COPY --from=go-builder /out/nre-agent-linux-arm64 ./panel/public/agent-assets/nre-agent-linux-arm64
COPY --from=go-builder /out/nre-agent-darwin-amd64 ./panel/public/agent-assets/nre-agent-darwin-amd64
COPY --from=go-builder /out/nre-agent-darwin-arm64 ./panel/public/agent-assets/nre-agent-darwin-arm64
RUN set -eux; \
    find ./scripts -type f -name '*.sh' -exec sed -i 's/\r$//' {} +; \
    chmod +x /usr/local/bin/nre-control-plane ./scripts/*.sh ./panel/public/agent-assets/*; \
    mkdir -p ./panel/data

VOLUME ["/opt/nginx-reverse-emby/panel/data"]
EXPOSE 8080
CMD ["/usr/local/bin/nre-control-plane"]
