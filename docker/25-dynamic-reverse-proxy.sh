#!/bin/sh
set -eu

TEMPLATE_FILE="/etc/nginx/templates/default.conf"
DIRECT_NO_TLS_TEMPLATE_FILE="/etc/nginx/templates/default.direct.no_tls.conf"
DIRECT_TLS_TEMPLATE_FILE="/etc/nginx/templates/default.direct.tls.conf"
DYNAMIC_DIR="/etc/nginx/conf.d/dynamic"
RULES_FILE="${PANEL_RULES_FILE:-/opt/nginx-reverse-emby/panel/data/proxy_rules.csv}"
RESOLVER="${NGINX_LOCAL_RESOLVERS:-1.1.1.1}"
PROXY_DEPLOY_MODE="${PROXY_DEPLOY_MODE:-front_proxy}"
FRONT_PROXY_PORT="${FRONT_PROXY_PORT:-3000}"
DIRECT_CERT_MODE="${DIRECT_CERT_MODE:-acme}"
DIRECT_CERT_DIR="${DIRECT_CERT_DIR:-/etc/nginx/certs}"
DIRECT_CERT_CLEANUP="${DIRECT_CERT_CLEANUP:-1}"
DIRECT_CERT_STATE_FILE="${DIRECT_CERT_STATE_FILE:-/opt/nginx-reverse-emby/panel/data/.active_cert_domains}"
ACME_HOME="${ACME_HOME:-/opt/acme.sh}"
ACME_SCRIPT="${ACME_SCRIPT:-$ACME_HOME/acme.sh}"
ACME_INSTALL_URL="${ACME_INSTALL_URL:-https://raw.githubusercontent.com/acmesh-official/acme.sh/master/acme.sh}"
ACME_CA="${ACME_CA:-letsencrypt}"
ACME_DNS_PROVIDER="${ACME_DNS_PROVIDER:-}"
ACME_EMAIL="${ACME_EMAIL:-}"
ACME_STANDALONE_STOP_NGINX="${ACME_STANDALONE_STOP_NGINX:-1}"
NGINX_BIN="${NGINX_BIN:-nginx}"

entrypoint_log() {
    if [ -z "${NGINX_ENTRYPOINT_QUIET_LOGS:-}" ]; then
        echo "$@"
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

    if printf '%s' "$value" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$'; then
        return 0
    fi

    if printf '%s' "$value" | grep -Eq '^[0-9A-Fa-f:.]+$' && printf '%s' "$value" | grep -q ':'; then
        return 0
    fi

    return 1
}

normalize_deploy_mode() {
    mode_raw=$(printf '%s' "$PROXY_DEPLOY_MODE" | tr '[:upper:]' '[:lower:]' | tr '-' '_')
    case "$mode_raw" in
        front_proxy|front|upstream|proxy) printf 'front_proxy' ;;
        direct|direct_tls|self_managed|host) printf 'direct' ;;
        *)
            entrypoint_log "$0: warning: unknown PROXY_DEPLOY_MODE='$PROXY_DEPLOY_MODE', fallback to 'front_proxy'"
            printf 'front_proxy'
            ;;
    esac
}

normalize_front_proxy_port() {
    case "$FRONT_PROXY_PORT" in
        ''|*[!0-9]*)
            entrypoint_log "$0: warning: invalid FRONT_PROXY_PORT='$FRONT_PROXY_PORT', fallback to '3000'"
            printf '3000'
            ;;
        *)
            printf '%s' "$FRONT_PROXY_PORT"
            ;;
    esac
}

ensure_acme_script() {
    if [ -x "$ACME_SCRIPT" ]; then
        return 0
    fi

    if ! command -v curl >/dev/null 2>&1; then
        entrypoint_log "$0: error: curl is required for direct TLS certificate issuance"
        return 1
    fi

    mkdir -p "$ACME_HOME"

    tmp_acme_install=$(mktemp)
    if ! curl -fsSL "$ACME_INSTALL_URL" -o "$tmp_acme_install"; then
        rm -f "$tmp_acme_install"
        entrypoint_log "$0: error: failed to download acme.sh from '$ACME_INSTALL_URL'"
        return 1
    fi

    if [ -n "$ACME_EMAIL" ]; then
        sh "$tmp_acme_install" --install --home "$ACME_HOME" --config-home "$ACME_HOME" --cert-home "$ACME_HOME" --accountemail "$ACME_EMAIL"
    else
        sh "$tmp_acme_install" --install --home "$ACME_HOME" --config-home "$ACME_HOME" --cert-home "$ACME_HOME"
    fi
    rm -f "$tmp_acme_install"

    if [ ! -x "$ACME_SCRIPT" ]; then
        entrypoint_log "$0: error: acme.sh installation succeeded but '$ACME_SCRIPT' was not found"
        return 1
    fi

    "$ACME_SCRIPT" --set-default-ca --server "$ACME_CA" >/dev/null 2>&1 || true
    return 0
}

