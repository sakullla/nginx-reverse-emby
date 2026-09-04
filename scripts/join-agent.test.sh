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
symlink_data_root="$tmp/symlink-ancestor-data"
symlink_external_root="$tmp/symlink-ancestor-external"

cleanup() {
    rm -f "$functions_file" "$curl_log" "$curl_timeout_log" \
        "$uninstall_log" "$uninstall_output" "$tmp/stage.out" "$tmp/register-replay.out" \
        "$tmp/active-force-reenrollment.out" \
        "$tmp/explicit-agent" "$tmp/explicit-agent.manifest.json" \
        "$tmp/derived-agent" "$tmp/derived-agent.manifest.json" \
        "$tmp/default-agent" "$tmp/default-agent.manifest.json"
    rm -rf "$mock_bin" "$tmp/pki-data" "$tmp/go-pending-data" "$tmp/incomplete-pending-data" \
        "$symlink_data_root" "$symlink_external_root" "$tmp/symlink-probe-link" "$tmp/symlink-probe-target"
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

assert_eq "canonical systemd service name" "$SYSTEMD_SERVICE_NAME" "nre-agent.service"
assert_eq "legacy systemd service name" "$LEGACY_SYSTEMD_SERVICE_NAME" "nginx-reverse-emby-agent.service"
grep -Fq 'repair-systemd) run_repair_systemd' "$script" || {
    echo "repair-systemd command must route to the existing Agent repair path" >&2
    exit 1
}
grep -Fq 'ASSET_BASE_URL="$MASTER_URL/panel-api/public/agent-assets"' "$script" || {
    echo "repair-systemd must derive the verified package source from the persisted master URL" >&2
    exit 1
}
grep -Fq 'backup_legacy_unit "$LEGACY_SYSTEMD_SERVICE_NAME"' "$script" || {
    echo "repair-systemd must preserve the legacy unit before cutover" >&2
    exit 1
}
grep -Fq 'install_systemd_service' "$script" || {
    echo "repair-systemd must reuse the canonical service migration implementation" >&2
    exit 1
}
grep -Fq 'Delegate=yes' "$script" || {
    echo "systemd install must delegate the Agent cgroup subtree" >&2
    exit 1
}
grep -Fq 'TasksMax=infinity' "$script" || {
    echo "systemd install must leave per-plugin process limits to delegated cgroups" >&2
    exit 1
}

run_go_pki_store_contract() {
    (
        cd "$script_dir/../go-agent"
        test_output="$(go test -count=1 -v ./internal/modules/pki \
            -run '^TestPrepareEnrollmentPersistsReplaySafeKeyAndCSR$')" || {
            printf '%s\n' "$test_output" >&2
            exit 1
        }
        printf '%s\n' "$test_output"
        printf '%s\n' "$test_output" | grep -Fq -- \
            '--- PASS: TestPrepareEnrollmentPersistsReplaySafeKeyAndCSR' || {
            echo "Go PKI store contract test was not selected" >&2
            exit 1
        }
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

# Linux CI must prove that no PKI ancestor symlink can redirect a tunnel key
# outside DATA_DIR. Git Bash hosts without native symlink permission skip this
# platform capability probe; the same shell assertions remain canonical on
# Linux.
mkdir "$tmp/symlink-probe-target"
if ln -s "$tmp/symlink-probe-target" "$tmp/symlink-probe-link" 2>/dev/null && \
   [ -L "$tmp/symlink-probe-link" ]; then
    for unsafe_component in pki identities agent; do
        rm -rf "$symlink_data_root" "$symlink_external_root"
        mkdir "$symlink_data_root" "$symlink_external_root"
        case "$unsafe_component" in
            pki)
                unsafe_link="$symlink_data_root/pki"
                ;;
            identities)
                mkdir "$symlink_data_root/pki"
                unsafe_link="$symlink_data_root/pki/identities"
                ;;
            agent)
                mkdir "$symlink_data_root/pki"
                mkdir "$symlink_data_root/pki/identities"
                unsafe_link="$symlink_data_root/pki/identities/agent"
                ;;
        esac
        ln -s "$symlink_external_root" "$unsafe_link"
        DATA_DIR="$symlink_data_root"
        AGENT_ID=""
        PKI_DOMAIN_ID=""
        PKI_SECURITY_ACK_JSON=""
        if (prepare_tunnel_enrollment >/dev/null 2>&1); then
            printf 'tunnel enrollment accepted symlinked %s ancestor\n' "$unsafe_component" >&2
            exit 1
        fi
        if [ -n "$(find "$symlink_external_root" -mindepth 1 -print -quit)" ]; then
            printf 'tunnel enrollment wrote through symlinked %s ancestor\n' "$unsafe_component" >&2
            exit 1
        fi
    done
