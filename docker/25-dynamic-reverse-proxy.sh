#!/bin/sh
set -eu

# --- 配置定义 ---
TEMPLATE_FILE="/etc/nginx/templates/default.conf"
DIRECT_NO_TLS_TEMPLATE_FILE="/etc/nginx/templates/default.direct.no_tls.conf"
DIRECT_TLS_TEMPLATE_FILE="/etc/nginx/templates/default.direct.tls.conf"
DYNAMIC_DIR="/etc/nginx/conf.d/dynamic"

# Data root
DATA_ROOT="/opt/nginx-reverse-emby/panel/data"
RULES_FILE="${PANEL_RULES_FILE:-$DATA_ROOT/proxy_rules.csv}"
ACME_HOME="${ACME_HOME:-$DATA_ROOT/.acme.sh}"
DIRECT_CERT_DIR="${DIRECT_CERT_DIR:-$DATA_ROOT/certs}"
DIRECT_CERT_STATE_FILE="${DIRECT_CERT_STATE_FILE:-$DATA_ROOT/.state/active_cert_domains}"

RESOLVER="${NGINX_LOCAL_RESOLVERS:-1.1.1.1}"
PROXY_DEPLOY_MODE="${PROXY_DEPLOY_MODE:-front_proxy}"
FRONT_PROXY_PORT="${FRONT_PROXY_PORT:-3000}"
DIRECT_CERT_MODE="${DIRECT_CERT_MODE:-acme}"
DIRECT_CERT_CLEANUP="${DIRECT_CERT_CLEANUP:-1}"

ACME_SCRIPT="$ACME_HOME/acme.sh"
ACME_INSTALL_URL="${ACME_INSTALL_URL:-https://raw.githubusercontent.com/acmesh-official/acme.sh/master/acme.sh}"
ACME_CA="${ACME_CA:-letsencrypt}"
ACME_DNS_PROVIDER="${ACME_DNS_PROVIDER:-}"
ACME_EMAIL="${ACME_EMAIL:-}"
ACME_STANDALONE_STOP_NGINX="${ACME_STANDALONE_STOP_NGINX:-1}"
NGINX_BIN="${NGINX_BIN:-nginx}"
ACME_COMMON_ARGS="--home $ACME_HOME --config-home $ACME_HOME --cert-home $ACME_HOME"

entrypoint_log() {
    if [ -z "${NGINX_ENTRYPOINT_QUIET_LOGS:-}" ]; then
        echo "[PROXY] $@"
    fi
}

trim_text() {
    printf '%s' "$1" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//'
}

escape_sed_replacement() {
    printf '%s' "$1" | sed -e 's/[\/&]/\\&/g'
}

parse_frontend_url() {
    node -e "let u; try { u = new URL(process.argv[1].trim()); } catch { process.exit(2); }
if (u.protocol !== 'http:' && u.protocol !== 'https:') process.exit(2);
let host = u.hostname;
if (!host) process.exit(2);
if (host.startsWith('[') && host.endsWith(']')) host = host.slice(1, -1);
const port = u.port || (u.protocol === 'https:' ? '443' : '80');
const path = (u.pathname && u.pathname.startsWith('/')) ? u.pathname : '/' + (u.pathname || '');
process.stdout.write(u.protocol.slice(0, -1) + '|' + host + '|' + port + '|' + path);" "$1"
}

format_server_name() {
    case "$1" in
        *:*) printf '[%s]' "$1" ;;
        *) printf '%s' "$1" ;;
    esac
}

sanitize_domain() {
    printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9._-]/_/g'
}

normalize_cert_domain() {
    value="$1"
    value=${value#[}
    value=${value%]}
    printf '%s' "$value"
}

is_true() {
    case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
        1|true|yes|on) return 0 ;;
        *) return 1 ;;
    esac
}

is_ip_address() {
    value=$(normalize_cert_domain "$1")
    if printf '%s' "$value" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$'; then return 0; fi
    if printf '%s' "$value" | grep -Eq '^[0-9A-Fa-f:.]+$' && printf '%s' "$value" | grep -q ':'; then return 0; fi
    return 1
}

normalize_deploy_mode() {
    mode_raw=$(printf '%s' "$PROXY_DEPLOY_MODE" | tr '[:upper:]' '[:lower:]' | tr '-' '_')
    case "$mode_raw" in
        front_proxy|front|upstream|proxy) printf 'front_proxy' ;;
        direct|direct_tls|self_managed|host) printf 'direct' ;;
        *) printf 'front_proxy' ;;
    esac
}

