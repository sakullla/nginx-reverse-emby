#!/bin/sh
set -eu

# --- 配置定义 ---
TEMPLATE_FILE="${NRE_TEMPLATE_FILE:-/etc/nginx/templates/default.conf}"
DIRECT_NO_TLS_TEMPLATE_FILE="${NRE_DIRECT_NO_TLS_TEMPLATE_FILE:-/etc/nginx/templates/default.direct.no_tls.conf}"
DIRECT_TLS_TEMPLATE_FILE="${NRE_DIRECT_TLS_TEMPLATE_FILE:-/etc/nginx/templates/default.direct.tls.conf}"
DYNAMIC_DIR="${NRE_DYNAMIC_DIR:-/etc/nginx/conf.d/dynamic}"

# Data root
DATA_ROOT="/opt/nginx-reverse-emby/panel/data"
RULES_FILE="${PANEL_RULES_FILE:-$DATA_ROOT/proxy_rules.csv}"
L4_RULES_JSON="${PANEL_L4_RULES_JSON:-$DATA_ROOT/l4_rules.json}"
if [ -n "${PANEL_MANAGED_CERTS_SYNC_JSON:-}" ]; then
    MANAGED_CERTS_SYNC_JSON="$PANEL_MANAGED_CERTS_SYNC_JSON"
elif [ -f "$DATA_ROOT/managed_cert_bundle.local.json" ]; then
    MANAGED_CERTS_SYNC_JSON="$DATA_ROOT/managed_cert_bundle.local.json"
else
    MANAGED_CERTS_SYNC_JSON="$DATA_ROOT/managed_cert_bundle.json"
fi
if [ -n "${PANEL_MANAGED_CERTS_POLICY_JSON:-}" ]; then
    MANAGED_CERTS_POLICY_JSON="$PANEL_MANAGED_CERTS_POLICY_JSON"
elif [ -f "$DATA_ROOT/managed_cert_policy.local.json" ]; then
    MANAGED_CERTS_POLICY_JSON="$DATA_ROOT/managed_cert_policy.local.json"
else
    MANAGED_CERTS_POLICY_JSON="$DATA_ROOT/managed_cert_policy.json"
fi
ACME_HOME="${ACME_HOME:-$DATA_ROOT/.acme.sh}"
DIRECT_CERT_DIR="${DIRECT_CERT_DIR:-$DATA_ROOT/certs}"
DIRECT_CERT_STATE_FILE="${DIRECT_CERT_STATE_FILE:-$DATA_ROOT/.state/active_cert_domains}"
STREAM_DYNAMIC_DIR="${NRE_STREAM_DYNAMIC_DIR:-/etc/nginx/stream-conf.d/dynamic}"

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