fi
rm -rf "$symlink_data_root" "$symlink_external_root" "$tmp/symlink-probe-link" "$tmp/symlink-probe-target"

# A legacy or interrupted install may retain only one half of the stable PKI
# identity. Treat either partial pair as a fresh anonymous enrollment instead
# of building an invalid SPIFFE URI or refusing to join forever.
for partial_identity_case in agent_id pki_domain; do
    DATA_DIR="$tmp/partial-identity-$partial_identity_case"
    partial_identity_output="$tmp/partial-identity-$partial_identity_case.out"
    case "$partial_identity_case" in
        agent_id)
            AGENT_ID="legacy-agent"
            PKI_DOMAIN_ID=""
            ;;
        pki_domain)
            AGENT_ID=""
            PKI_DOMAIN_ID="legacy-domain"
            ;;
    esac
    PKI_SECURITY_ACK_JSON=""
    prepare_tunnel_enrollment >"$partial_identity_output" 2>&1
    assert_eq "$partial_identity_case clears agent id" "$AGENT_ID" ""
    assert_eq "$partial_identity_case clears PKI domain" "$PKI_DOMAIN_ID" ""
    if ! grep -Fq '[JOIN] Ignoring incomplete stable PKI identity; starting fresh enrollment' "$partial_identity_output"; then
        printf '%s did not report partial identity fallback\n' "$partial_identity_case" >&2
        exit 1
    fi
    partial_request_json="$(tr -d '\r\n' < "$DATA_DIR/pki/identities/agent/pending/request.json")"
    if ! printf '%s' "$partial_request_json" | grep -Fq '"pki_domain_id":"","agent_id":""'; then
        printf '%s enrollment journal retained a partial identity\n' "$partial_identity_case" >&2
        exit 1
    fi
    partial_csr_text="$(openssl req -in "$DATA_DIR/pki/identities/agent/pending/request.csr.pem" -noout -text)"
    if printf '%s\n' "$partial_csr_text" | grep -Eq 'URI:'; then
        printf '%s fallback CSR unexpectedly retained a stable identity\n' "$partial_identity_case" >&2
        exit 1
    fi
    rm -rf "$DATA_DIR"
    rm -f "$partial_identity_output"
done

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

run_go_pki_store_contract

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

REGISTER_RESPONSE='{"ok":true,"agent":{"id":"agent-1"},"pki":{"agent_id":"agent-1","agent_token":"control-secret","tunnel_credential":{"identity_id":"identity-1","certificate_id":"certificate-1","purpose":"client_auth","certificate_pem":"CERTIFICATE-PEM","public_key_fingerprint_sha256":"abc","authority_id":"authority-1","ca_generation":1,"not_before":"2026-08-02T00:00:00Z","not_after":"2027-08-02T00:00:00Z"},"security_snapshot":{"pki_domain_id":"domain-1","pki_epoch":1,"security_revision":0,"full":true,"issued_at":"2026-08-02T00:00:00Z","trust_roots":[],"revoked_identity_ids":[],"revoked_serials":[],"signer_generation":1,"signature":"AA=="}}}'
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
run_go_pki_store_contract
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