acme_cert_is_issued() {
    cert_domain="$1"
    acme_info=$("$ACME_SCRIPT" --info -d "$cert_domain" --ecc 2>/dev/null || true)
    printf '%s' "$acme_info" | grep -q "RealFullChainPath"
}

cleanup_stale_acme_record() {
    cert_domain="$1"
    if [ -z "$cert_domain" ] || [ ! -x "$ACME_SCRIPT" ]; then
        return 0
    fi

    "$ACME_SCRIPT" --remove -d "$cert_domain" --ecc >/dev/null 2>&1 || true
    "$ACME_SCRIPT" --remove -d "$cert_domain" >/dev/null 2>&1 || true
    return 0
}

certificate_uses_standalone_challenge() {
    cert_domain="$1"
    cert_domain_clean=$(normalize_cert_domain "$cert_domain")

    if [ -z "$ACME_DNS_PROVIDER" ]; then
        return 0
    fi

    if is_ip_address "$cert_domain_clean"; then
        return 0
    fi

    return 1
}

issue_certificate_dns() {
    cert_domain="$1"
    cert_domain_clean=$(normalize_cert_domain "$cert_domain")
    dns_provider_norm="$2"
    dns_arg="dns_${dns_provider_norm}"

    entrypoint_log "$0: issuing certificate via DNS challenge for '$cert_domain_clean' (provider='$dns_provider_norm')"
    if "$ACME_SCRIPT" --issue --dns "$dns_arg" -d "$cert_domain_clean" --keylength ec-256; then
        return 0
    fi

    cleanup_stale_acme_record "$cert_domain_clean"
    if ! "$ACME_SCRIPT" --issue --dns "$dns_arg" -d "$cert_domain_clean" --keylength ec-256; then
        return 1
    fi

    return 0
}

issue_certificate_standalone() {
    cert_domain="$1"
    cert_domain_clean=$(normalize_cert_domain "$cert_domain")
    issue_extra=""
    listen_extra=""

    if printf '%s' "$cert_domain_clean" | grep -q '^\*\.'; then
        entrypoint_log "$0: error: wildcard certificate '$cert_domain_clean' requires DNS challenge"
        return 1
    fi

    if is_ip_address "$cert_domain_clean"; then
        issue_extra="--certificate-profile shortlived --days 6"
    fi
    if printf '%s' "$cert_domain_clean" | grep -q ':'; then
        listen_extra="--listen-v6"
    fi

    if ! "$ACME_SCRIPT" --issue --standalone -d "$cert_domain_clean" --keylength ec-256 $issue_extra $listen_extra; then
        cleanup_stale_acme_record "$cert_domain_clean"
        if ! "$ACME_SCRIPT" --issue --standalone -d "$cert_domain_clean" --keylength ec-256 $issue_extra $listen_extra; then
            return 1
        fi
    fi

    return 0
}

issue_cert_with_acme() {
    cert_domain="$1"
    cert_domain_clean=$(normalize_cert_domain "$cert_domain")
    acme_dns_provider="$ACME_DNS_PROVIDER"

    if is_ip_address "$cert_domain_clean" && [ -n "$acme_dns_provider" ]; then
        entrypoint_log "$0: warning: DNS validation is skipped for IP cert '$cert_domain_clean'; fallback to standalone"
        acme_dns_provider=""
    fi

    if ! acme_cert_is_issued "$cert_domain_clean"; then
        cleanup_stale_acme_record "$cert_domain_clean"
        if [ -n "$acme_dns_provider" ]; then
            issue_certificate_dns "$cert_domain_clean" "$acme_dns_provider" || return 1
        else
            issue_certificate_standalone "$cert_domain_clean" || return 1
        fi
    else
        entrypoint_log "$0: ACME certificate record already exists for '$cert_domain_clean', skip issue step"
    fi

    return 0
}

