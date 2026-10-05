#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
script="${script_dir}/deploy-compose.sh"
compose_file="${script_dir}/../docker-compose.yaml"
tmp="${TMPDIR:-/tmp}/nre-deploy-compose-test.$$"
env_test_file="${tmp}.env"
secure_env_test_file="${tmp}.secure-env"
secure_env_test_dir="${tmp}.secure-dir"
trap 'rm -f "$tmp" "$env_test_file" "$secure_env_test_file"; rmdir "$secure_env_test_dir" 2>/dev/null || true' EXIT HUP INT TERM

awk '
    function update_depth(line) {
        opens = gsub(/\{/, "{", line)
        closes = gsub(/\}/, "}", line)
        depth += opens - closes
    }

    /^json_string_field\(\)/ ||
    /^json_number_field\(\)/ ||
    /^panel_certificate_objects\(\)/ ||
    /^panel_certificate_object\(\)/ ||
    /^create_panel_self_proxy\(\)/ ||
    /^secure_env_file\(\)/ ||
    /^write_env_value\(\)/ ||
    /^configure_forwarded_headers_trust\(\)/ ||
    /^resolve_domain_ipv4\(\)/ ||
    /^detect_busy_ports\(\)/ ||
    /^preflight_checks\(\)/ ||
    /^wait_public_panel_ready\(\)/ {
        emit = 1
        depth = 0
    }

    emit {
        print
        update_depth($0)
        if (depth == 0) {
            emit = 0
        }
    }
' "$script" >"$tmp"

panel_api_base="http://panel.test"
public_panel_ready_attempts=2
last_panel_self_proxy_error=""
APPLY_CALLS=0
CURL_RULE_STATUS=201
CURL_RULE_BODY='{}'

json_escape() {
    printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

SAY_LOG=""
WARN_LOG=""

say() {
    SAY_LOG="${SAY_LOG}$*
"
}

warn() {
    WARN_LOG="${WARN_LOG}$*
"
}

sleep() {
    :
}

# ---- 部署前预检 mock：DNS / 端口工具与交互全部可控 ----
interactive=0
UNAVAILABLE_COMMANDS=""
TEST_PUBLIC_IP="203.0.113.10"
GETENT_OUTPUT=""
GETENT_STATUS=0
NSLOOKUP_OUTPUT=""
NSLOOKUP_STATUS=0
HOST_OUTPUT=""
HOST_STATUS=0
SS_OUTPUT=""
SS_STATUS=0
NETSTAT_OUTPUT=""
NETSTAT_STATUS=0
ASK_YES_NO_CALLS=0
ASK_YES_NO_RESULT=0

has_command() {
    case " ${UNAVAILABLE_COMMANDS} " in
        *" $1 "*) return 1 ;;
    esac
    command -v "$1" >/dev/null 2>&1
}

detect_public_ip() {
    printf '%s' "$TEST_PUBLIC_IP"
}

ask_yes_no() {
    ASK_YES_NO_CALLS=$((ASK_YES_NO_CALLS + 1))
    return "$ASK_YES_NO_RESULT"
}

getent() {
    [ "$GETENT_STATUS" -eq 0 ] || return "$GETENT_STATUS"
    printf '%s\n' "$GETENT_OUTPUT"
}

nslookup() {
    [ "$NSLOOKUP_STATUS" -eq 0 ] || return "$NSLOOKUP_STATUS"
    printf '%s\n' "$NSLOOKUP_OUTPUT"
}

host() {
    [ "$HOST_STATUS" -eq 0 ] || return "$HOST_STATUS"
    printf '%s\n' "$HOST_OUTPUT"
}

ss() {
    [ "$SS_STATUS" -eq 0 ] || return "$SS_STATUS"
    printf '%s\n' "$SS_OUTPUT"
}

netstat() {
    [ "$NETSTAT_STATUS" -eq 0 ] || return "$NETSTAT_STATUS"
    printf '%s\n' "$NETSTAT_OUTPUT"
}

is_panel_html() {
    return 1
}

