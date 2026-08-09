FROM node:24-trixie-slim AS node-base

FROM node-base AS frontend-builder
WORKDIR /build
COPY panel/frontend/package*.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY panel/frontend/ ./
RUN npm run build

FROM golang:1.26.5-trixie AS go-builder
ARG GO_AGENT_LDFLAGS="-s -w"
WORKDIR /src
COPY plugin-sdk/go/go.mod plugin-sdk/go/go.sum ./plugin-sdk/go/
COPY go-agent/go.mod go-agent/go.sum ./go-agent/
WORKDIR /src/go-agent
RUN --mount=type=cache,target=/go/pkg/mod go mod download
WORKDIR /src
COPY plugin-sdk/ ./plugin-sdk/
COPY go-agent/ ./go-agent/
WORKDIR /src/go-agent
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "${GO_AGENT_LDFLAGS}" -o /out/nre-agent-linux-amd64 ./cmd/nre-agent && \
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "${GO_AGENT_LDFLAGS}" -o /out/nre-agent-linux-arm64 ./cmd/nre-agent && \
    CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "${GO_AGENT_LDFLAGS}" -o /out/nre-agent-darwin-amd64 ./cmd/nre-agent && \
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "${GO_AGENT_LDFLAGS}" -o /out/nre-agent-darwin-arm64 ./cmd/nre-agent && \
    for binary in /out/nre-agent-linux-amd64 /out/nre-agent-linux-arm64 /out/nre-agent-darwin-amd64 /out/nre-agent-darwin-arm64; do \
      filename="$(basename "$binary")"; \
      platform="${filename#nre-agent-}"; \
      digest="$(sha256sum "$binary" | awk '{print $1}')"; \
      size="$(stat -c %s "$binary")"; \
      printf '{"schema_version":1,"filename":"%s","platform":"%s","sha256":"%s","size":%s}\n' "$filename" "$platform" "$digest" "$size" > "$binary.manifest.json"; \
    done
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags "${GO_AGENT_LDFLAGS}" -o /out/nre-agent ./cmd/nre-agent

FROM golang:1.26.5-trixie AS backend-go-builder
ARG APP_VERSION=dev
ARG BUILD_TIME=dev
ARG GO_VERSION=dev
WORKDIR /src
COPY plugin-sdk/go/go.mod plugin-sdk/go/go.sum ./plugin-sdk/go/
COPY go-agent/go.mod go-agent/go.sum ./go-agent/
COPY panel/backend-go/go.mod panel/backend-go/go.sum ./panel/backend-go/
WORKDIR /src/panel/backend-go
RUN --mount=type=cache,target=/go/pkg/mod go mod download
WORKDIR /src
COPY plugin-sdk/ ./plugin-sdk/
COPY go-agent/ ./go-agent/
COPY panel/backend-go/ ./panel/backend-go/
WORKDIR /src/panel/backend-go
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build \
      -ldflags "-s -w -X main.appVersion=${APP_VERSION} -X main.buildTime=${BUILD_TIME} -X main.goVersion=${GO_VERSION}" \
      -o /out/nre-control-plane ./cmd/nre-control-plane

FROM gcr.io/distroless/static-debian12 AS go-agent-runtime
COPY --from=go-builder /out/nre-agent /usr/local/bin/nre-agent
ENTRYPOINT ["/usr/local/bin/nre-agent"]

FROM debian:trixie-slim AS control-plane-runtime
ENV PANEL_BACKEND_HOST=0.0.0.0 \
    PANEL_BACKEND_PORT=8080
WORKDIR /opt/nginx-reverse-emby
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates tzdata; \
    rm -rf /var/lib/apt/lists/*
COPY scripts/ ./scripts/
COPY official-market.lock ./official-market.lock
COPY --from=frontend-builder /build/dist ./panel/frontend/dist/
COPY --from=backend-go-builder /out/nre-control-plane /usr/local/bin/nre-control-plane
COPY --from=go-builder /out/nre-agent-linux-amd64 ./panel/public/agent-assets/nre-agent-linux-amd64
COPY --from=go-builder /out/nre-agent-linux-arm64 ./panel/public/agent-assets/nre-agent-linux-arm64
COPY --from=go-builder /out/nre-agent-darwin-amd64 ./panel/public/agent-assets/nre-agent-darwin-amd64
COPY --from=go-builder /out/nre-agent-darwin-arm64 ./panel/public/agent-assets/nre-agent-darwin-arm64
COPY --from=go-builder /out/nre-agent-linux-amd64.manifest.json ./panel/public/agent-assets/nre-agent-linux-amd64.manifest.json
COPY --from=go-builder /out/nre-agent-linux-arm64.manifest.json ./panel/public/agent-assets/nre-agent-linux-arm64.manifest.json
COPY --from=go-builder /out/nre-agent-darwin-amd64.manifest.json ./panel/public/agent-assets/nre-agent-darwin-amd64.manifest.json
COPY --from=go-builder /out/nre-agent-darwin-arm64.manifest.json ./panel/public/agent-assets/nre-agent-darwin-arm64.manifest.json
RUN set -eux; \
    find ./scripts -type f -name '*.sh' -exec sed -i 's/\r$//' {} +; \
    chmod +x /usr/local/bin/nre-control-plane ./scripts/*.sh ./panel/public/agent-assets/nre-agent-*; \
    chmod 644 ./panel/public/agent-assets/*.manifest.json; \
    mkdir -p ./panel/data

VOLUME ["/opt/nginx-reverse-emby/panel/data"]
# The image exposes only the existing panel/control listener. Internal relay
# mTLS runs on agent-managed data-plane listeners, not a second control port.
EXPOSE 8080
CMD ["/usr/local/bin/nre-control-plane"]