format_network_host() {
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

port_is_listening() {
    port_hex=$(printf '%04X' "$1")
    for proc_file in /proc/net/tcp /proc/net/tcp6; do
        [ -r "$proc_file" ] || continue
        if awk -v port_hex="$port_hex" '
            $4 == "0A" {
                split($2, local_addr, ":")
                if (local_addr[2] == port_hex) {
                    found = 1
                }
            }
            END { exit(found ? 0 : 1) }
        ' "$proc_file"; then
            return 0
        fi
    done
    return 1
}


fail_standalone_if_port_80_in_use() {
    cert_domain="$1"
    if port_is_listening 80; then
        echo "[PROXY] error: cannot issue certificate for $cert_domain via standalone while port 80 is already in use. Configure ACME_DNS_PROVIDER, free port 80, pre-create the rule before container startup, or disable PANEL_AUTO_APPLY and restart after saving the rule." >&2
        return 1
    fi
}

nginx_pid_is_running() {
    if [ -s /var/run/nginx.pid ]; then
        nginx_pid=$(cat /var/run/nginx.pid 2>/dev/null || true)
        [ -n "$nginx_pid" ] && kill -0 "$nginx_pid" 2>/dev/null
        return $?
    fi
    return 1
}

wait_for_port_release() {
    port="$1"
    retries="${2:-50}"
    while [ "$retries" -gt 0 ]; do
        if ! port_is_listening "$port"; then
            return 0
        fi
        sleep 0.2
        retries=$((retries - 1))
    done
    return 1
}

stop_nginx_for_standalone_acme() {
    cert_domain="$1"
    ACME_STOPPED_NGINX="0"
    if ! port_is_listening 80; then
        return 0
    fi

    if is_true "$ACME_STANDALONE_STOP_NGINX" && nginx_pid_is_running; then
        entrypoint_log "Temporarily stopping nginx to issue standalone certificate for $cert_domain"
        "$NGINX_BIN" -s quit >/dev/null 2>&1 || "$NGINX_BIN" -s stop >/dev/null 2>&1 || true
        if ! wait_for_port_release 80; then
            echo "[PROXY] error: failed to stop nginx before standalone issuance for $cert_domain" >&2
            return 1
        fi
        ACME_STOPPED_NGINX="1"
        return 0
    fi

    fail_standalone_if_port_80_in_use "$cert_domain"
}

restore_nginx_after_standalone_acme() {
    if [ "${ACME_STOPPED_NGINX:-0}" = "1" ]; then
        entrypoint_log "Starting nginx after standalone certificate issuance"
        "$NGINX_BIN" >/dev/null 2>&1 || true
        ACME_STOPPED_NGINX="0"
    fi
}

acme_dns_hook_path() {
    [ -n "$ACME_DNS_PROVIDER" ] || return 1
    printf '%s/dnsapi/dns_%s.sh' "$ACME_HOME" "$ACME_DNS_PROVIDER"
}

install_acme_script() {
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
        sh "$tmp_acme_install" --install-online --nocron $ACME_COMMON_ARGS ${ACME_EMAIL:+--accountemail "$ACME_EMAIL"}
    ); then
        rm -rf "$tmp_acme_dir"
        return 1
    fi
    rm -rf "$tmp_acme_dir"
    "$ACME_SCRIPT" --set-default-ca --server "$ACME_CA" $ACME_COMMON_ARGS >/dev/null 2>&1 || true
    return 0
}

ensure_acme_script() {
    if [ -x "$ACME_SCRIPT" ]; then
        if [ -n "$ACME_DNS_PROVIDER" ]; then
            dns_hook=$(acme_dns_hook_path || true)
            if [ -n "$dns_hook" ] && [ ! -f "$dns_hook" ]; then
                entrypoint_log "Existing acme.sh install is missing dns_$ACME_DNS_PROVIDER hook, reinstalling..."
                install_acme_script
                return 0
            fi
        fi
        return 0
    fi
    install_acme_script
}

cleanup_stale_acme_record() {
    cert_domain="$1"
    if [ ! -x "$ACME_SCRIPT" ]; then return 0; fi
    entrypoint_log "Cleaning up stale acme record for $cert_domain..."
    "$ACME_SCRIPT" --remove -d "$cert_domain" --ecc $ACME_COMMON_ARGS >/dev/null 2>&1 || true
    "$ACME_SCRIPT" --remove -d "$cert_domain" $ACME_COMMON_ARGS >/dev/null 2>&1 || true
    rm -rf "$ACME_HOME/$cert_domain" "$ACME_HOME/${cert_domain}_ecc"
}

acme_cert_is_issued() {
    cert_domain="$1"
    "$ACME_SCRIPT" --info -d "$cert_domain" --ecc $ACME_COMMON_ARGS 2>/dev/null | grep -q "RealFullChainPath"
}