# Model migrate-from-main retry after registration and Go credential activation
# succeeded but the later connectivity check rolled the service back. The new
# data root must win over legacy/empty identity state and no anonymous
# enrollment or second registration may be created.
DATA_DIR="$tmp/active-migration-retry"
ENV_FILE="$DATA_DIR/agent.env"
AGENT_ID="agent-1"
AGENT_TOKEN="control-secret"
PKI_DOMAIN_ID="domain-1"
AGENT_CONTROL_TOKEN_PERSISTED="1"
mkdir -p "$DATA_DIR/pki/identities/agent/generations/generation-1" "$DATA_DIR/pki/security"
write_agent_env "$ENV_FILE"
printf '%s' '{"version":1,"generation":"generation-1","manifest_hash":"hash","activated_at":"2026-08-02T00:00:00Z"}' \
    >"$DATA_DIR/pki/identities/agent/active.json"
printf '%s' '{"version":1,"generation":"generation-1","credential":{"certificate_id":"certificate-1"},"pki_domain_id":"domain-1","expectation":{"agent_id":"agent-1"}}' \
    >"$DATA_DIR/pki/identities/agent/generations/generation-1/manifest.json"
printf '%s' '{"pki_domain_id":"domain-1","pki_epoch":1,"security_revision":0,"full":true,"certificate_id":"certificate-1"}' \
    >"$DATA_DIR/pki/security/ack.json"
AGENT_ID=""
AGENT_TOKEN=""
PKI_DOMAIN_ID=""
AGENT_CONTROL_TOKEN_PERSISTED="0"
load_migration_recovery_env_if_present "$ENV_FILE"
prepare_tunnel_enrollment
assert_eq "migration retry restored stable agent id" "$AGENT_ID" "agent-1"
assert_eq "migration retry restored stable PKI domain" "$PKI_DOMAIN_ID" "domain-1"
assert_eq "migration retry recognized active credential" "$PKI_ACTIVE_REGISTRATION_PRESENT" "1"
if [ -e "$DATA_DIR/pki/identities/agent/pending" ]; then
    printf 'migration retry created a second anonymous pending enrollment\n' >&2
    exit 1
fi
: >"$tmp/active-register-called"
curl() {
    printf 'called\n' >"$tmp/active-register-called"
    exit 1
}
register_agent >"$tmp/active-register-replay.out"
if [ -s "$tmp/active-register-called" ]; then
    printf 'migration retry called register after active credential recovery\n' >&2
    exit 1
fi
rm -f "$tmp/active-register-replay.out" "$tmp/active-register-called"

# A forced bound re-enrollment journals a proposal without replacing the active
# token in agent.env. If the server token is still valid, the backend preserves
# and returns it; request failure and restart must replay the exact proposal.
FORCE_PKI_REENROLL="1"
AGENT_ID=""
REQUESTED_AGENT_TOKEN="operator-proposed-control-token"
AGENT_TOKEN="$REQUESTED_AGENT_TOKEN"
PKI_DOMAIN_ID=""
AGENT_CONTROL_TOKEN_PERSISTED="0"
load_migration_recovery_env_if_present "$ENV_FILE"
prepare_tunnel_enrollment >"$tmp/active-force-reenrollment.out"
assert_eq "forced re-enrollment keeps stable agent id" "$AGENT_ID" "agent-1"
assert_eq "forced re-enrollment keeps stable PKI domain" "$PKI_DOMAIN_ID" "domain-1"
assert_eq "forced re-enrollment requires registration" "$PKI_REENROLLMENT_REQUIRED" "1"
assert_eq "forced re-enrollment does not reuse active registration" "$PKI_ACTIVE_REGISTRATION_PRESENT" "0"
assert_eq "forced re-enrollment pending owner" "$PKI_ENROLLMENT_AGENT_ID" "agent-1"
assert_eq "forced re-enrollment pending domain" "$PKI_ENROLLMENT_DOMAIN_ID" "domain-1"
assert_eq "forced re-enrollment keeps active control token" "$AGENT_TOKEN" "control-secret"
pending_token_file="$DATA_DIR/.join-state/pending-control-token.json"
prepare_registration_control_token
proposed_control_token="$REGISTRATION_AGENT_TOKEN"
assert_eq "forced re-enrollment uses isolated proposal" "$proposed_control_token" "$REQUESTED_AGENT_TOKEN"
if [ ! -s "$pending_token_file" ]; then
    printf 'forced re-enrollment did not journal its proposed control token\n' >&2
    exit 1
