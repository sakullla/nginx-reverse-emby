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

normalize_domain() {
  value="$1"
  value=${value#[}
  value=${value%]}
  printf '%s' "$value" | tr '[:upper:]' '[:lower:]'
}

is_wildcard_domain() {
  case "$1" in
    \*.*) return 0 ;;
    *) return 1 ;;
  esac
}

get_acme_cert_name() {
  domain=$(normalize_domain "$1")
  if is_wildcard_domain "$domain"; then
    printf '%s' "${domain#*.}"
    return 0
  fi
  printf '%s' "$domain"
}

cleanup_acme_record() {
  cert_name="$1"
  requested_domain="$2"

  "$ACME_SCRIPT" --remove -d "$cert_name" --ecc $ACME_COMMON_ARGS >/dev/null 2>&1 || true
  "$ACME_SCRIPT" --remove -d "$cert_name" $ACME_COMMON_ARGS >/dev/null 2>&1 || true

  if [ "$requested_domain" != "$cert_name" ]; then
    "$ACME_SCRIPT" --remove -d "$requested_domain" --ecc $ACME_COMMON_ARGS >/dev/null 2>&1 || true
    "$ACME_SCRIPT" --remove -d "$requested_domain" $ACME_COMMON_ARGS >/dev/null 2>&1 || true
  fi

  rm -rf \
    "$ACME_HOME/$cert_name" \
    "$ACME_HOME/${cert_name}_ecc" \
    "$ACME_HOME/$requested_domain" \
    "$ACME_HOME/${requested_domain}_ecc"
}

has_certificate_record() {
  cert_name="$1"
  "$ACME_SCRIPT" --info -d "$cert_name" --ecc $ACME_COMMON_ARGS 2>/dev/null | grep -q "RealFullChainPath"
}

request_certificate() {
  requested_domain="$1"
  cert_name="$2"

  cleanup_acme_record "$cert_name" "$requested_domain"
  if is_wildcard_domain "$requested_domain"; then
    "$ACME_SCRIPT" --issue $ACME_COMMON_ARGS --server "$ACME_CA" --dns "dns_$ACME_DNS_PROVIDER" -d "$cert_name" -d "$requested_domain" --keylength ec-256
  else
    "$ACME_SCRIPT" --issue $ACME_COMMON_ARGS --server "$ACME_CA" --dns "dns_$ACME_DNS_PROVIDER" -d "$cert_name" --keylength ec-256
  fi
}

install_certificate_files() {
  cert_name="$1"
  target_dir="$2"

  "$ACME_SCRIPT" --install-cert -d "$cert_name" --ecc $ACME_COMMON_ARGS \
    --fullchain-file "$target_dir/cert" \
    --key-file "$target_dir/key"
}

prepare_certificate_context() {
  [ -n "$DOMAIN" ] || { log "domain is required"; exit 1; }
  [ -n "$TARGET_DIR" ] || { log "target dir is required"; exit 1; }
  [ "$ACME_DNS_PROVIDER" = "cf" ] || { log "only Cloudflare dns provider is supported"; exit 1; }
  [ -n "${CF_Token:-}" ] || { log "CF_Token is required"; exit 1; }

  ensure_acme
  export CF_Token="${CF_Token}"
  [ -n "${CF_Account_ID:-}" ] && export CF_Account_ID="${CF_Account_ID}"

  requested_domain=$(normalize_domain "$DOMAIN")
  cert_name=$(get_acme_cert_name "$requested_domain")
  mkdir -p "$TARGET_DIR"
}

issue_certificate() {
  prepare_certificate_context

  if ! has_certificate_record "$cert_name"; then
    log "issuing certificate for $requested_domain"
    request_certificate "$requested_domain" "$cert_name"
  else
    log "certificate already exists for $requested_domain, reinstalling latest copy"
  fi

  install_certificate_files "$cert_name" "$TARGET_DIR"

  log "certificate ready: $requested_domain -> $TARGET_DIR"
}

renew_certificate() {
  prepare_certificate_context

  if has_certificate_record "$cert_name"; then
    log "renewing certificate for $requested_domain"
    "$ACME_SCRIPT" --renew -d "$cert_name" --ecc $ACME_COMMON_ARGS
  else
    log "no existing acme record for $requested_domain, issuing a new certificate"
    request_certificate "$requested_domain" "$cert_name"
  fi

  install_certificate_files "$cert_name" "$TARGET_DIR"
  log "certificate synced: $requested_domain -> $TARGET_DIR"
}

case "$COMMAND" in
  issue)
    issue_certificate
    ;;
  renew)
    renew_certificate
    ;;
  *)
    log "usage: $0 <issue|renew> <domain> <target-dir>"
    exit 1
    ;;
esac
