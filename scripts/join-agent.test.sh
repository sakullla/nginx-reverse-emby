#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
script="${script_dir}/join-agent.sh"
tmp="${TMPDIR:-/tmp}/nre-join-agent-test.$$"
functions_file="$tmp/functions.sh"
curl_log="$tmp/curl.log"

cleanup() {
    rm -f "$functions_file" "$curl_log" \
        "$tmp/explicit-agent" "$tmp/explicit-agent.manifest.json" \
        "$tmp/derived-agent" "$tmp/derived-agent.manifest.json"
    rmdir "$tmp" 2>/dev/null || true
}

trap cleanup EXIT HUP INT TERM
mkdir -p "$tmp"

awk '
    function update_depth(line) {
        opens = gsub(/\{/, "{", line)
        closes = gsub(/\}/, "}", line)
        depth += opens - closes
    }

    /^companion_manifest_url\(\)/ ||
    /^copy_or_download_binary\(\)/ {
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
' "$script" >"$functions_file"

. "$functions_file"

assert_eq() {
    label="$1"
    got="$2"
    expected="$3"
    if [ "$got" != "$expected" ]; then
        printf '%s: got %s, want %s\n' "$label" "$got" "$expected" >&2
        exit 1
    fi
}

assert_eq "plain companion URL" \
    "$(companion_manifest_url 'https://downloads.example/nre-agent')" \
    "https://downloads.example/nre-agent.manifest.json"
assert_eq "query companion URL" \
    "$(companion_manifest_url 'https://downloads.example/nre-agent?token=abc')" \
    "https://downloads.example/nre-agent.manifest.json?token=abc"
assert_eq "query and fragment companion URL" \
    "$(companion_manifest_url 'https://downloads.example/nre-agent?token=abc#section')" \
    "https://downloads.example/nre-agent.manifest.json?token=abc#section"

curl() {
    output=""
    url=""
    previous=""
    for arg do
        if [ "$previous" = "-o" ]; then
            output="$arg"
        fi
        case "$arg" in
            http://*|https://*) url="$arg" ;;
        esac
        previous="$arg"
    done
    printf '%s\n' "$url" >>"$curl_log"
    : >"$output"
}

verify_binary_manifest() {
    return 0
}

SCRIPT_DIR=""
ASSET_BASE_URL=""
PLATFORM="linux"
ARCH="amd64"
BINARY_URL="https://downloads.example/nre-agent?binary=signature"
MANIFEST_URL="https://manifests.example/nre-agent.json?manifest=signature"
copy_or_download_binary "nre-agent-linux-amd64" "$tmp/explicit-agent"
assert_eq "explicit binary and manifest requests" "$(cat "$curl_log")" \
    "$(printf '%s\n%s' "$BINARY_URL" "$MANIFEST_URL")"

: >"$curl_log"
MANIFEST_URL=""
copy_or_download_binary "nre-agent-linux-amd64" "$tmp/derived-agent"
assert_eq "derived manifest request" "$(cat "$curl_log")" \
    "$(printf '%s\n%s' "$BINARY_URL" 'https://downloads.example/nre-agent.manifest.json?binary=signature')"

printf 'join-agent custom manifest URL tests passed\n'