fi
pending_token_mode="$(stat -c '%a' "$pending_token_file" 2>/dev/null || stat -f '%Lp' "$pending_token_file")"
assert_eq "forced pending token mode" "$pending_token_mode" "600"
write_agent_env "$ENV_FILE"
if ! grep -Fq "NRE_AGENT_TOKEN='control-secret'" "$ENV_FILE" || grep -Fq "$REQUESTED_AGENT_TOKEN" "$ENV_FILE"; then
    printf 'pre-registration env write replaced the active control token\n' >&2
    exit 1
fi

failed_registration_token="$tmp/failed-registration-token"
curl() {
    for curl_arg do
        case "$curl_arg" in
            "X-Agent-Token: "*) printf '%s' "${curl_arg#X-Agent-Token: }" >"$failed_registration_token" ;;
        esac
    done
    return 22
}
if (register_agent >/dev/null 2>&1); then
    printf 'injected pre-commit registration failure unexpectedly succeeded\n' >&2
    exit 1
fi
assert_eq "failed registration used proposed token" "$(cat "$failed_registration_token")" "$proposed_control_token"
if ! grep -Fq "NRE_AGENT_TOKEN='control-secret'" "$ENV_FILE"; then
    printf 'failed registration destroyed the active token in agent.env\n' >&2
    exit 1
fi

# Simulate a fresh process after the failed request. The durable active token
# remains the runtime credential while the proposal is recovered for replay.
AGENT_ID=""
AGENT_TOKEN=""
PKI_DOMAIN_ID=""
AGENT_CONTROL_TOKEN_PERSISTED="0"
REGISTRATION_AGENT_TOKEN=""
load_migration_recovery_env_if_present "$ENV_FILE"
prepare_tunnel_enrollment >/dev/null
prepare_registration_control_token
assert_eq "failed registration restart kept active token" "$AGENT_TOKEN" "control-secret"
assert_eq "failed registration restart replayed proposal" "$REGISTRATION_AGENT_TOKEN" "$proposed_control_token"

REGISTER_RESPONSE="$registration_replay_response"
stage_pki_registration_response >/dev/null
if ! grep -Fq "NRE_AGENT_TOKEN='control-secret'" "$ENV_FILE"; then
    printf 'successful replay did not retain the authoritative stable token\n' >&2
    exit 1
fi
if [ -e "$pending_token_file" ]; then
    printf 'successful stable-token replay left pending join state behind\n' >&2
    exit 1
fi

# The server may revoke/clear the control token while an offline agent receives
# no renewal marker. Forced re-enrollment must still send a fresh proposal; when
# the transaction returns it, the response becomes the new durable credential.
rm -rf "$DATA_DIR/pki/identities/agent/pending"
rm -f "$DATA_DIR/pki/identities/agent/renewal.json"
AGENT_TOKEN="control-secret"
REGISTRATION_AGENT_TOKEN=""
REQUESTED_AGENT_TOKEN="offline-revocation-control-token"
AGENT_CONTROL_TOKEN_PERSISTED="1"
write_agent_env "$ENV_FILE"
prepare_tunnel_enrollment >/dev/null
prepare_registration_control_token
assert_eq "offline revocation uses isolated proposal without marker" "$REGISTRATION_AGENT_TOKEN" "$REQUESTED_AGENT_TOKEN"
REGISTER_RESPONSE="$(printf '%s' "$registration_replay_response" | sed "s/control-secret/$REGISTRATION_AGENT_TOKEN/")"
stage_pki_registration_response >/dev/null
if ! grep -Fq "NRE_AGENT_TOKEN='offline-revocation-control-token'" "$ENV_FILE"; then
    printf 'offline revocation did not promote the authoritative replacement token\n' >&2
    exit 1