issue_cert_with_acme() {
    cert_domain="$1"
    issue_mode="${2:-auto}"
    cert_domain_clean=$(normalize_cert_domain "$cert_domain")

    if acme_cert_is_issued "$cert_domain_clean"; then
        entrypoint_log "Certificate already issued for $cert_domain_clean"
        return 0
    fi

    cleanup_stale_acme_record "$cert_domain_clean"

    if [ "$issue_mode" != "local_http01" ] && [ -n "$ACME_DNS_PROVIDER" ] && ! is_ip_address "$cert_domain_clean"; then
        entrypoint_log "Issuing via DNS: $ACME_DNS_PROVIDER for $cert_domain_clean"
        if "$ACME_SCRIPT" --issue $ACME_COMMON_ARGS --server "$ACME_CA" --dns "dns_$ACME_DNS_PROVIDER" -d "$cert_domain_clean" --keylength ec-256; then
            return 0
        fi
        entrypoint_log "Initial DNS issuance failed for $cert_domain_clean, retrying with --force after cleanup..."
        cleanup_stale_acme_record "$cert_domain_clean"
        "$ACME_SCRIPT" --issue --force $ACME_COMMON_ARGS --server "$ACME_CA" --dns "dns_$ACME_DNS_PROVIDER" -d "$cert_domain_clean" --keylength ec-256
    else
        stop_nginx_for_standalone_acme "$cert_domain_clean"
        entrypoint_log "Issuing via Standalone for $cert_domain_clean"
        issue_args="$ACME_COMMON_ARGS --server $ACME_CA --standalone -d $cert_domain_clean --keylength ec-256"
        if is_ip_address "$cert_domain_clean"; then
            issue_args="$issue_args --certificate-profile shortlived --days 6"
        fi
        if printf '%s' "$cert_domain_clean" | grep -q ':'; then
            issue_args="$issue_args --listen-v6"
        fi
        if "$ACME_SCRIPT" --issue $issue_args; then
            restore_nginx_after_standalone_acme
            return 0
        fi
        entrypoint_log "Initial standalone issuance failed for $cert_domain_clean, retrying with --force after cleanup..."
        cleanup_stale_acme_record "$cert_domain_clean"
        if "$ACME_SCRIPT" --issue --force $issue_args; then
            restore_nginx_after_standalone_acme
            return 0
        fi
        restore_nginx_after_standalone_acme
        return 1
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
        --reloadcmd "sh -c '$NGINX_BIN -t >/dev/null 2>&1 && { [ -s /var/run/nginx.pid ] && $NGINX_BIN -s reload || true; }; true'"
}

ensure_certificates_for_rules() {
    cert_domains_file="$1"
    if [ ! -s "$cert_domains_file" ] || [ "$(normalize_deploy_mode)" != "direct" ]; then return 0; fi

    ensure_acme_script


    while IFS='|' read -r issue_mode cert_domain || [ -n "$issue_mode$cert_domain" ]; do
        issue_mode=$(trim_text "$issue_mode")
        cert_domain=$(trim_text "$cert_domain")
        [ -z "$cert_domain" ] && continue
        [ -n "$issue_mode" ] || issue_mode="auto"
        issue_cert_with_acme "$cert_domain" "$issue_mode"
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
    RULES_JSON="${PANEL_RULES_JSON:-$DATA_ROOT/proxy_rules.json}"

    # 首先处理环境变量中的规则 PROXY_RULE_1, PROXY_RULE_2...
    i=1
    while true; do
        rule_val=$(eval "printf '%s' \"\${PROXY_RULE_${i}:-}\"")
        [ -z "$rule_val" ] && break
        printf '%s\n' "$rule_val" >> "$output_file"
        i=$((i + 1))
    done

    # 从 JSON 文件中提取启用的规则并转换为 CSV 格式以便后续处理
    # 格式: frontend_url,backend_url,proxy_redirect
    if [ -f "$RULES_JSON" ]; then
        node -e "
            const fs = require('fs');
            try {
                const rules = JSON.parse(fs.readFileSync('$RULES_JSON', 'utf8'));
                rules.filter(r => r.enabled !== false).forEach(r => {
                    const proxyRedirect = r.proxy_redirect !== false ? '1' : '0';
                    process.stdout.write(r.frontend_url + ',' + r.backend_url + ',' + proxyRedirect + '\n');
                });
            } catch (e) {
                process.stderr.write('Error parsing rules.json: ' + e.message + '\n');
                process.exit(1);
            }
        " >> "$output_file" || true
    elif [ -f "$RULES_FILE" ]; then
        # 回退逻辑: 如果没有 JSON 但有旧的 CSV，默认启用 proxy_redirect
        grep -v '^\s*#' "$RULES_FILE" | grep -v '^\s*$' | while IFS= read -r line; do
            printf '%s,1\n' "$line"
        done >> "$output_file" || true
    fi

    if [ -s "$output_file" ]; then
        awk '!seen[$0]++' "$output_file" > "${output_file}.tmp" && mv "${output_file}.tmp" "$output_file"
    fi
}