install_cert_files() {
    cert_domain="$1"
    cert_domain_clean=$(normalize_cert_domain "$cert_domain")
    cert_target_dir="$DIRECT_CERT_DIR/$cert_domain_clean"
    reload_cmd="sh -c '$NGINX_BIN -t >/dev/null 2>&1 && $NGINX_BIN -s reload >/dev/null 2>&1 || true'"
    mkdir -p "$cert_target_dir"

    "$ACME_SCRIPT" --install-cert -d "$cert_domain_clean" --ecc \
        --fullchain-file "$cert_target_dir/cert" \
        --key-file "$cert_target_dir/key" \
        --reloadcmd "$reload_cmd"
}

ensure_domain_certificate() {
    cert_domain="$1"
    cert_domain_clean=$(normalize_cert_domain "$cert_domain")
    cert_target_dir="$DIRECT_CERT_DIR/$cert_domain_clean"

    case "$(printf '%s' "$DIRECT_CERT_MODE" | tr '[:upper:]' '[:lower:]')" in
        acme)
            issue_cert_with_acme "$cert_domain_clean" || return 1
            install_cert_files "$cert_domain_clean" || return 1
            ;;
        manual)
            if [ -s "$cert_target_dir/cert" ] && [ -s "$cert_target_dir/key" ]; then
                return 0
            fi
            entrypoint_log "$0: error: missing cert/key in '$cert_target_dir' while DIRECT_CERT_MODE=manual"
            return 1
            ;;
        *)
            entrypoint_log "$0: error: unsupported DIRECT_CERT_MODE='$DIRECT_CERT_MODE'"
            return 1
            ;;
    esac
    return 0
}

cleanup_unused_certificates() {
    active_cert_domains_file="$1"

    if ! is_true "$DIRECT_CERT_CLEANUP"; then
        return 0
    fi

    if [ ! -d "$DIRECT_CERT_DIR" ]; then
        return 0
    fi

    cert_mode_norm=$(printf '%s' "$DIRECT_CERT_MODE" | tr '[:upper:]' '[:lower:]')
    tmp_active=$(mktemp)
    tmp_previous=$(mktemp)

    if [ -s "$active_cert_domains_file" ]; then
        awk 'NF{print}' "$active_cert_domains_file" | awk '!seen[$0]++' > "$tmp_active"
    fi

    if [ -f "$DIRECT_CERT_STATE_FILE" ]; then
        awk 'NF{print}' "$DIRECT_CERT_STATE_FILE" | awk '!seen[$0]++' > "$tmp_previous"
    fi

    if [ -s "$tmp_previous" ]; then
        while IFS= read -r cert_domain || [ -n "$cert_domain" ]; do
            if [ -z "$cert_domain" ]; then
                continue
            fi

            if [ -s "$tmp_active" ] && grep -Fxq "$cert_domain" "$tmp_active"; then
                continue
            fi

            cert_path="$DIRECT_CERT_DIR/$cert_domain"
            if [ -d "$cert_path" ]; then
                rm -rf "$cert_path"
                entrypoint_log "$0: removed stale cert directory '$cert_path'"
            fi

            if [ "$cert_mode_norm" = "acme" ] && [ -x "$ACME_SCRIPT" ]; then
                "$ACME_SCRIPT" --remove -d "$cert_domain" --ecc >/dev/null 2>&1 || true
                "$ACME_SCRIPT" --remove -d "$cert_domain" >/dev/null 2>&1 || true
                entrypoint_log "$0: removed stale acme record for '$cert_domain'"
            fi
        done < "$tmp_previous"
    fi

    mkdir -p "$(dirname "$DIRECT_CERT_STATE_FILE")"
    if [ -s "$tmp_active" ]; then
        cp "$tmp_active" "$DIRECT_CERT_STATE_FILE"
    else
        : > "$DIRECT_CERT_STATE_FILE"
    fi

    rm -f "$tmp_active" "$tmp_previous"
    return 0
}