curl() {
    response_file=""
    is_apply=0
    is_rules=0
    previous_arg=""

    for arg do
        if [ "$previous_arg" = "-o" ]; then
            response_file="$arg"
        fi
        previous_arg="$arg"
        case "$arg" in
            */apply) is_apply=1 ;;
            */rules) is_rules=1 ;;
        esac
    done

    if [ "$is_apply" -eq 1 ]; then
        APPLY_CALLS=$((APPLY_CALLS + 1))
        return 0
    fi

    if [ "$is_rules" -eq 1 ]; then
        if [ -n "$response_file" ]; then
            printf '%s' "$CURL_RULE_BODY" >"$response_file"
        fi
        printf '%s' "$CURL_RULE_STATUS"
        return 0
    fi

    return 22
}

. "$tmp"

assert_eq() {
    label="$1"
    got="$2"
    expected="$3"
    if [ "$got" != "$expected" ]; then
        printf '%s: got %s, want %s\n' "$label" "$got" "$expected" >&2
        exit 1
    fi
}

assert_apply_calls() {
    expected="$1"
    label="$2"
    if [ "$APPLY_CALLS" -ne "$expected" ]; then
        printf '%s: apply calls = %s, want %s\n' "$label" "$APPLY_CALLS" "$expected" >&2
        exit 1
    fi
}

umask 022
secure_env_file "$secure_env_test_file"
case "$(uname -s 2>/dev/null || true)" in
    MINGW*|MSYS*|CYGWIN*) ;;
    *)
        secure_env_mode="$(stat -c '%a' "$secure_env_test_file" 2>/dev/null || stat -f '%Lp' "$secure_env_test_file")"
        assert_eq "new env file mode" "$secure_env_mode" "600"
        chmod 644 "$secure_env_test_file"
        secure_env_file "$secure_env_test_file"
        secure_env_mode="$(stat -c '%a' "$secure_env_test_file" 2>/dev/null || stat -f '%Lp' "$secure_env_test_file")"
        assert_eq "existing env file mode" "$secure_env_mode" "600"
        ;;
esac
mkdir "$secure_env_test_dir"
if secure_env_file "$secure_env_test_dir"; then
    printf 'secure env setup accepted a non-regular path\n' >&2
    exit 1
fi

if ! grep -Fq 'NRE_TRUST_FORWARDED_HEADERS: "${NRE_TRUST_FORWARDED_HEADERS:-false}"' "$compose_file"; then
    printf 'compose does not pass NRE_TRUST_FORWARDED_HEADERS with a safe default\n' >&2
    exit 1
fi

: >"$env_test_file"
trust_forwarded_headers=""
public_url="https://existing-proxy.example"
domain=""
configure_forwarded_headers_trust "$env_test_file"
assert_eq "existing proxy trust" "$(grep '^NRE_TRUST_FORWARDED_HEADERS=' "$env_test_file")" "NRE_TRUST_FORWARDED_HEADERS=true"

public_url=""
domain=""
configure_forwarded_headers_trust "$env_test_file"
assert_eq "direct HTTP trust" "$(grep '^NRE_TRUST_FORWARDED_HEADERS=' "$env_test_file")" "NRE_TRUST_FORWARDED_HEADERS=false"

domain="panel-self-proxy.example"
configure_forwarded_headers_trust "$env_test_file"
assert_eq "panel self-proxy trust" "$(grep '^NRE_TRUST_FORWARDED_HEADERS=' "$env_test_file")" "NRE_TRUST_FORWARDED_HEADERS=true"

trust_forwarded_headers="false"
configure_forwarded_headers_trust "$env_test_file"
assert_eq "explicit trust override" "$(grep '^NRE_TRUST_FORWARDED_HEADERS=' "$env_test_file")" "NRE_TRUST_FORWARDED_HEADERS=false"

