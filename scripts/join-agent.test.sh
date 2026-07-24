#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
script="${script_dir}/join-agent.sh"
tmp="${TMPDIR:-/tmp}/nre-join-agent-test.$$"
functions_file="$tmp/functions.sh"
curl_log="$tmp/curl.log"
mock_bin="$tmp/mock-bin"
uninstall_log="$tmp/uninstall.log"
uninstall_output="$tmp/uninstall.out"

cleanup() {
    rm -f "$functions_file" "$curl_log" \
        "$uninstall_log" "$uninstall_output" \
        "$tmp/explicit-agent" "$tmp/explicit-agent.manifest.json" \
        "$tmp/derived-agent" "$tmp/derived-agent.manifest.json"
    rm -rf "$mock_bin"
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

mkdir -p "$mock_bin"
cat >"$mock_bin/id" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "-u" ]; then
    printf '0\n'
    exit 0
fi
exit 1
EOF
cat >"$mock_bin/uname" <<'EOF'
#!/bin/sh
case "${1:-}" in
    -s) printf 'Linux\n' ;;
    -m) printf 'x86_64\n' ;;
    *) exit 1 ;;
esac
EOF
cat >"$mock_bin/systemctl" <<'EOF'
#!/bin/sh
printf 'systemctl %s\n' "$*" >>"$NRE_UNINSTALL_TEST_LOG"
exit 0
EOF
cat >"$mock_bin/rm" <<'EOF'
#!/bin/sh
printf 'rm %s\n' "$*" >>"$NRE_UNINSTALL_TEST_LOG"
exit 0
EOF
chmod 755 "$mock_bin/id" "$mock_bin/uname" "$mock_bin/systemctl" "$mock_bin/rm"

if ! PATH="$mock_bin:$PATH" NRE_UNINSTALL_TEST_LOG="$uninstall_log" \
    sh "$script" uninstall-agent --data-dir "$tmp/runtime" --source-dir "$tmp/source" \
    >"$uninstall_output" 2>&1; then
    cat "$uninstall_output" >&2
    exit 1
fi
if ! grep -Fq 'rm -f /usr/local/bin/nginx-reverse-emby-agent-uninstall.sh' "$uninstall_log"; then
    printf 'uninstall wrapper removal was not attempted:\n' >&2
    cat "$uninstall_log" >&2
    exit 1
fi

printf 'join-agent custom manifest URL tests passed\n'
