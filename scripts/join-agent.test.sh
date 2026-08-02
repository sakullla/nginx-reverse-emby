#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
script="${script_dir}/join-agent.sh"
tmp="${TMPDIR:-/tmp}/nre-join-agent-test.$$"
functions_file="$tmp/functions.sh"
curl_log="$tmp/curl.log"
curl_timeout_log="$tmp/curl-timeout.log"
mock_bin="$tmp/mock-bin"
uninstall_log="$tmp/uninstall.log"
uninstall_output="$tmp/uninstall.out"

cleanup() {
    rm -f "$functions_file" "$curl_log" "$curl_timeout_log" \
        "$uninstall_log" "$uninstall_output" "$tmp/stage.out" "$tmp/register-replay.out" \
        "$tmp/explicit-agent" "$tmp/explicit-agent.manifest.json" \
        "$tmp/derived-agent" "$tmp/derived-agent.manifest.json" \
        "$tmp/default-agent" "$tmp/default-agent.manifest.json"
    rm -rf "$mock_bin" "$tmp/pki-data" "$tmp/go-pending-data" "$tmp/incomplete-pending-data"
    rmdir "$tmp" 2>/dev/null || true
}

trap cleanup EXIT HUP INT TERM
mkdir -p "$tmp"

sed '/^COMMAND="join"$/,$d' "$script" >"$functions_file"

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

run_go_shell_pki_contract() {
    go_data_dir="$DATA_DIR"
    if command -v cygpath >/dev/null 2>&1; then
        go_data_dir="$(cygpath -w "$DATA_DIR")"
    fi
    (
        cd "$script_dir/../go-agent"
        NRE_TEST_SHELL_PKI_DATA_DIR="$go_data_dir" \
            go test -count=1 ./internal/modules/pki -run '^TestShellPendingEnrollmentContract$'
    )
}

assert_eq "escaped JSON string" \
    "$(extract_json_string '{"agent_token":"quote\"slash\\tail"}' agent_token)" \
    'quote"slash\tail'

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
    timeout=""
    url=""
    previous=""
    for arg do
        if [ "$previous" = "-o" ]; then
            output="$arg"
        fi
        if [ "$previous" = "--max-time" ]; then
            timeout="$arg"
        fi
        case "$arg" in
            http://*|https://*) url="$arg" ;;
        esac
        previous="$arg"
    done
    printf '%s\n' "$url" >>"$curl_log"
    printf '%s\n' "$timeout" >>"$curl_timeout_log"
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
assert_eq "explicit download timeouts" "$(cat "$curl_timeout_log")" \
    "$(printf '%s\n%s' '1800' '1800')"

: >"$curl_log"
: >"$curl_timeout_log"
MANIFEST_URL=""
copy_or_download_binary "nre-agent-linux-amd64" "$tmp/derived-agent"
assert_eq "derived manifest request" "$(cat "$curl_log")" \
    "$(printf '%s\n%s' "$BINARY_URL" 'https://downloads.example/nre-agent.manifest.json?binary=signature')"
assert_eq "derived download timeouts" "$(cat "$curl_timeout_log")" \
    "$(printf '%s\n%s' '1800' '1800')"

: >"$curl_log"
: >"$curl_timeout_log"
BINARY_URL=""
ASSET_BASE_URL="https://panel.example/panel-api/public/agent-assets"
copy_or_download_binary "nre-agent-linux-amd64" "$tmp/default-agent"
assert_eq "default asset requests" "$(cat "$curl_log")" \
    "$(printf '%s\n%s' "$ASSET_BASE_URL/nre-agent-linux-amd64" "$ASSET_BASE_URL/nre-agent-linux-amd64.manifest.json")"
assert_eq "default download timeouts" "$(cat "$curl_timeout_log")" \
    "$(printf '%s\n%s' '1800' '1800')"

DATA_DIR="$tmp/pki-data"
AGENT_ID=""
PKI_DOMAIN_ID=""
PKI_SECURITY_ACK_JSON=""
prepare_tunnel_enrollment
first_request_id="$PKI_ENROLLMENT_REQUEST_ID"
first_csr="$PKI_TUNNEL_CSR_PEM"
if [ ! -s "$DATA_DIR/pki/identities/agent/pending/private-key.pem" ] || \
   [ ! -s "$DATA_DIR/pki/identities/agent/pending/request.json" ]; then
    printf 'tunnel enrollment material was not persisted\n' >&2
    exit 1
fi
pending_mode="$(stat -c '%a' "$DATA_DIR/pki/identities/agent/pending" 2>/dev/null || stat -f '%Lp' "$DATA_DIR/pki/identities/agent/pending")"
private_key_mode="$(stat -c '%a' "$DATA_DIR/pki/identities/agent/pending/private-key.pem" 2>/dev/null || stat -f '%Lp' "$DATA_DIR/pki/identities/agent/pending/private-key.pem")"
assert_eq "pending directory mode" "$pending_mode" "700"
assert_eq "pending private-key mode" "$private_key_mode" "600"
if [ -e "$DATA_DIR/pki/identities/agent/pending/request-id" ]; then
    printf 'new tunnel enrollment unexpectedly created the legacy request-id sidecar\n' >&2
    exit 1