CERT_LIST='{"certificates":[{"id":1,"domain":"first.example","enabled":true,"status":"active","last_error":""},{"id":2,"domain":"target.example","enabled":true,"status":"active","last_error":"","agent_reports":{"local":{"status":"error","last_error":"nested error"}}},{"id":3,"domain":"third.example","enabled":true,"status":"issuing","last_error":""}],"ok":true}'
TARGET_CERT="$(panel_certificate_object "$CERT_LIST" "" "target.example")"
assert_eq "middle certificate id" "$(json_number_field "$TARGET_CERT" id)" "2"
assert_eq "middle certificate status" "$(json_string_field "$TARGET_CERT" status)" "active"
assert_eq "middle certificate last_error" "$(json_string_field "$TARGET_CERT" last_error)" ""

THIRD_CERT="$(panel_certificate_object "$CERT_LIST" "3" "")"
assert_eq "id-selected certificate domain" "$(json_string_field "$THIRD_CERT" domain)" "third.example"
assert_eq "id-selected certificate status" "$(json_string_field "$THIRD_CERT" status)" "issuing"

APPLY_CALLS=0
CURL_RULE_STATUS=201
CURL_RULE_BODY='{}'
create_panel_self_proxy "token" "panel.example.com" "https" "1"
assert_apply_calls 0 "deferred HTTPS create"

APPLY_CALLS=0
CURL_RULE_STATUS=409
CURL_RULE_BODY='{"message":"frontend_url conflicts with existing rule"}'
create_panel_self_proxy "token" "panel.example.com" "https" "1"
assert_apply_calls 0 "deferred HTTPS existing rule"

APPLY_CALLS=0
CURL_RULE_STATUS=201
CURL_RULE_BODY='{}'
create_panel_self_proxy "token" "panel.example.com" "http" "0"
assert_apply_calls 1 "non-deferred HTTP create"

APPLY_CALLS=0
if wait_public_panel_ready "token" "https://panel.example.com/" "0"; then
    printf 'deferred readiness unexpectedly succeeded\n' >&2
    exit 1
fi
assert_apply_calls 0 "deferred readiness poll"

APPLY_CALLS=0
if wait_public_panel_ready "token" "http://panel.example.com/" "1"; then
    printf 'non-deferred readiness unexpectedly succeeded\n' >&2
    exit 1
fi
assert_apply_calls 2 "non-deferred readiness poll"

# ---- 部署前预检 ----

reset_preflight_env() {
    UNAVAILABLE_COMMANDS=""
    TEST_PUBLIC_IP="203.0.113.10"
    GETENT_OUTPUT=""
    GETENT_STATUS=0
    NSLOOKUP_OUTPUT=""
    NSLOOKUP_STATUS=0
    HOST_OUTPUT=""
    HOST_STATUS=0
    SS_OUTPUT=""
    SS_STATUS=0
    NETSTAT_OUTPUT=""
    NETSTAT_STATUS=0
    ASK_YES_NO_CALLS=0
    ASK_YES_NO_RESULT=0
    SAY_LOG=""
    WARN_LOG=""
    interactive=0
}

assert_contains() {
    label="$1"
    haystack="$2"
    needle="$3"
    case "$haystack" in
        *"$needle"*) ;;
        *)
            printf '%s: missing "%s" in output:\n%s\n' "$label" "$needle" "$haystack" >&2
            exit 1
            ;;
    esac
}

# 匹配：多 A 记录任一匹配即通过，端口空闲 → 预检通过。
reset_preflight_env
GETENT_OUTPUT='198.51.100.7   STREAM panel.example.com
203.0.113.10    STREAM panel.example.com'
if ! preflight_checks "panel.example.com"; then
    printf 'matching preflight unexpectedly aborted\n' >&2
    exit 1
fi
assert_contains "preflight pass summary" "$SAY_LOG" "预检通过"
assert_contains "preflight dns match" "$SAY_LOG" "203.0.113.10"
assert_eq "preflight pass ask calls" "$ASK_YES_NO_CALLS" "0"

# 不匹配 + 非交互：输出实际解析值与公网 IP 的警告并继续。
reset_preflight_env
GETENT_OUTPUT='198.51.100.7   STREAM panel.example.com'
if ! preflight_checks "panel.example.com"; then
    printf 'noninteractive mismatch should continue\n' >&2
    exit 1