ensure_certificates_for_rules() {
    cert_domains_file="$1"

    if [ ! -s "$cert_domains_file" ]; then
        return 0
    fi

    cert_mode_norm=$(printf '%s' "$DIRECT_CERT_MODE" | tr '[:upper:]' '[:lower:]')
    if [ "$cert_mode_norm" = "acme" ]; then
        ensure_acme_script || return 1
    fi

    nginx_was_running=0
    require_standalone=0
    if [ "$cert_mode_norm" = "acme" ] && is_true "$ACME_STANDALONE_STOP_NGINX"; then
        while IFS= read -r cert_domain || [ -n "$cert_domain" ]; do
            cert_domain=$(trim_text "$cert_domain")
            if [ -z "$cert_domain" ]; then
                continue
            fi
            if certificate_uses_standalone_challenge "$cert_domain"; then
                require_standalone=1
                break
            fi
        done < "$cert_domains_file"
    fi

    if [ "$require_standalone" -eq 1 ]; then
        nginx_running=0
        if command -v pgrep >/dev/null 2>&1 && pgrep -x nginx >/dev/null 2>&1; then
            nginx_running=1
        elif command -v pidof >/dev/null 2>&1 && pidof nginx >/dev/null 2>&1; then
            nginx_running=1
        fi

        if [ "$nginx_running" -eq 1 ]; then
            nginx_was_running=1
            entrypoint_log "$0: stopping nginx for standalone ACME challenge"
            "$NGINX_BIN" -s stop >/dev/null 2>&1 || true
        fi
    fi

    cert_error=0
    while IFS= read -r cert_domain || [ -n "$cert_domain" ]; do
        cert_domain=$(trim_text "$cert_domain")
        if [ -z "$cert_domain" ]; then
            continue
        fi
        if ! ensure_domain_certificate "$cert_domain"; then
            cert_error=1
            break
        fi
    done < "$cert_domains_file"

    if [ "$nginx_was_running" -eq 1 ]; then
        entrypoint_log "$0: starting nginx after standalone ACME challenge"
        "$NGINX_BIN" >/dev/null 2>&1 || true
    fi

    if [ "$cert_error" -ne 0 ]; then
        return 1
    fi

    return 0
}

collect_rules() {
    output_file="$1"

    i=1
    while true; do
        rule_var="PROXY_RULE_${i}"
        rule_val=$(eval "printf '%s' \"\${${rule_var}:-}\"")
        if [ -z "$rule_val" ]; then
            break
        fi
        printf '%s\n' "$rule_val" >> "$output_file"
        i=$((i + 1))
    done

    if [ -f "$RULES_FILE" ]; then
        while IFS= read -r line || [ -n "$line" ]; do
            line=$(trim_text "$line")
            if [ -z "$line" ]; then
                continue
            fi
            case "$line" in
                \#*) continue ;;
            esac
            printf '%s\n' "$line" >> "$output_file"
        done < "$RULES_FILE"
    fi

    if [ -s "$output_file" ]; then
        awk '!seen[$0]++' "$output_file" > "${output_file}.dedup"
        mv "${output_file}.dedup" "$output_file"
    fi
}

if [ ! -f "$TEMPLATE_FILE" ]; then
    entrypoint_log "$0: error: template file '$TEMPLATE_FILE' was not found"
    exit 1
fi

deploy_mode=$(normalize_deploy_mode)
front_proxy_port=$(normalize_front_proxy_port)

if [ "$deploy_mode" = "direct" ]; then
    if [ ! -f "$DIRECT_NO_TLS_TEMPLATE_FILE" ]; then
        entrypoint_log "$0: error: direct no-tls template '$DIRECT_NO_TLS_TEMPLATE_FILE' was not found"
        exit 1
    fi
    if [ ! -f "$DIRECT_TLS_TEMPLATE_FILE" ]; then
        entrypoint_log "$0: error: direct tls template '$DIRECT_TLS_TEMPLATE_FILE' was not found"
        exit 1
    fi
    mkdir -p "$DIRECT_CERT_DIR"
fi