fi
anonymous_subject="$(openssl req -in "$DATA_DIR/pki/identities/agent/pending/request.csr.pem" -noout -subject -nameopt RFC2253 | tr -d '[:space:]')"
case "$anonymous_subject" in
    subject=) ;;
    *) printf 'anonymous enrollment CSR has unexpected subject: %s\n' "$anonymous_subject" >&2; exit 1 ;;
esac
anonymous_csr_text="$(openssl req -in "$DATA_DIR/pki/identities/agent/pending/request.csr.pem" -noout -text)"
if printf '%s\n' "$anonymous_csr_text" | grep -Eq 'X509v3|URI:|DNS:|IP Address:|email:'; then
    printf 'anonymous enrollment CSR contains requested identity extensions\n' >&2
    exit 1
fi
if ! printf '%s\n' "$anonymous_csr_text" | grep -Fq 'Signature Algorithm: ecdsa-with-SHA256'; then
    printf 'anonymous enrollment CSR does not use ECDSA-SHA256\n' >&2
    exit 1
fi
if grep -Fq 'register-secret' "$DATA_DIR/pki/identities/agent/pending/request.json"; then
    printf 'register token leaked into enrollment journal\n' >&2
    exit 1
fi
prepare_tunnel_enrollment
assert_eq "stable enrollment request id" "$PKI_ENROLLMENT_REQUEST_ID" "$first_request_id"
assert_eq "stable enrollment CSR" "$PKI_TUNNEL_CSR_PEM" "$first_csr"

run_go_shell_pki_contract

MASTER_URL="https://panel.example"
AGENT_NAME="edge"
AGENT_TOKEN="control-before-register"
AGENT_URL=""
AGENT_VERSION="1"
AGENT_TAGS="edge"
AGENT_CAPABILITIES="http_rules"
PLATFORM="linux"
ARCH="amd64"
REGISTER_TOKEN="register-secret"
payload="$(build_register_payload)"
if ! printf '%s' "$payload" | grep -Fq '"pki_enrollment_request_id"'; then
    printf 'registration payload omitted enrollment request id\n' >&2
    exit 1
fi
if ! printf '%s' "$payload" | grep -Fq '"tunnel_csr_pem"'; then
    printf 'registration payload omitted tunnel CSR\n' >&2
    exit 1
fi
if printf '%s' "$payload" | grep -Fq 'PRIVATE KEY'; then
    printf 'registration payload leaked tunnel private key\n' >&2
    exit 1
fi
if ! printf '%s' "$payload" | grep -Fq '"agent_id":""'; then
    printf 'anonymous registration payload unexpectedly claimed a stable agent id\n' >&2
    exit 1
fi

mkdir -p "$DATA_DIR"
ENV_FILE="$DATA_DIR/agent.env"
write_agent_env "$ENV_FILE"
env_mode="$(stat -c '%a' "$ENV_FILE" 2>/dev/null || stat -f '%Lp' "$ENV_FILE")"
case "$env_mode" in
    600) ;;
    *) printf 'agent.env mode is %s, want 600\n' "$env_mode" >&2; exit 1 ;;
esac
if grep -Fq 'register-secret' "$ENV_FILE"; then
    printf 'register token leaked into agent.env\n' >&2
    exit 1
fi

REGISTER_RESPONSE='{"ok":true,"agent":{"id":"agent-1"},"pki":{"agent_id":"agent-1","agent_token":"control-secret","tunnel_credential":{"identity_id":"identity-1","certificate_id":"certificate-1","purpose":"client","certificate_pem":"CERTIFICATE-PEM","public_key_fingerprint_sha256":"abc","authority_id":"authority-1","ca_generation":1,"not_before":"2026-08-02T00:00:00Z","not_after":"2027-08-02T00:00:00Z"},"security_snapshot":{"pki_domain_id":"domain-1","pki_epoch":1,"security_revision":0,"full":true,"issued_at":"2026-08-02T00:00:00Z","trust_roots":[],"revoked_identity_ids":[],"revoked_serials":[],"signer_generation":1,"signature":"AA=="}}}'
stage_output="$tmp/stage.out"
stage_pki_registration_response >"$stage_output"
if grep -Fq 'control-secret' "$stage_output" || grep -Fq 'CERTIFICATE-PEM' "$stage_output"; then
    printf 'registration output leaked raw credential material\n' >&2
    exit 1
fi
assert_eq "registered stable agent id" "$AGENT_ID" "agent-1"
assert_eq "registered control token" "$AGENT_TOKEN" "control-secret"
assert_eq "registered PKI domain" "$PKI_DOMAIN_ID" "domain-1"
staged_response="$DATA_DIR/pki/identities/agent/pending/response.json"
if ! grep -Fq '"certificate_id":"certificate-1"' "$staged_response"; then
    printf 'sanitized staged response omitted credential\n' >&2
    exit 1
