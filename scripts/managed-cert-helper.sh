#!/bin/sh
set -eu

COMMAND="${1:-}"
DOMAIN="${2:-}"
TARGET_DIR="${3:-}"

ACME_HOME="${ACME_HOME:-/opt/nginx-reverse-emby/panel/data/.acme.sh}"
ACME_SCRIPT="$ACME_HOME/acme.sh"
ACME_INSTALL_URL="${ACME_INSTALL_URL:-https://raw.githubusercontent.com/acmesh-official/acme.sh/master/acme.sh}"
ACME_CA="${ACME_CA:-letsencrypt}"
ACME_DNS_PROVIDER="${ACME_DNS_PROVIDER:-cf}"
ACME_COMMON_ARGS="--home $ACME_HOME --config-home $ACME_HOME --cert-home $ACME_HOME"

log() {
  echo "[CERT] $*" >&2
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    log "missing command: $1"
    exit 1
  }
}

install_acme() {
  mkdir -p "$ACME_HOME"
  tmp_dir=$(mktemp -d)
  trap 'rm -rf "$tmp_dir"' EXIT INT TERM
  need_cmd curl
  curl -fsSL "$ACME_INSTALL_URL" -o "$tmp_dir/acme.sh"
  chmod +x "$tmp_dir/acme.sh"
  (
    cd "$tmp_dir"
    sh "$tmp_dir/acme.sh" --install-online --nocron $ACME_COMMON_ARGS
  )
  "$ACME_SCRIPT" --set-default-ca --server "$ACME_CA" $ACME_COMMON_ARGS >/dev/null 2>&1 || true
  rm -rf "$tmp_dir"
  trap - EXIT INT TERM
}

ensure_acme() {
  if [ ! -x "$ACME_SCRIPT" ]; then
    install_acme
  fi
}

issue_certificate() {
  [ -n "$DOMAIN" ] || { log "domain is required"; exit 1; }
  [ -n "$TARGET_DIR" ] || { log "target dir is required"; exit 1; }
  [ "$ACME_DNS_PROVIDER" = "cf" ] || { log "only Cloudflare dns provider is supported"; exit 1; }
  [ -n "${CF_Token:-}" ] || { log "CF_Token is required"; exit 1; }

  ensure_acme
  export CF_Token="${CF_Token}"
  [ -n "${CF_Account_ID:-}" ] && export CF_Account_ID="${CF_Account_ID}"

  mkdir -p "$TARGET_DIR"

  if ! "$ACME_SCRIPT" --info -d "$DOMAIN" --ecc $ACME_COMMON_ARGS 2>/dev/null | grep -q "RealFullChainPath"; then
    log "issuing certificate for $DOMAIN"
    "$ACME_SCRIPT" --remove -d "$DOMAIN" --ecc $ACME_COMMON_ARGS >/dev/null 2>&1 || true
    "$ACME_SCRIPT" --remove -d "$DOMAIN" $ACME_COMMON_ARGS >/dev/null 2>&1 || true
    "$ACME_SCRIPT" --issue $ACME_COMMON_ARGS --server "$ACME_CA" --dns "dns_$ACME_DNS_PROVIDER" -d "$DOMAIN" --keylength ec-256
  else
    log "certificate already exists for $DOMAIN, reinstalling latest copy"
  fi

  "$ACME_SCRIPT" --install-cert -d "$DOMAIN" --ecc $ACME_COMMON_ARGS \
    --fullchain-file "$TARGET_DIR/cert" \
    --key-file "$TARGET_DIR/key"

  log "certificate ready: $DOMAIN -> $TARGET_DIR"
}

case "$COMMAND" in
  issue)
    issue_certificate
    ;;
  *)
    log "usage: $0 issue <domain> <target-dir>"
    exit 1
    ;;
esac