nginx_is_running() {
    nginx_pid_file="/var/run/nginx.pid"
    [ -r "$nginx_pid_file" ] || return 1
    nginx_pid=$(cat "$nginx_pid_file" 2>/dev/null || true)
    [ -n "$nginx_pid" ] || return 1
    kill -0 "$nginx_pid" 2>/dev/null
}


fail_standalone_if_nginx_running() {
    cert_domain="$1"
    if nginx_is_running; then
        echo "[PROXY] error: cannot issue certificate for $cert_domain via standalone while nginx is already running and occupying port 80. Configure ACME_DNS_PROVIDER, pre-create the rule before container startup, or disable PANEL_AUTO_APPLY and restart after saving the rule." >&2
        return 1
    fi
}

ensure_acme_script() {
    if [ -x "$ACME_SCRIPT" ]; then return 0; fi
    mkdir -p "$ACME_HOME"
    entrypoint_log "Installing acme.sh to $ACME_HOME..."
    tmp_acme_dir=$(mktemp -d)
    tmp_acme_install="$tmp_acme_dir/acme.sh"
    if ! curl -fsSL "$ACME_INSTALL_URL" -o "$tmp_acme_install"; then
        entrypoint_log "error: failed to download acme.sh"
        rm -rf "$tmp_acme_dir"
        return 1
    fi
    chmod +x "$tmp_acme_install"
    if ! (
        cd "$tmp_acme_dir" &&
        ./acme.sh --install $ACME_COMMON_ARGS ${ACME_EMAIL:+--accountemail "$ACME_EMAIL"}
    ); then
        rm -rf "$tmp_acme_dir"
        return 1
    fi
    rm -rf "$tmp_acme_dir"
    "$ACME_SCRIPT" --set-default-ca --server "$ACME_CA" $ACME_COMMON_ARGS >/dev/null 2>&1 || true
    return 0
}

cleanup_stale_acme_record() {
    cert_domain="$1"
    if [ ! -x "$ACME_SCRIPT" ]; then return 0; fi
    entrypoint_log "Cleaning up stale acme record for $cert_domain..."
    "$ACME_SCRIPT" --remove -d "$cert_domain" --ecc $ACME_COMMON_ARGS >/dev/null 2>&1 || true
    "$ACME_SCRIPT" --remove -d "$cert_domain" $ACME_COMMON_ARGS >/dev/null 2>&1 || true
}

acme_cert_is_issued() {
    cert_domain="$1"
    "$ACME_SCRIPT" --info -d "$cert_domain" --ecc $ACME_COMMON_ARGS 2>/dev/null | grep -q "RealFullChainPath"
}

issue_cert_with_acme() {
    cert_domain="$1"
    cert_domain_clean=$(normalize_cert_domain "$cert_domain")

    if acme_cert_is_issued "$cert_domain_clean"; then
        entrypoint_log "Certificate already issued for $cert_domain_clean"
        return 0
    fi

    cleanup_stale_acme_record "$cert_domain_clean"

    if [ -n "$ACME_DNS_PROVIDER" ] && ! is_ip_address "$cert_domain_clean"; then
        entrypoint_log "Issuing via DNS: $ACME_DNS_PROVIDER for $cert_domain_clean"
        "$ACME_SCRIPT" --issue $ACME_COMMON_ARGS --server "$ACME_CA" --dns "dns_$ACME_DNS_PROVIDER" -d "$cert_domain_clean" --keylength ec-256
    else
        fail_standalone_if_nginx_running "$cert_domain_clean"
        entrypoint_log "Issuing via Standalone for $cert_domain_clean"
        issue_args="$ACME_COMMON_ARGS --server $ACME_CA --standalone -d $cert_domain_clean --keylength ec-256"
        if is_ip_address "$cert_domain_clean"; then
            issue_args="$issue_args --certificate-profile shortlived --days 6"
        fi
        if printf '%s' "$cert_domain_clean" | grep -q ':'; then
            issue_args="$issue_args --listen-v6"
        fi
        "$ACME_SCRIPT" --issue $issue_args
    fi
}

install_cert_files() {
    cert_domain="$1"
    cert_domain_clean=$(normalize_cert_domain "$cert_domain")
    cert_target_dir="$DIRECT_CERT_DIR/$cert_domain_clean"
    mkdir -p "$cert_target_dir"
    "$ACME_SCRIPT" --install-cert -d "$cert_domain_clean" --ecc $ACME_COMMON_ARGS \
        --fullchain-file "$cert_target_dir/cert" \
        --key-file "$cert_target_dir/key" \
        --reloadcmd "sh -c '$NGINX_BIN -t && { [ -s /var/run/nginx.pid ] && $NGINX_BIN -s reload || true; }'"
}