fi
if grep -Fq 'control-secret' "$staged_response" || grep -Fq 'register-secret' "$staged_response"; then
    printf 'secret leaked into staged PKI response\n' >&2
    exit 1
fi
run_go_shell_pki_contract
prepare_tunnel_enrollment
assert_eq "staged-response replay request id" "$PKI_ENROLLMENT_REQUEST_ID" "$first_request_id"
assert_eq "staged-response replay CSR" "$PKI_TUNNEL_CSR_PEM" "$first_csr"
if [ ! -s "$staged_response" ]; then
    printf 'join rerun orphaned the staged credential response\n' >&2
    exit 1
fi
if [ -e "$DATA_DIR/pki/identities/agent/staged" ]; then
    printf 'join rerun created an unconsumed staged credential directory\n' >&2
    exit 1
fi
if [ "${PKI_STAGED_REGISTRATION_PRESENT:-0}" != "1" ]; then
    printf 'join rerun did not recognize the staged registration response\n' >&2
    exit 1
fi
replay_payload="$(build_register_payload)"
if ! printf '%s' "$replay_payload" | grep -Fq '"agent_id":""'; then
    printf 'registration replay changed the original anonymous agent id\n' >&2
    exit 1
fi
: >"$curl_log"
register_agent >"$tmp/register-replay.out"
if [ -s "$curl_log" ]; then
    printf 'staged registration replay unexpectedly called the register endpoint\n' >&2
    exit 1
fi
rm -f "$tmp/register-replay.out"

# Simulate a crash/power-loss ordering where the public staged response is
# present but agent.env is unavailable. The response contains no raw control
# token, so the client must replay the original enrollment instead of trusting
# a newly generated token and silently skipping registration.
registration_replay_response="$REGISTER_RESPONSE"
rm -f "$ENV_FILE"
AGENT_ID=""
AGENT_TOKEN="new-unpersisted-control-token"
PKI_DOMAIN_ID=""
AGENT_CONTROL_TOKEN_PERSISTED="0"
prepare_tunnel_enrollment
assert_eq "staged response without durable token requires replay" "$PKI_STAGED_REGISTRATION_PRESENT" "0"
curl() {
    printf '%s' "$registration_replay_response"
}
register_agent >"$tmp/register-replay.out"
assert_eq "registration replay restored stable agent id" "$AGENT_ID" "agent-1"
assert_eq "registration replay restored control token" "$AGENT_TOKEN" "control-secret"
assert_eq "registration replay restored PKI domain" "$PKI_DOMAIN_ID" "domain-1"
assert_eq "registration replay persisted token provenance" "$AGENT_CONTROL_TOKEN_PERSISTED" "1"
rm -f "$tmp/register-replay.out"

shell_data_dir="$DATA_DIR"
go_pending_root="$tmp/go-pending-data/pki/identities/agent/pending"
mkdir -p "$go_pending_root"
cp "$shell_data_dir/pki/identities/agent/pending/private-key.pem" "$go_pending_root/private-key.pem"
cp "$shell_data_dir/pki/identities/agent/pending/request.csr.pem" "$go_pending_root/request.csr.pem"
cp "$shell_data_dir/pki/identities/agent/pending/request.json" "$go_pending_root/request.json"
chmod 700 "$tmp/go-pending-data/pki" "$tmp/go-pending-data/pki/identities" \
    "$tmp/go-pending-data/pki/identities/agent" "$go_pending_root"
chmod 600 "$go_pending_root/private-key.pem" "$go_pending_root/request.csr.pem" "$go_pending_root/request.json"
DATA_DIR="$tmp/go-pending-data"
prepare_tunnel_enrollment
assert_eq "Go-layout pending request id" "$PKI_ENROLLMENT_REQUEST_ID" "$first_request_id"
assert_eq "Go-layout pending CSR" "$PKI_TUNNEL_CSR_PEM" "$first_csr"
if [ -e "$go_pending_root/request-id" ]; then
    printf 'Go-layout pending replay required a legacy request-id sidecar\n' >&2
    exit 1
fi

DATA_DIR="$tmp/incomplete-pending-data"
incomplete_root="$DATA_DIR/pki/identities/agent/pending"
mkdir -p "$incomplete_root"
printf 'sole-private-key-copy\n' >"$incomplete_root/private-key.pem"
if (prepare_tunnel_enrollment >/dev/null 2>&1); then
    printf 'incomplete pending enrollment was silently replaced\n' >&2
    exit 1
fi
if ! grep -Fq 'sole-private-key-copy' "$incomplete_root/private-key.pem"; then
    printf 'incomplete pending enrollment private key was deleted\n' >&2
    exit 1
fi
DATA_DIR="$shell_data_dir"
write_agent_env "$ENV_FILE"
if ! grep -Fq "NRE_AGENT_ID='agent-1'" "$ENV_FILE" || ! grep -Fq "NRE_AGENT_TOKEN='control-secret'" "$ENV_FILE"; then
    printf 'agent.env did not retain stable control credentials\n' >&2
    exit 1
fi

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

printf 'join-agent manifest and PKI credential tests passed\n'