fi
if [ -e "$pending_token_file" ]; then
    printf 'offline revocation left its committed proposal journal behind\n' >&2
    exit 1
fi

# Inject a crash after response publication but before the shell-only proposal
# journal is removed. Go publishes the new active generation while pending/
# still exists, the shell observes that interleaving, and only then Go removes
# pending/. A later forced enrollment must create a fresh request without being
# blocked by the already-activated journal.
rm -rf "$DATA_DIR/pki/identities/agent/pending"
REGISTRATION_AGENT_TOKEN=""
REQUESTED_AGENT_TOKEN="crash-window-control-token"
prepare_tunnel_enrollment >/dev/null
prepare_registration_control_token
crash_window_request_id="$PKI_ENROLLMENT_REQUEST_ID"
REGISTER_RESPONSE="$(printf '%s' "$registration_replay_response" | sed 's/control-secret/offline-revocation-control-token/')"
(
    clear_pending_registration_control_token() { return 0; }
    stage_pki_registration_response >/dev/null
)
if [ ! -s "$pending_token_file" ] || [ ! -s "$DATA_DIR/pki/identities/agent/pending/response.json" ]; then
    printf 'crash-window fixture did not retain both response and proposal journal\n' >&2
    exit 1
fi
crash_window_generation="generation-crash-window"
mkdir -p "$DATA_DIR/pki/identities/agent/generations/$crash_window_generation"
printf '{"version":1,"generation":%s,"request_id":%s,"credential":{"certificate_id":"certificate-1"},"pki_domain_id":"domain-1","expectation":{"agent_id":"agent-1"}}' \
    "$(json_string "$crash_window_generation")" "$(json_string "$crash_window_request_id")" \
    >"$DATA_DIR/pki/identities/agent/generations/$crash_window_generation/manifest.json"
printf '{"version":1,"generation":%s,"manifest_hash":"hash","activated_at":"2026-08-02T00:00:00Z"}' \
    "$(json_string "$crash_window_generation")" \
    >"$DATA_DIR/pki/identities/agent/active.json"
AGENT_ID=""
AGENT_TOKEN=""
PKI_DOMAIN_ID=""
REGISTRATION_AGENT_TOKEN=""
AGENT_CONTROL_TOKEN_PERSISTED="0"
load_migration_recovery_env_if_present "$ENV_FILE"
if load_active_registration_if_present; then
    printf 'forced crash-window active load unexpectedly reused registration\n' >&2
    exit 1
fi
if [ ! -e "$DATA_DIR/pki/identities/agent/pending" ]; then
    printf 'crash-window active check did not occur before pending tombstone\n' >&2
    exit 1
fi
if [ -e "$pending_token_file" ]; then
    printf 'active request did not reconcile its proposal journal while pending still existed\n' >&2
    exit 1
fi

# Complete Go's post-pointer pending tombstone after the shell active check,
# then begin the next forced enrollment lifecycle.
rm -rf "$DATA_DIR/pki/identities/agent/pending"
AGENT_ID=""
AGENT_TOKEN=""
PKI_DOMAIN_ID=""
REGISTRATION_AGENT_TOKEN=""
REQUESTED_AGENT_TOKEN="post-crash-control-token"
AGENT_CONTROL_TOKEN_PERSISTED="0"
load_migration_recovery_env_if_present "$ENV_FILE"
prepare_tunnel_enrollment >/dev/null
if [ "$PKI_ENROLLMENT_REQUEST_ID" = "$crash_window_request_id" ]; then
    printf 'forced enrollment reused the already activated request ID\n' >&2
    exit 1
