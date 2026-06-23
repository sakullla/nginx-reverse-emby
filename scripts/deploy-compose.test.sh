#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
script="${script_dir}/deploy-compose.sh"
tmp="${TMPDIR:-/tmp}/nre-deploy-compose-test.$$"
trap 'rm -f "$tmp"' EXIT HUP INT TERM

awk '
    function update_depth(line) {
        opens = gsub(/\{/, "{", line)
        closes = gsub(/\}/, "}", line)
        depth += opens - closes
    }

    /^create_panel_self_proxy\(\)/ || /^wait_public_panel_ready\(\)/ {
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

say() {
    :
}

sleep() {
    :
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

assert_apply_calls() {
    expected="$1"
    label="$2"
    if [ "$APPLY_CALLS" -ne "$expected" ]; then
        printf '%s: apply calls = %s, want %s\n' "$label" "$APPLY_CALLS" "$expected" >&2
        exit 1
    fi
}

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

printf 'deploy-compose deferred apply tests passed\n'