mkdir -p "$DYNAMIC_DIR"
rm -f "$DYNAMIC_DIR"/*.conf

tmp_rules=$(mktemp)
tmp_parsed=$(mktemp)
tmp_certs=$(mktemp)
trap 'rm -f "$tmp_rules" "${tmp_rules}.dedup" "$tmp_parsed" "$tmp_certs" "${tmp_certs}.dedup"' EXIT
collect_rules "$tmp_rules"

if [ -s "$tmp_rules" ]; then
    while IFS= read -r raw_rule || [ -n "$raw_rule" ]; do
        frontend_raw=${raw_rule%%,*}
        backend_raw=${raw_rule#*,}

        if [ "$frontend_raw" = "$raw_rule" ]; then
            entrypoint_log "$0: warning: skip invalid rule '$raw_rule' (missing comma separator)"
            continue
        fi

        frontend_url=$(trim_text "$frontend_raw")
        backend_url=$(trim_text "$backend_raw")

        if [ -z "$frontend_url" ] || [ -z "$backend_url" ]; then
            entrypoint_log "$0: warning: skip invalid rule '$raw_rule' (empty frontend/backend)"
            continue
        fi

        parsed=$(parse_frontend_url "$frontend_url" 2>/dev/null || true)
        if [ -z "$parsed" ]; then
            entrypoint_log "$0: warning: skip invalid frontend url '$frontend_url'"
            continue
        fi

        frontend_proto=$(printf '%s' "$parsed" | cut -d'|' -f1)
        domain_name=$(printf '%s' "$parsed" | cut -d'|' -f2)
        frontend_port=$(printf '%s' "$parsed" | cut -d'|' -f3)
        domain_path=$(printf '%s' "$parsed" | cut -d'|' -f4)

        if [ -z "$domain_path" ]; then
            domain_path="/"
        fi

        if [ "$deploy_mode" = "front_proxy" ]; then
            frontend_port="$front_proxy_port"
        fi

        config_filename="$(sanitize_domain "$domain_name").${frontend_port}.conf"
        server_name=$(format_server_name "$domain_name")
        cert_domain=$(normalize_cert_domain "$domain_name")

        printf '%s|%s|%s|%s|%s|%s|%s|%s\n' \
            "$frontend_proto" "$domain_name" "$frontend_port" "$domain_path" \
            "$backend_url" "$server_name" "$cert_domain" "$config_filename" >> "$tmp_parsed"

        if [ "$deploy_mode" = "direct" ] && [ "$frontend_proto" = "https" ]; then
            printf '%s\n' "$cert_domain" >> "$tmp_certs"
        fi
    done < "$tmp_rules"
fi

if [ "$deploy_mode" = "direct" ] && [ -s "$tmp_certs" ]; then
    awk '!seen[$0]++' "$tmp_certs" > "${tmp_certs}.dedup"
    mv "${tmp_certs}.dedup" "$tmp_certs"
    if ! ensure_certificates_for_rules "$tmp_certs"; then
        entrypoint_log "$0: error: failed to prepare certificates for direct mode"
        exit 1
    fi
fi

if [ "$deploy_mode" = "direct" ]; then
    cleanup_unused_certificates "$tmp_certs"
fi

config_count=0
if [ -s "$tmp_parsed" ]; then
    while IFS='|' read -r frontend_proto domain_name frontend_port domain_path backend_url server_name cert_domain config_filename || [ -n "$frontend_proto" ]; do
        if [ -z "$frontend_proto" ] || [ -z "$domain_name" ] || [ -z "$frontend_port" ] || [ -z "$domain_path" ] || [ -z "$backend_url" ] || [ -z "$server_name" ] || [ -z "$cert_domain" ] || [ -z "$config_filename" ]; then
            continue
        fi

        template_content=$(cat "$TEMPLATE_FILE")
        if [ "$deploy_mode" = "direct" ]; then
            if [ "$frontend_proto" = "https" ]; then
                template_content=$(cat "$DIRECT_TLS_TEMPLATE_FILE")
            else
                template_content=$(cat "$DIRECT_NO_TLS_TEMPLATE_FILE")
            fi
        fi

        generated_block=$(echo "$template_content" | \
            sed "s|\${frontend_port}|$(escape_sed_replacement "$frontend_port")|g" | \
            sed "s|\${domain_name}|$(escape_sed_replacement "$server_name")|g" | \
            sed "s|\${resolver}|$(escape_sed_replacement "$RESOLVER")|g" | \
            sed "s|\${domain_path}|$(escape_sed_replacement "$domain_path")|g" | \
            sed "s|\${proxy_target}|$(escape_sed_replacement "$backend_url")|g" | \
            sed "s|\${cert_dir}|$(escape_sed_replacement "$DIRECT_CERT_DIR")|g" | \
            sed "s|\${cert_domain}|$(escape_sed_replacement "$cert_domain")|g")

        printf '%s\n' "$generated_block" > "$DYNAMIC_DIR/$config_filename"
        config_count=$((config_count + 1))
        entrypoint_log "$0: generated $DYNAMIC_DIR/$config_filename"
    done < "$tmp_parsed"
fi

if [ "$config_count" -eq 0 ]; then
    entrypoint_log "$0: no valid proxy rules were found"
else
    entrypoint_log "$0: generated $config_count nginx config file(s) in mode '$deploy_mode'"
fi

exit 0