fi
prepare_registration_control_token
assert_eq "post-crash forced enrollment uses new proposal" "$REGISTRATION_AGENT_TOKEN" "$REQUESTED_AGENT_TOKEN"

# Cover the reverse interleaving: the shell reads the old active generation,
# then Go publishes this request and tombstones pending/ before the same shell
# invocation reaches new enrollment creation. The request-use boundary must
# refresh active state and replace only the journal proven to be activated.
reverse_window_request_id="$PKI_ENROLLMENT_REQUEST_ID"
REGISTER_RESPONSE="$(printf '%s' "$registration_replay_response" | sed 's/control-secret/offline-revocation-control-token/')"
(
    clear_pending_registration_control_token() { return 0; }
    stage_pki_registration_response >/dev/null
)
if [ ! -s "$pending_token_file" ] || [ ! -s "$DATA_DIR/pki/identities/agent/pending/response.json" ]; then
    printf 'reverse crash-window fixture did not retain both response and proposal journal\n' >&2
    exit 1
fi
reverse_window_generation="generation-reverse-crash-window"
mkdir -p "$DATA_DIR/pki/identities/agent/generations/$reverse_window_generation"
printf '{"version":1,"generation":%s,"request_id":%s,"credential":{"certificate_id":"certificate-1"},"pki_domain_id":"domain-1","expectation":{"agent_id":"agent-1"}}' \
    "$(json_string "$reverse_window_generation")" "$(json_string "$reverse_window_request_id")" \
    >"$DATA_DIR/pki/identities/agent/generations/$reverse_window_generation/manifest.json"

reverse_active_load_calls=0
load_active_registration_if_present() {
    if load_active_registration_if_present_impl; then
        reverse_active_load_status=0
    else
        reverse_active_load_status=$?
    fi
    reverse_active_load_calls=$((reverse_active_load_calls + 1))
    if [ "$reverse_active_load_calls" -eq 1 ]; then
        printf '{"version":1,"generation":%s,"manifest_hash":"hash","activated_at":"2026-08-02T00:00:00Z"}' \
            "$(json_string "$reverse_window_generation")" \
            >"$DATA_DIR/pki/identities/agent/active.json"
        rm -rf "$DATA_DIR/pki/identities/agent/pending"
    fi
    return "$reverse_active_load_status"
}
AGENT_ID=""
AGENT_TOKEN=""
PKI_DOMAIN_ID=""
REGISTRATION_AGENT_TOKEN=""
REQUESTED_AGENT_TOKEN="reverse-post-crash-control-token"
AGENT_CONTROL_TOKEN_PERSISTED="0"
load_migration_recovery_env_if_present "$ENV_FILE"
prepare_tunnel_enrollment >/dev/null
load_active_registration_if_present() {
    load_active_registration_if_present_impl
}
if [ "$PKI_ENROLLMENT_REQUEST_ID" = "$reverse_window_request_id" ]; then
    printf 'reverse crash-window reused the already activated request ID\n' >&2
    exit 1
fi
reverse_retained_journal="$(tr -d '\r\n' < "$pending_token_file")"
assert_eq "reverse crash-window retains journal until active refresh" \
    "$(extract_json_string "$reverse_retained_journal" request_id)" "$reverse_window_request_id"
prepare_registration_control_token
assert_eq "reverse crash-window refreshes active before replacing proposal" \
    "$REGISTRATION_AGENT_TOKEN" "$REQUESTED_AGENT_TOKEN"
reverse_replacement_journal="$(tr -d '\r\n' < "$pending_token_file")"
assert_eq "reverse crash-window journal follows fresh request" \
    "$(extract_json_string "$reverse_replacement_journal" request_id)" "$PKI_ENROLLMENT_REQUEST_ID"
clear_pending_registration_control_token