fi
assert_contains "mismatch resolved value" "$WARN_LOG" "198.51.100.7"
assert_contains "mismatch public ip" "$WARN_LOG" "203.0.113.10"
assert_contains "mismatch continue summary" "$WARN_LOG" "预检有警告，可继续"
assert_eq "mismatch noninteractive ask calls" "$ASK_YES_NO_CALLS" "0"

# 不匹配 + 交互选择中止：返回 1 且只询问一次。
reset_preflight_env
GETENT_OUTPUT='198.51.100.7   STREAM panel.example.com'
interactive=1
ASK_YES_NO_RESULT=1
if preflight_checks "panel.example.com"; then
    printf 'interactive abort was ignored\n' >&2
    exit 1
fi
assert_eq "mismatch abort ask calls" "$ASK_YES_NO_CALLS" "1"

# 80/443 被占用 + 非交互：输出占用端口并继续。
reset_preflight_env
GETENT_OUTPUT='203.0.113.10   STREAM panel.example.com'
SS_OUTPUT='LISTEN 0 128 0.0.0.0:443 0.0.0.0:*
LISTEN 0 128 [::]:80 [::]:*'
if ! preflight_checks "panel.example.com"; then
    printf 'noninteractive busy ports should continue\n' >&2
    exit 1
fi
assert_contains "busy port 443" "$WARN_LOG" "443"
assert_contains "busy port 80" "$WARN_LOG" "80"
assert_contains "busy port continue summary" "$WARN_LOG" "预检有警告，可继续"
assert_eq "busy ports noninteractive ask calls" "$ASK_YES_NO_CALLS" "0"

# 80 被占用 + 交互默认继续：询问一次后继续。
reset_preflight_env
GETENT_OUTPUT='203.0.113.10   STREAM panel.example.com'
SS_OUTPUT='LISTEN 0 128 *:80 *:*'
interactive=1
if ! preflight_checks "panel.example.com"; then
    printf 'interactive busy ports should continue by default\n' >&2
    exit 1
fi
assert_eq "busy ports interactive ask calls" "$ASK_YES_NO_CALLS" "1"
assert_contains "busy ports interactive summary" "$WARN_LOG" "预检有警告，可继续"

# ss 缺失时回退 netstat。
reset_preflight_env
GETENT_OUTPUT='203.0.113.10   STREAM panel.example.com'
UNAVAILABLE_COMMANDS="ss"
NETSTAT_OUTPUT='tcp 0 0 0.0.0.0:443 0.0.0.0:* LISTEN'
if ! preflight_checks "panel.example.com"; then
    printf 'netstat fallback should continue\n' >&2
    exit 1
fi
assert_contains "netstat fallback busy port" "$WARN_LOG" "443"

# DNS 与端口工具全部缺失：降级为警告并继续。
reset_preflight_env
UNAVAILABLE_COMMANDS="getent nslookup host ss netstat"
if ! preflight_checks "panel.example.com"; then
    printf 'missing tools should continue\n' >&2
    exit 1
fi
assert_contains "dns missing tools warning" "$WARN_LOG" "无法解析"
assert_contains "port missing tools warning" "$WARN_LOG" "ss/netstat"
assert_contains "missing tools summary" "$WARN_LOG" "预检有警告，可继续"
assert_eq "missing tools ask calls" "$ASK_YES_NO_CALLS" "0"

# 仅解析出 IPv6：无法比对，降级为警告并继续。
reset_preflight_env
GETENT_OUTPUT='2001:db8::1    STREAM panel.example.com'
if ! preflight_checks "panel.example.com"; then
    printf 'ipv6-only resolution should continue\n' >&2
    exit 1
fi
assert_contains "ipv6-only warning" "$WARN_LOG" "无法解析"
assert_contains "ipv6-only continue summary" "$WARN_LOG" "预检有警告，可继续"

printf 'deploy-compose deferred apply tests passed\n'