collect_l4_rules() {
    output_file="$1"
    # Copy JSON content into the temp file so downstream parsing works with mktemp paths.
    if [ ! -f "$L4_RULES_JSON" ]; then
        return 0
    fi
    cat "$L4_RULES_JSON" > "$output_file"
}

install_synced_certificate() {
    cert_domain="$1"
    [ -f "$MANAGED_CERTS_SYNC_JSON" ] || return 1
    CERT_DOMAIN="$cert_domain" DIRECT_CERT_DIR="$DIRECT_CERT_DIR" MANAGED_CERTS_SYNC_JSON="$MANAGED_CERTS_SYNC_JSON" node -e "
        const fs = require('fs');
        const path = require('path');
        const domain = String(process.env.CERT_DOMAIN || '').trim().toLowerCase();
        const bundleFile = process.env.MANAGED_CERTS_SYNC_JSON;
        const certRoot = process.env.DIRECT_CERT_DIR;
        const bundle = JSON.parse(fs.readFileSync(bundleFile, 'utf8'));
        const isWildcard = (value) => /^\\*\\.[a-z0-9.-]+$/i.test(String(value || '').trim());
        const wildcardMatches = (pattern, host) => {
            const normalizedPattern = String(pattern || '').trim().toLowerCase();
            const normalizedHost = String(host || '').trim().toLowerCase();
            if (!isWildcard(normalizedPattern)) return false;
            const suffix = normalizedPattern.slice(2);
            if (!normalizedHost.endsWith('.' + suffix)) return false;
            return normalizedHost.split('.').length === suffix.split('.').length + 1;
        };
        const item = (Array.isArray(bundle) ? bundle : []).find((entry) => {
            const entryDomain = String(entry.domain || '').trim().toLowerCase();
            return entryDomain === domain || wildcardMatches(entryDomain, domain);
        });
        if (!item || !item.cert_pem || !item.key_pem) process.exit(1);
        const targetDir = path.join(certRoot, domain);
        fs.mkdirSync(targetDir, { recursive: true });
        fs.writeFileSync(path.join(targetDir, 'cert'), String(item.cert_pem), 'utf8');
        fs.writeFileSync(path.join(targetDir, 'key'), String(item.key_pem), 'utf8');
    " >/dev/null 2>&1
}

resolve_managed_certificate_mode() {
    cert_domain="$1"
    [ -f "$MANAGED_CERTS_POLICY_JSON" ] || { printf 'absent'; return 0; }
    CERT_DOMAIN="$cert_domain" MANAGED_CERTS_POLICY_JSON="$MANAGED_CERTS_POLICY_JSON" node -e "
        const fs = require('fs');
        const domain = String(process.env.CERT_DOMAIN || '').trim().toLowerCase();
        const policyFile = process.env.MANAGED_CERTS_POLICY_JSON;
        const isWildcard = (value) => /^\\*\\.[a-z0-9.-]+$/i.test(String(value || '').trim());
        const wildcardMatches = (pattern, host) => {
            const normalizedPattern = String(pattern || '').trim().toLowerCase();
            const normalizedHost = String(host || '').trim().toLowerCase();
            if (!isWildcard(normalizedPattern)) return false;
            const suffix = normalizedPattern.slice(2);
            if (!normalizedHost.endsWith('.' + suffix)) return false;
            return normalizedHost.split('.').length === suffix.split('.').length + 1;
        };
        let policy = [];
        try {
            policy = JSON.parse(fs.readFileSync(policyFile, 'utf8'));
        } catch {
            process.stdout.write('absent');
            process.exit(0);
        }
        const sorted = (Array.isArray(policy) ? policy : []).slice().sort((left, right) => {
            const leftWildcard = isWildcard(left && left.domain);
            const rightWildcard = isWildcard(right && right.domain);
            if (leftWildcard !== rightWildcard) return leftWildcard ? 1 : -1;
            return Number(right && right.revision || 0) - Number(left && left.revision || 0);
        });
        const item = sorted.find((entry) => {
            const entryDomain = String(entry.domain || '').trim().toLowerCase();
            return entryDomain === domain || wildcardMatches(entryDomain, domain);
        });
        if (!item) {
            process.stdout.write('absent');
            process.exit(0);
        }
        if (item.enabled === false) {
            process.stdout.write('disabled');
            process.exit(0);
        }
        const mode = String(item.issuer_mode || '').trim().toLowerCase();
        if (mode === 'master_cf_dns' || mode === 'local_http01') {
            process.stdout.write(mode);
            process.exit(0);
        }
        process.stdout.write('absent');
    "
}