ensure_certificates_for_rules() {
    cert_domains_file="$1"
    if [ ! -s "$cert_domains_file" ] || [ "$(normalize_deploy_mode)" != "direct" ]; then return 0; fi

    ensure_acme_script


    while IFS= read -r cert_domain || [ -n "$cert_domain" ]; do
        cert_domain=$(trim_text "$cert_domain")
        [ -z "$cert_domain" ] && continue
        issue_cert_with_acme "$cert_domain"
        install_cert_files "$cert_domain"
    done < "$cert_domains_file"

}

cleanup_unused_certificates() {
    is_true "$DIRECT_CERT_CLEANUP" || return 0
    active_certs_file="$1"

    [ -f "$DIRECT_CERT_STATE_FILE" ] || {
        mkdir -p "$(dirname "$DIRECT_CERT_STATE_FILE")"
        cp "$active_certs_file" "$DIRECT_CERT_STATE_FILE"
        return 0
    }

    while IFS= read -r prev_domain || [ -n "$prev_domain" ]; do
        [ -z "$prev_domain" ] && continue
        if ! grep -Fxq "$prev_domain" "$active_certs_file"; then
            entrypoint_log "Removing stale certificate for $prev_domain"
            rm -rf "$DIRECT_CERT_DIR/$prev_domain"
            cleanup_stale_acme_record "$prev_domain"
        fi
    done < "$DIRECT_CERT_STATE_FILE"

    cp "$active_certs_file" "$DIRECT_CERT_STATE_FILE"
}

collect_rules() {
    output_file="$1"
    i=1
    while true; do
        rule_val=$(eval "printf '%s' \"\${PROXY_RULE_${i}:-}\"")
        [ -z "$rule_val" ] && break
        printf '%s\n' "$rule_val" >> "$output_file"
        i=$((i + 1))
    done
    if [ -f "$RULES_FILE" ]; then
        grep -v '^\s*#' "$RULES_FILE" | grep -v '^\s*$' >> "$output_file" || true
    fi
    if [ -s "$output_file" ]; then
        awk '!seen[$0]++' "$output_file" > "${output_file}.tmp" && mv "${output_file}.tmp" "$output_file"
    fi
}

# --- Main Flow ---
deploy_mode=$(normalize_deploy_mode)
mkdir -p "$DYNAMIC_DIR" "$DIRECT_CERT_DIR"
rm -f "$DYNAMIC_DIR"/*.conf

tmp_rules=$(mktemp)
tmp_certs=$(mktemp)
collect_rules "$tmp_rules"

if [ -s "$tmp_rules" ]; then
    while IFS=, read -r frontend_url backend_url || [ -n "$frontend_url" ]; do
        [ -z "$backend_url" ] && continue
        parsed=$(parse_frontend_url "$frontend_url" || continue)

        proto=$(echo "$parsed" | cut -d'|' -f1)
        domain=$(echo "$parsed" | cut -d'|' -f2)
        port=$(echo "$parsed" | cut -d'|' -f3)
        path=$(echo "$parsed" | cut -d'|' -f4)

        [ "$deploy_mode" = "front_proxy" ] && port="$FRONT_PROXY_PORT"

        conf_name="$(sanitize_domain "$domain").${port}.conf"
        srv_name=$(format_server_name "$domain")
        cert_dom=$(normalize_cert_domain "$domain")

        template="$TEMPLATE_FILE"
        if [ "$deploy_mode" = "direct" ]; then
            template=$([ "$proto" = "https" ] && echo "$DIRECT_TLS_TEMPLATE_FILE" || echo "$DIRECT_NO_TLS_TEMPLATE_FILE")
            [ "$proto" = "https" ] && echo "$cert_dom" >> "$tmp_certs"
        fi

        sed -e "s|\${frontend_port}|$port|g" \
            -e "s|\${domain_name}|$srv_name|g" \
            -e "s|\${resolver}|$RESOLVER|g" \
            -e "s|\${domain_path}|$path|g" \
            -e "s|\${proxy_target}|$backend_url|g" \
            -e "s|\${cert_dir}|$DIRECT_CERT_DIR|g" \
            -e "s|\${cert_domain}|$cert_dom|g" \
            "$template" > "$DYNAMIC_DIR/$conf_name"
        entrypoint_log "Generated config for $domain"
    done < "$tmp_rules"
fi

if [ "$deploy_mode" = "direct" ]; then
    if [ -s "$tmp_certs" ]; then
        awk '!seen[$0]++' "$tmp_certs" > "${tmp_certs}.dedup"
        ensure_certificates_for_rules "${tmp_certs}.dedup"
        cleanup_unused_certificates "${tmp_certs}.dedup"
        rm -f "${tmp_certs}.dedup"
    fi
fi

rm -f "$tmp_rules" "$tmp_certs"
exit 0