# Restore the active fixture so the separate renewal-marker case starts from
# the same last-known-good state.
rm -rf "$DATA_DIR/pki/identities/agent/pending"
AGENT_TOKEN="control-secret"
REGISTRATION_AGENT_TOKEN=""
REQUESTED_AGENT_TOKEN=""
AGENT_CONTROL_TOKEN_PERSISTED="1"
write_agent_env "$ENV_FILE"
rm -f "$tmp/active-force-reenrollment.out" "$failed_registration_token"
FORCE_PKI_REENROLL="0"

# A typed revocation/owner mismatch is persisted by the Go agent as an explicit
# trust-reset fence. The join path must retain the stable owner but create a new
# local key/CSR for one-time-token registration instead of silently reusing the
# revoked active credential.
printf '%s' '{"version":1,"credential_identity":"identity-1","credential_fingerprint_sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","due_at":"2026-08-02T00:00:00Z","failure_count":0,"reenrollment_required":true,"reason":"revoked_identity","updated_at":"2026-08-02T00:00:00Z"}' \
    >"$DATA_DIR/pki/identities/agent/renewal.json"
AGENT_ID=""
AGENT_TOKEN=""
REQUESTED_AGENT_TOKEN="revoked-rotated-control-token"
PKI_DOMAIN_ID=""
AGENT_CONTROL_TOKEN_PERSISTED="0"
load_migration_recovery_env_if_present "$ENV_FILE"
prepare_tunnel_enrollment >"$tmp/active-reenrollment.out"
assert_eq "revoked migration keeps stable agent id" "$AGENT_ID" "agent-1"
assert_eq "revoked migration keeps stable PKI domain" "$PKI_DOMAIN_ID" "domain-1"
assert_eq "revoked migration requires new registration" "$PKI_REENROLLMENT_REQUIRED" "1"
assert_eq "revoked migration does not reuse active registration" "$PKI_ACTIVE_REGISTRATION_PRESENT" "0"
assert_eq "revoked migration pending owner" "$PKI_ENROLLMENT_AGENT_ID" "agent-1"
assert_eq "revoked migration pending domain" "$PKI_ENROLLMENT_DOMAIN_ID" "domain-1"
assert_eq "revoked migration keeps active control token" "$AGENT_TOKEN" "control-secret"
prepare_registration_control_token
if [ -z "$REGISTRATION_AGENT_TOKEN" ] || [ "$REGISTRATION_AGENT_TOKEN" = "$AGENT_TOKEN" ]; then
    printf 'revoked migration did not persist a separate proposed control token\n' >&2
    exit 1
fi
assert_eq "revoked migration uses explicit replacement token" "$REGISTRATION_AGENT_TOKEN" "$REQUESTED_AGENT_TOKEN"
if [ ! -s "$pending_token_file" ]; then
    printf 'revoked migration did not journal its replacement control token\n' >&2
    exit 1
fi
pending_token_mode="$(stat -c '%a' "$pending_token_file" 2>/dev/null || stat -f '%Lp' "$pending_token_file")"
assert_eq "revoked pending token mode" "$pending_token_mode" "600"
if [ ! -s "$DATA_DIR/pki/identities/agent/pending/private-key.pem" ] || \
    [ ! -s "$DATA_DIR/pki/identities/agent/pending/request.csr.pem" ]; then
    printf 'revoked migration did not create a replacement pending enrollment\n' >&2
    exit 1
fi
REGISTER_RESPONSE="$(printf '%s' "$registration_replay_response" | sed "s/control-secret/$REGISTRATION_AGENT_TOKEN/")"
stage_pki_registration_response >/dev/null
if ! grep -Fq "NRE_AGENT_TOKEN='revoked-rotated-control-token'" "$ENV_FILE"; then
    printf 'revoked migration did not atomically promote the replacement token\n' >&2
    exit 1
fi
if [ -e "$pending_token_file" ]; then
    printf 'revoked migration left its committed replacement token journal behind\n' >&2
    exit 1
fi
rm -f "$tmp/active-reenrollment.out"

DATA_DIR="$shell_data_dir"
ENV_FILE="$DATA_DIR/agent.env"

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