generate_l4_configs() {
    rules_json_path="$1"
    mkdir -p "$STREAM_DYNAMIC_DIR"
    if [ ! -f "$rules_json_path" ] || [ ! -s "$rules_json_path" ]; then
        rm -f "$STREAM_DYNAMIC_DIR"/*.conf
        return 0
    fi

    RESOLVER_LINE="$RESOLVER"
    tmp_stream_dir=$(mktemp -d "$STREAM_DYNAMIC_DIR/.tmp.XXXXXX")

    if ! node -e "
        const fs = require('fs');
        const path = require('path');

        const rulesJsonPath = '$rules_json_path';
        const streamDynamicDir = '$tmp_stream_dir';
        const resolver = '$RESOLVER_LINE'.trim();

        function formatNetworkHost(host) {
            if (host.includes(':')) return '[' + host + ']';
            return host;
        }

        function sanitizeDomain(domain) {
            return domain.toLowerCase().replace(/[^a-z0-9._-]/g, '_');
        }

        function isIpAddress(value) {
            if (!value) return false;
            // IPv4 check
            if (/^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/.test(value)) return true;
            // IPv6 check (simplified - has colons and is hex-like)
            if (/^[0-9A-Fa-f:.]+$/.test(value) && value.includes(':')) return true;
            return false;
        }

        function needsResolve(host) {
            if (!host) return false;
            // Strip brackets for IPv6
            const cleanHost = host.replace(/^\[|\]$/g, '');
            return !isIpAddress(cleanHost);
        }

        function formatBackend(backend) {
            const host = formatNetworkHost(backend.host);
            const weight = backend.weight && backend.weight !== 1 ? ' weight=' + backend.weight : '';
            const resolve = needsResolve(backend.host) ? ' resolve' : '';
            return '    server ' + host + ':' + backend.port + weight + resolve + ';';
        }

        function renderUpstreamBlock(name, backends, lbStrategy, lbHashKey, zoneSize) {
            let lines = ['upstream ' + name + ' {'];
            lines.push('    zone ' + name + ' ' + (zoneSize || '64k') + ';');

            if (lbStrategy === 'least_conn') {
                lines.push('    least_conn;');
            } else if (lbStrategy === 'random') {
                lines.push('    random;');
            } else if (lbStrategy === 'hash') {
                lines.push('    hash ' + (lbHashKey || '\$remote_addr') + ';');
            }
            // round_robin is default, no directive needed

            backends.forEach((b, i) => {
                lines.push(formatBackend(b, i === 0));
            });

            lines.push('}');
            return lines.join('\n');
        }

        try {
            const rules = JSON.parse(fs.readFileSync(rulesJsonPath, 'utf8'));
            if (!Array.isArray(rules)) {
                throw new Error('L4 rules is not an array');
            }

            let requiresResolver = false;
            rules.filter(r => r && r.enabled !== false).forEach((r, index) => {
                const protocol = String(r.protocol || 'tcp').trim().toLowerCase();
                const listenHost = String(r.listen_host || '0.0.0.0').trim();
                const listenPort = String(r.listen_port || '').trim();
                const name = String(r.name || (protocol + '-' + listenPort)).trim();

                if (!listenPort) return;

                // Get backends array
                let backends = [];
                if (Array.isArray(r.backends) && r.backends.length > 0) {
                    backends = r.backends.filter(b => b && b.host && b.port).map(b => ({
                        host: String(b.host).trim(),
                        port: Number(b.port),
                        weight: Number(b.weight) || 1,
                        resolve: b.resolve === true
                    }));
                } else if (r.upstream_host && r.upstream_port) {
                    // Fallback to legacy single upstream
                    backends = [{
                        host: String(r.upstream_host).trim(),
                        port: Number(r.upstream_port),
                        weight: 1,
                        resolve: false
                    }];
                }

                if (backends.length === 0) {
                    console.error('Skipping L4 rule ' + name + ': no valid backends');
                    return;
                }
                if (backends.some((backend) => needsResolve(backend.host))) {
                    if (!resolver) {
                        throw new Error('NGINX_LOCAL_RESOLVERS is required for hostname L4 backends');
                    }
                    requiresResolver = true;
                }

                // Get load balancing settings
                const lb = r.load_balancing || {};
                const lbStrategy = String(lb.strategy || 'round_robin').toLowerCase();
                const lbHashKey = lb.hash_key;
                const zoneSize = lb.zone_size || '64k';

                const ruleNameSanitized = sanitizeDomain(name);
                const upstreamName = 'up_' + ruleNameSanitized + '_' + protocol + '_' + listenPort;
                const confName = ruleNameSanitized + '.' + protocol + '.' + listenPort + '.conf';
                const listenHostFmt = formatNetworkHost(listenHost);

                // Build listen directive
                const listenDirective = protocol === 'udp'
                    ? '    listen ' + listenHostFmt + ':' + listenPort + ' udp reuseport;'
                    : '    listen ' + listenHostFmt + ':' + listenPort + ';';

                // Build proxy timeouts based on protocol
                const proxyConnectTimeout = protocol === 'udp' ? '' : '\n    proxy_connect_timeout 10s;';
                const proxyTimeout = protocol === 'udp'
                    ? '    proxy_timeout 20s;'
                    : '    proxy_timeout 10m;';

                // Generate config
                const configLines = [
                    '# L4 Forward: ' + name,
                    '# Protocol: ' + protocol.toUpperCase(),
                    '# Listen: ' + listenHost + ':' + listenPort,
                    '# Backends: ' + backends.length,
                    '# Load Balancing: ' + lbStrategy,
                    '',
                    renderUpstreamBlock(upstreamName, backends, lbStrategy, lbHashKey, zoneSize),
                    '',
                    'server {',
                    listenDirective,
                    proxyConnectTimeout,
                    proxyTimeout,
                    '    proxy_pass ' + upstreamName + ';',
                    '}',
                    ''
                ];

                const configPath = path.join(streamDynamicDir, confName);
                fs.writeFileSync(configPath, configLines.join('\n'), 'utf8');

                const backendList = backends.map(b => b.host + ':' + b.port).join(', ');
                console.log('[PROXY] Generated L4 config for ' + protocol + ' ' + listenHost + ':' + listenPort + ' -> [' + backendList + '] (strategy: ' + lbStrategy + ')');
            });

            if (requiresResolver) {
                fs.writeFileSync(
                    path.join(streamDynamicDir, '_resolver.conf'),
                    [
                        '# L4 hostname resolver',
                        'resolver ' + resolver + ';',
                        'resolver_timeout 5s;',
                        ''
                    ].join('\n'),
                    'utf8'
                );
            }
        } catch (e) {
            console.error('Error generating L4 configs: ' + e.message);
            process.exit(1);
        }
    "; then
        rm -rf "$tmp_stream_dir"
        return 1
    fi

    find "$STREAM_DYNAMIC_DIR" -maxdepth 1 -type f -name '*.conf' -delete
    find "$tmp_stream_dir" -maxdepth 1 -type f -name '*.conf' -exec mv {} "$STREAM_DYNAMIC_DIR"/ \;
    rmdir "$tmp_stream_dir"
}

# --- Main Flow ---
deploy_mode=$(normalize_deploy_mode)
mkdir -p "$DYNAMIC_DIR" "$DIRECT_CERT_DIR" "$STREAM_DYNAMIC_DIR"
rm -f "$DYNAMIC_DIR"/*.conf

tmp_rules=$(mktemp)
tmp_issue_certs=$(mktemp)
tmp_active_certs=$(mktemp)
tmp_l4_rules=$(mktemp)
collect_rules "$tmp_rules"
collect_l4_rules "$tmp_l4_rules"

if [ -s "$tmp_rules" ]; then
    while IFS=, read -r frontend_url backend_url proxy_redirect || [ -n "$frontend_url" ]; do
        [ -z "$backend_url" ] && continue
        # 默认为启用 proxy_redirect (1)
        proxy_redirect=${proxy_redirect:-1}
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
            if [ "$proto" = "https" ]; then
                echo "$cert_dom" >> "$tmp_active_certs"
                cert_mode=$(resolve_managed_certificate_mode "$cert_dom")
                case "$cert_mode" in
                    master_cf_dns)
                        if ! install_synced_certificate "$cert_dom"; then
                            echo "[PROXY] error: managed certificate for $cert_dom is configured as master_cf_dns but no synced certificate material was found" >&2
                            exit 1
                        fi
                        entrypoint_log "Installed synced certificate for $cert_dom"
                        ;;
                    local_http01)
                        printf 'local_http01|%s\n' "$cert_dom" >> "$tmp_issue_certs"
                        ;;
                    disabled)
                        echo "[PROXY] error: managed certificate for $cert_dom is disabled; enable the certificate or adjust the HTTPS rule" >&2
                        exit 1
                        ;;
                    *)
                        if ! install_synced_certificate "$cert_dom"; then
                            printf 'auto|%s\n' "$cert_dom" >> "$tmp_issue_certs"
                        else
                            entrypoint_log "Installed synced certificate for $cert_dom"
                        fi
                        ;;
                esac
            fi
        fi

        # 根据 proxy_redirect 生成配置
        if [ "$proxy_redirect" = "1" ]; then
            # 启用 proxy_redirect: 生成 302/307 处理配置
            if [ "$deploy_mode" = "front_proxy" ]; then
                location_proxy_redirect='        proxy_redirect ~^(https?)://([^:/]+(?::\d+)?)(/.+)$ $http_x_forwarded_proto://$http_x_forwarded_host:$http_x_forwarded_port/backstream/$1/$2$3;'
            else
                location_proxy_redirect='        proxy_redirect ~^(https?)://([^:/]+(?::\d+)?)(/.+)$ $scheme://$host:$server_port/backstream/$1/$2$3;'
            fi
            # 生成 backstream 配置
            if [ "$deploy_mode" = "front_proxy" ]; then
                backstream_config='    location ~  ^/backstream/(https?)/([^/]+)  {
        set $website                          $1://$2;
        rewrite ^/backstream/(https?)/([^/]+)(/.+)$  $3 break;
        early_hints $early_hints;
        proxy_pass                            $website;

        proxy_set_header Host                 $proxy_host;

        proxy_http_version                    1.1;
        proxy_cache_bypass                    $http_upgrade;
        proxy_set_header Upgrade              $http_upgrade;
        proxy_set_header Connection           $connection_upgrade;

        proxy_ssl_server_name                 on;

        proxy_connect_timeout                 60s;
        proxy_send_timeout                    60s;
        proxy_read_timeout                    60s;

        proxy_redirect ~^(https?)://([^:/]+(?::\d+)?)(/.+)$ $http_x_forwarded_proto://$http_x_forwarded_host:$http_x_forwarded_port/backstream/$1/$2$3;

        proxy_intercept_errors on;
        error_page 307 = @handle_redirect;
    }

    location @handle_redirect {
        set $saved_redirect_location '"'"'$upstream_http_location'"'"';
        early_hints $early_hints;
        proxy_pass $saved_redirect_location;
        proxy_set_header Host                 $proxy_host;
        proxy_http_version                    1.1;
        proxy_cache_bypass                    $http_upgrade;

        proxy_ssl_server_name                 on;

        proxy_set_header Upgrade              $http_upgrade;
        proxy_set_header Connection           $connection_upgrade;

        proxy_connect_timeout                 60s;
        proxy_send_timeout                    60s;
        proxy_read_timeout                    60s;
    }
'
            else
                backstream_config='    location ~ ^/backstream/(https?)/([^/]+) {
        set $website $1://$2;
        rewrite ^/backstream/(https?)/([^/]+)(/.+)$ $3 break;
        early_hints $early_hints;
        proxy_pass $website;

        proxy_set_header Host $proxy_host;

        proxy_http_version 1.1;
        proxy_cache_bypass $http_upgrade;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        proxy_ssl_server_name on;

        proxy_connect_timeout 60s;
        proxy_send_timeout 60s;
        proxy_read_timeout 60s;

        proxy_redirect ~^(https?)://([^:/]+(?::\d+)?)(/.+)$ $scheme://$host:$server_port/backstream/$1/$2$3;

        proxy_intercept_errors on;
        error_page 307 = @handle_redirect;
    }

    location @handle_redirect {
        set $saved_redirect_location '"'"'$upstream_http_location'"'"';
        early_hints $early_hints;
        proxy_pass $saved_redirect_location;
        proxy_set_header Host $proxy_host;
        proxy_http_version 1.1;
        proxy_cache_bypass $http_upgrade;

        proxy_ssl_server_name on;

        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        proxy_connect_timeout 60s;
        proxy_send_timeout 60s;
        proxy_read_timeout 60s;
    }
'
            fi
        else
            # 禁用 proxy_redirect: 不生成 302/307 处理配置，使用默认的 proxy_redirect 行为
            location_proxy_redirect='        # proxy_redirect disabled - passing redirects directly to client'
            backstream_config=''
        fi

        # 使用 awk 处理多行替换，避免 sed 的转义问题
        awk -v frontend_port="$port" \
            -v domain_name="$srv_name" \
            -v resolver="$RESOLVER" \
            -v domain_path="$path" \
            -v proxy_target="$backend_url" \
            -v cert_dir="$DIRECT_CERT_DIR" \
            -v cert_domain="$cert_dom" \
            -v location_proxy_redirect="$location_proxy_redirect" \
            -v backstream_config="$backstream_config" '
            { gsub(/\${frontend_port}/, frontend_port) }
            { gsub(/\${domain_name}/, domain_name) }
            { gsub(/\${resolver}/, resolver) }
            { gsub(/\${domain_path}/, domain_path) }
            { gsub(/\${proxy_target}/, proxy_target) }
            { gsub(/\${cert_dir}/, cert_dir) }
            { gsub(/\${cert_domain}/, cert_domain) }
            { gsub(/\${location_proxy_redirect}/, location_proxy_redirect) }
            { gsub(/\${backstream_config}/, backstream_config) }
            { print }
        ' "$template" > "$DYNAMIC_DIR/$conf_name"
        entrypoint_log "Generated config for $domain (proxy_redirect: $proxy_redirect)"
    done < "$tmp_rules"
fi

generate_l4_configs "$tmp_l4_rules"

if [ "$deploy_mode" = "direct" ]; then
    if [ -s "$tmp_issue_certs" ]; then
        awk '!seen[$0]++' "$tmp_issue_certs" > "${tmp_issue_certs}.dedup"
        ensure_certificates_for_rules "${tmp_issue_certs}.dedup"
        rm -f "${tmp_issue_certs}.dedup"
    fi
    if [ -s "$tmp_active_certs" ]; then
        awk '!seen[$0]++' "$tmp_active_certs" > "${tmp_active_certs}.dedup"
        cleanup_unused_certificates "${tmp_active_certs}.dedup"
        rm -f "${tmp_active_certs}.dedup"
    else
        : > "$tmp_active_certs"
        cleanup_unused_certificates "$tmp_active_certs"
    fi
fi

rm -f "$tmp_rules" "$tmp_issue_certs" "$tmp_active_certs" "$tmp_l4_rules"
exit 0
