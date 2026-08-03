#!/bin/sh
set -eu
umask 077

DEFAULT_MASTER_URL="__DEFAULT_MASTER_URL__"
DEFAULT_ASSET_BASE_URL="__DEFAULT_ASSET_BASE_URL__"
UNSET_MASTER_SENTINEL="__JOIN_AGENT_DEFAULT_MASTER_URL__"
UNSET_ASSET_BASE_URL_SENTINEL="__JOIN_AGENT_DEFAULT_ASSET_BASE_URL__"

[ "$DEFAULT_MASTER_URL" = "$UNSET_MASTER_SENTINEL" ] && DEFAULT_MASTER_URL=""
[ "$DEFAULT_ASSET_BASE_URL" = "$UNSET_ASSET_BASE_URL_SENTINEL" ] && DEFAULT_ASSET_BASE_URL=""
case "$DEFAULT_MASTER_URL" in __DEFAULT_*__) DEFAULT_MASTER_URL="" ;; esac
case "$DEFAULT_ASSET_BASE_URL" in __DEFAULT_*__) DEFAULT_ASSET_BASE_URL="" ;; esac

usage() {
    cat <<EOF
Usage:
  join-agent.sh --register-token TOKEN [options]
  join-agent.sh migrate-from-main --register-token TOKEN [options]
  join-agent.sh uninstall-agent [options]

Commands:
  migrate-from-main       Migrate a legacy lightweight Agent node to go-agent
  uninstall-agent         Remove the local Agent runtime from this host

Required:
  --register-token TOKEN   Master registration token

Optional:
  --master-url URL         Master control-plane URL (default: embedded panel URL)
  --asset-base-url URL     Control-plane asset base URL (default: embedded asset URL)
  --agent-name NAME        Agent name, default: current hostname
  --agent-token TOKEN      Agent heartbeat token, default: auto-generated
  --agent-url URL          Optional public URL for display / direct access
  --data-dir DIR           Install directory, default: /var/lib/nre-agent
  --version VERSION        Agent version sent during registration, default: 1
  --tags TAGS              Comma-separated tags, e.g. edge,emby
  --binary-url URL         Download URL override for the nre-agent binary
  --manifest-url URL       Manifest URL override (requires --binary-url)
  --force-pki-reenroll     Re-enroll tunnel; rotate control token only after server-side revocation
  --install-systemd        Install and start a systemd service (Linux)
  --install-launchd        Install and load a launchd agent (macOS)
  --source-dir DIR         Legacy lightweight Agent directory for migrate-from-main or uninstall-agent
  -h, --help               Show help

Examples:
  curl -fsSL ${DEFAULT_MASTER_URL:-http://master.example.com:3000}/panel-api/public/join-agent.sh | sh -s -- --register-token change-this-register-token --install-systemd
  join-agent.sh --master-url http://master.example.com:3000 --register-token change-this-register-token --install-systemd
  join-agent.sh --register-token change-this-register-token --install-launchd
  join-agent.sh migrate-from-main --master-url http://master.example.com:3000 --register-token change-this-register-token
  join-agent.sh uninstall-agent --data-dir /var/lib/nre-agent
EOF
}

trim_slash() {
    printf '%s' "$1" | sed 's#/*$##'
}

normalize_master_url() {
    value="$(trim_slash "$1")"
    value="$(printf '%s' "$value" | sed 's#/panel-api/public/join-agent\.sh$##')"
    value="$(printf '%s' "$value" | sed 's#/panel-api$##')"
    printf '%s' "$value"
}

is_valid_master_url() {
    printf '%s' "$1" | grep -Eq '^https?://[^/]+$'
}

shell_quote() {
    printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\''/g")"
}

is_windows_shell() {
    case "$(uname -s 2>/dev/null || true)" in
        MINGW*|MSYS*|CYGWIN*) return 0 ;;
        *) return 1 ;;
    esac
}

# chmod under Git Bash does not remove inherited NTFS ACEs. Keep the shell
# artifact contract identical to the Go Store's Windows restrictPath SDDL so
# the production agent can consume pending enrollment state without a
# test-only ACL rewrite.
restrict_private_path() {
    private_path="$1"
    private_kind="$2"
    case "$private_kind" in
        directory)
            chmod 700 "$private_path"
            private_directory=1
            ;;
        file)
            chmod 600 "$private_path"
            private_directory=0
            ;;
        *)
            echo "Invalid private path kind: $private_kind" >&2
            exit 1
            ;;
    esac
    is_windows_shell || return 0

    command -v powershell.exe >/dev/null 2>&1 || {
        echo "powershell.exe is required to secure PKI state on Windows" >&2
        exit 1
    }
    if command -v cygpath >/dev/null 2>&1; then
        private_windows_path="$(cygpath -w "$private_path")"
    else
        private_windows_path="$private_path"
    fi
    NRE_PRIVATE_ACL_PATH="$private_windows_path" \
    NRE_PRIVATE_ACL_DIRECTORY="$private_directory" \
    MSYS2_ARG_CONV_EXCL='*' \
    powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command '
        $ErrorActionPreference = "Stop"
        $path = $env:NRE_PRIVATE_ACL_PATH
        $directory = $env:NRE_PRIVATE_ACL_DIRECTORY -eq "1"
        $sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
        $inheritance = if ($directory) { "OICI" } else { "" }
        $sddl = "D:P(A;$inheritance;GA;;;$sid)(A;$inheritance;GA;;;SY)(A;$inheritance;GA;;;BA)"
        $sections = [System.Security.AccessControl.AccessControlSections]::Access
        if ($directory) {
            $acl = [System.IO.Directory]::GetAccessControl($path, $sections)
        } else {
            $acl = [System.IO.File]::GetAccessControl($path, $sections)
        }
        $acl.SetSecurityDescriptorSddlForm($sddl, $sections)
        if ($directory) {
            [System.IO.Directory]::SetAccessControl($path, $acl)
        } else {
            [System.IO.File]::SetAccessControl($path, $acl)
        }
    ' >/dev/null
}

json_string() {
    escaped=$(printf '%s' "$1" | sed ':a;N;$!ba;s/\\/\\\\/g;s/"/\\"/g;s/\r/\\r/g;s/\n/\\n/g;s/\t/\\t/g')
    printf '"%s"' "$escaped"
}

extract_registered_agent_id() {
    printf '%s' "$1" | tr -d '\r\n' | sed -n 's/.*"agent"[[:space:]]*:[[:space:]]*{[^}]*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

extract_json_string() {
    printf '%s\n' "$1" | awk -v wanted="\"$2\"" '
        function hex_value(ch) {
            if (ch >= "0" && ch <= "9") return ch + 0
            ch = tolower(ch)
            if (ch >= "a" && ch <= "f") return index("abcdef", ch) + 9
            return -1
        }
        function decode_ascii_unicode(hex,    value, index_, digit) {
            if (length(hex) != 4) exit 1
            value = 0
            for (index_ = 1; index_ <= 4; index_++) {
                digit = hex_value(substr(hex, index_, 1))
                if (digit < 0) exit 1
                value = value * 16 + digit
            }
            # Agent control tokens are persisted in a POSIX env file. Reject
            # control and non-ASCII escapes instead of silently changing them.
            if (value < 32 || value > 126) exit 1
            return sprintf("%c", value)
        }
        {
            text = $0
            start = index(text, wanted)
            if (start == 0) exit 1
            start += length(wanted)
            while (start <= length(text) && substr(text, start, 1) != ":") start++
            start++
            while (start <= length(text) && substr(text, start, 1) ~ /[[:space:]]/) start++
            if (substr(text, start, 1) != "\"") exit 1
            result = ""
            for (i = start + 1; i <= length(text); i++) {
                ch = substr(text, i, 1)
                if (ch == "\"") {
                    printf "%s", result
                    exit 0
                }
                if (ch != "\\") {
                    result = result ch
                    continue
                }
                i++
                escaped = substr(text, i, 1)
                if (escaped == "\"" || escaped == "\\" || escaped == "/") result = result escaped
                else if (escaped == "b" || escaped == "f" || escaped == "n" || escaped == "r" || escaped == "t") exit 1
                else if (escaped == "u") {
                    result = result decode_ascii_unicode(substr(text, i + 1, 4))
                    i += 4
                } else exit 1
            }
            exit 1
        }
    '
}

extract_json_boolean() {
    printf '%s\n' "$1" | awk -v wanted="\"$2\"" '
        {
            text = $0
            start = index(text, wanted)
            if (start == 0) exit 1
            start += length(wanted)
            while (start <= length(text) && substr(text, start, 1) != ":") start++
            if (start > length(text)) exit 1
            start++
            while (start <= length(text) && substr(text, start, 1) ~ /[[:space:]]/) start++
            value = substr(text, start)
            if (value ~ /^true([^[:alpha:][:digit:]_]|$)/) {
                printf "true"
                exit 0
            }
            if (value ~ /^false([^[:alpha:][:digit:]_]|$)/) {
                printf "false"
                exit 0
            }
            exit 1
        }
    '
}

# Extract one JSON object without depending on jq/python. Braces inside JSON
# strings (including certificate PEM escapes) do not affect the depth counter.
extract_json_object() {
    printf '%s\n' "$1" | awk -v wanted="\"$2\"" '
        {
            text = $0
            start = index(text, wanted)
            if (start == 0) exit 1
            start += length(wanted)
            while (start <= length(text) && substr(text, start, 1) != ":") start++
            while (start <= length(text) && substr(text, start, 1) != "{") start++
            if (start > length(text)) exit 1
            depth = 0
            quoted = 0
            escaped = 0
            for (i = start; i <= length(text); i++) {
                ch = substr(text, i, 1)
                if (quoted) {
                    if (escaped) escaped = 0
                    else if (ch == "\\") escaped = 1
                    else if (ch == "\"") quoted = 0
                } else if (ch == "\"") quoted = 1
                else if (ch == "{") depth++
                else if (ch == "}") {
                    depth--
                    if (depth == 0) {
                        print substr(text, start, i - start + 1)
                        exit 0
                    }
                }
            }
            exit 1
        }
    '
}

generate_token() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 24
        return 0
    fi
    if command -v python3 >/dev/null 2>&1; then
        python3 - <<'PY'
import secrets
print(secrets.token_hex(24))
PY
        return 0
    fi
    echo "[ERROR] Cannot generate a cryptographically strong agent token: neither openssl nor python3 is available." >&2
    echo "Install openssl or python3, or provide --agent-token explicitly." >&2
    return 1
}

absolute_path() {
    target="$1"
    case "$target" in
        /*) printf '%s\n' "$target" ;;
        *)
            target_dir=$(dirname -- "$target")
            target_name=$(basename -- "$target")
            mkdir -p "$target_dir"
            target_dir_abs=$(CDPATH= cd -- "$target_dir" && pwd)
            printf '%s/%s\n' "$target_dir_abs" "$target_name"
            ;;
    esac
}

detect_platform() {
    uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]'
}

detect_arch() {
    arch=$(uname -m 2>/dev/null || printf 'unknown')
    case "$arch" in
        x86_64|amd64) printf 'amd64' ;;
        arm64|aarch64) printf 'arm64' ;;
        *) printf '%s' "$arch" ;;
    esac
}

xml_escape() {
    printf '%s' "$1" | sed \
        -e 's/&/\&amp;/g' \
        -e 's/</\&lt;/g' \
        -e 's/>/\&gt;/g' \
        -e "s/'/\&apos;/g" \
        -e 's/"/\&quot;/g'
}

require_root_or_sudo() {
    if [ "$(id -u)" -eq 0 ]; then
        printf '%s\n' ""
        return 0
    fi
    if command -v sudo >/dev/null 2>&1; then
        printf '%s\n' "sudo"
        return 0
    fi
    return 1
}

run_root_cmd() {
    if [ -n "${SUDO_BIN:-}" ]; then
        "$SUDO_BIN" "$@"
    else
        "$@"
    fi
}

persist_installed_join_script() {
    mkdir -p "$BIN_DIR"
    [ -n "$MASTER_URL" ] || {
        echo "Missing --master-url; cannot persist installed join-agent.sh" >&2
        exit 1
    }
    curl -fsSL --connect-timeout 15 --max-time 300 "$MASTER_URL/panel-api/public/join-agent.sh" -o "$JOIN_SCRIPT_PATH"
    chmod 755 "$JOIN_SCRIPT_PATH"
}

install_uninstall_wrapper() {
    if [ -z "${SUDO_BIN:-}" ] && [ "$(id -u)" -ne 0 ] && [ ! -w "$(dirname -- "$UNINSTALL_WRAPPER_PATH")" ]; then
        SUDO_BIN="$(require_root_or_sudo)" || {
            echo "Installing uninstall wrapper requires root or sudo" >&2
            exit 1
        }
    fi

    uninstall_source_arg=""
    if [ -n "${WRAPPER_SOURCE_DIR:-}" ]; then
        uninstall_source_arg=" --source-dir $(shell_quote "$SOURCE_DIR")"
    fi

    cat <<EOF | run_root_cmd tee "$UNINSTALL_WRAPPER_PATH" >/dev/null
#!/bin/sh
set -eu
exec $(shell_quote "$JOIN_SCRIPT_PATH") uninstall-agent --data-dir $(shell_quote "$DATA_DIR")$uninstall_source_arg
EOF
    run_root_cmd chmod 755 "$UNINSTALL_WRAPPER_PATH"
    echo "[JOIN] Installed uninstall command: $UNINSTALL_WRAPPER_PATH"
}

remove_uninstall_wrapper() {
    if [ ! -e "$UNINSTALL_WRAPPER_PATH" ]; then
        return 0
    fi
    if [ -z "${SUDO_BIN:-}" ] && [ "$(id -u)" -ne 0 ] && [ ! -w "$UNINSTALL_WRAPPER_PATH" ] && [ ! -w "$(dirname -- "$UNINSTALL_WRAPPER_PATH")" ]; then
        SUDO_BIN="$(require_root_or_sudo)" || {
            echo "Removing uninstall wrapper requires root or sudo" >&2
            exit 1
        }
    fi
    run_root_cmd rm -f "$UNINSTALL_WRAPPER_PATH"
}

service_exists() {
    systemctl status nginx-reverse-emby-agent.service >/dev/null 2>&1
}

service_is_active() {
    systemctl is-active --quiet nginx-reverse-emby-agent.service
}

resolve_script_dir() {
    script_path=${0:-}
    [ -n "$script_path" ] || return 1
    case "$script_path" in
        /*) dir=$(dirname -- "$script_path") ;;
        */*) dir=$(dirname -- "$script_path") ;;
        *) dir="." ;;
    esac
    CDPATH= cd -- "$dir" 2>/dev/null && pwd
}

manifest_json_value() {
    key="$1"
    file="$2"
    sed -n \
        -e "s/.*\"$key\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" \
        -e "s/.*\"$key\"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p" \
        "$file" | head -n 1
}

file_sha256() {
    file="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$file" | awk '{print $1}'
        return 0
    fi
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$file" | awk '{print $1}'
        return 0
    fi
    if command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 "$file" | sed 's/^.*= //'
        return 0
    fi
    echo "No SHA-256 utility is available (sha256sum, shasum, or openssl required)" >&2
    return 1
}

verify_binary_manifest() {
    binary_path="$1"
    manifest_path="$2"
    expected_filename="$3"
    expected_platform="$PLATFORM-$ARCH"

    [ -f "$manifest_path" ] || { echo "Package manifest missing: $manifest_path" >&2; return 1; }
    schema_version=$(manifest_json_value schema_version "$manifest_path")
    manifest_filename=$(manifest_json_value filename "$manifest_path")
    manifest_platform=$(manifest_json_value platform "$manifest_path")
    manifest_sha256=$(manifest_json_value sha256 "$manifest_path")
    manifest_size=$(manifest_json_value size "$manifest_path")

    [ "$schema_version" = "1" ] || { echo "Unsupported package manifest schema: $schema_version" >&2; return 1; }
    [ "$manifest_filename" = "$expected_filename" ] || { echo "Package manifest filename mismatch" >&2; return 1; }
    [ "$manifest_platform" = "$expected_platform" ] || { echo "Package manifest platform mismatch: $manifest_platform" >&2; return 1; }
    case "$manifest_size" in ''|*[!0-9]*) echo "Package manifest size is invalid" >&2; return 1 ;; esac
    [ "$manifest_size" -gt 0 ] || { echo "Package manifest size must be positive" >&2; return 1; }
    case "$manifest_sha256" in
        *[!0-9a-fA-F]*|'') echo "Package manifest SHA-256 is invalid" >&2; return 1 ;;
    esac
    [ "${#manifest_sha256}" -eq 64 ] || { echo "Package manifest SHA-256 length is invalid" >&2; return 1; }

    actual_size=$(wc -c < "$binary_path" | tr -d '[:space:]')
    [ "$actual_size" = "$manifest_size" ] || { echo "Package size mismatch" >&2; return 1; }
    actual_sha256=$(file_sha256 "$binary_path") || return 1
    [ "$(printf '%s' "$actual_sha256" | tr '[:upper:]' '[:lower:]')" = "$(printf '%s' "$manifest_sha256" | tr '[:upper:]' '[:lower:]')" ] || {
        echo "Package SHA-256 mismatch" >&2
        return 1
    }
}

companion_manifest_url() {
    value="$1"
    fragment=""
    query=""
    case "$value" in
        *\#*) fragment="#${value#*\#}"; value="${value%%\#*}" ;;
    esac
    case "$value" in
        *\?*) query="?${value#*\?}"; value="${value%%\?*}" ;;
    esac
    printf '%s.manifest.json%s%s\n' "$value" "$query" "$fragment"
}

copy_or_download_binary() {
    asset_name="$1"
    dest_path="$2"
    local_path=""

    if [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/../panel/public/agent-assets/$asset_name" ]; then
        local_path="$SCRIPT_DIR/../panel/public/agent-assets/$asset_name"
    elif [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/$asset_name" ]; then
        local_path="$SCRIPT_DIR/$asset_name"
    fi

    mkdir -p "$(dirname -- "$dest_path")"

    if [ -n "$local_path" ] && [ -f "$local_path" ]; then
        [ -f "$local_path.manifest.json" ] || { echo "Package manifest missing: $local_path.manifest.json" >&2; exit 1; }
        cp "$local_path" "$dest_path"
        cp "$local_path.manifest.json" "$dest_path.manifest.json"
    elif [ -n "$BINARY_URL" ]; then
        echo "[JOIN] Downloading nre-agent from $BINARY_URL ..." >&2
        curl -fsSL --connect-timeout 15 --max-time 1800 "$BINARY_URL" -o "$dest_path"
        download_manifest_url="$MANIFEST_URL"
        if [ -z "$download_manifest_url" ]; then
            download_manifest_url="$(companion_manifest_url "$BINARY_URL")"
        fi
        curl -fsSL --connect-timeout 15 --max-time 1800 "$download_manifest_url" -o "$dest_path.manifest.json"
    else
        [ -n "$ASSET_BASE_URL" ] || {
            echo "Missing nre-agent binary source. Re-run with --asset-base-url URL or --binary-url URL." >&2
            exit 1
        }

        echo "[JOIN] Downloading $asset_name from $ASSET_BASE_URL ..." >&2
        curl -fsSL --connect-timeout 15 --max-time 1800 "$ASSET_BASE_URL/$asset_name" -o "$dest_path"
        curl -fsSL --connect-timeout 15 --max-time 1800 "$ASSET_BASE_URL/$asset_name.manifest.json" -o "$dest_path.manifest.json"
    fi
    if ! verify_binary_manifest "$dest_path" "$dest_path.manifest.json" "$asset_name"; then
        rm -f "$dest_path" "$dest_path.manifest.json"
        exit 1
    fi
    chmod 755 "$dest_path"
    chmod 644 "$dest_path.manifest.json"
}

build_tags_json() {
    if [ -z "$1" ]; then
        printf '[]'
        return 0
    fi

    old_ifs=$IFS
    IFS=,
    set -- $1
    IFS=$old_ifs

    first=1
    printf '['
    for tag in "$@"; do
        trimmed=$(printf '%s' "$tag" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
        [ -n "$trimmed" ] || continue
        if [ "$first" -eq 0 ]; then
            printf ','
        fi
        json_string "$trimmed"
        first=0
    done
    printf ']'
}

build_capabilities_json() {
    if [ -z "$1" ]; then
        printf '["http_rules","l4","cert_install"]'
        return 0
    fi

    old_ifs=$IFS
    IFS=,
    set -- $1
    IFS=$old_ifs

    first=1
    printf '['
    for capability in "$@"; do
        trimmed=$(printf '%s' "$capability" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
        [ -n "$trimmed" ] || continue
        if [ "$first" -eq 0 ]; then
            printf ','
        fi
        json_string "$trimmed"
        first=0
    done
    if [ "$first" -eq 1 ]; then
        printf '"http_rules","l4","cert_install"'
    fi
    printf ']'
}

build_register_payload() {
    tags_json=$(build_tags_json "$AGENT_TAGS")
    capabilities_json=$(build_capabilities_json "$AGENT_CAPABILITIES")
    payload_agent_token="${REGISTRATION_AGENT_TOKEN:-$AGENT_TOKEN}"
    payload_agent_id="$AGENT_ID"
    if [ "${PKI_ENROLLMENT_CONTEXT_READY:-0}" = "1" ]; then
        payload_agent_id="$PKI_ENROLLMENT_AGENT_ID"
    fi
    printf '{'
    printf '"agent_id":%s,' "$(json_string "$payload_agent_id")"
    printf '"name":%s,' "$(json_string "$AGENT_NAME")"
    printf '"agent_url":%s,' "$(json_string "$AGENT_URL")"
    printf '"agent_token":%s,' "$(json_string "$payload_agent_token")"
    printf '"version":%s,' "$(json_string "$AGENT_VERSION")"
    printf '"platform":%s,' "$(json_string "$PLATFORM-$ARCH")"
    printf '"tags":%s,' "$tags_json"
    printf '"capabilities":%s,' "$capabilities_json"
    printf '"mode":"pull",'
    printf '"pki_enrollment_request_id":%s,' "$(json_string "$PKI_ENROLLMENT_REQUEST_ID")"
    printf '"tunnel_csr_pem":%s,' "$(json_string "$PKI_TUNNEL_CSR_PEM")"
    if [ -n "$PKI_SECURITY_ACK_JSON" ]; then
        printf '"pki_security_ack":%s,' "$PKI_SECURITY_ACK_JSON"
    fi
    printf '"register_token":%s' "$(json_string "$REGISTER_TOKEN")"
    printf '}'
}

write_agent_env() {
    env_file="$1"
    env_tmp="$env_file.tmp.$$"
    cat > "$env_tmp" <<EOF
NRE_MASTER_URL=$(shell_quote "$MASTER_URL")
NRE_AGENT_ID=$(shell_quote "$AGENT_ID")
NRE_AGENT_NAME=$(shell_quote "$AGENT_NAME")
NRE_AGENT_TOKEN=$(shell_quote "$AGENT_TOKEN")
NRE_AGENT_URL=$(shell_quote "$AGENT_URL")
NRE_AGENT_VERSION=$(shell_quote "$AGENT_VERSION")
NRE_AGENT_TAGS=$(shell_quote "$AGENT_TAGS")
NRE_AGENT_CAPABILITIES=$(shell_quote "$AGENT_CAPABILITIES")
NRE_DATA_DIR=$(shell_quote "$DATA_DIR")
NRE_PKI_DOMAIN_ID=$(shell_quote "$PKI_DOMAIN_ID")
EOF
    restrict_private_path "$env_tmp" file
    mv "$env_tmp" "$env_file"
    restrict_private_path "$env_file" file
}

clear_pending_registration_control_token() {
    pending_token_root="$DATA_DIR/.join-state"
    pending_token_file="$pending_token_root/pending-control-token.json"
    if [ -e "$pending_token_file" ]; then
        [ -f "$pending_token_file" ] && [ ! -L "$pending_token_file" ] || {
            echo "Stored pending control token is unsafe" >&2
            exit 1
        }
        rm -f "$pending_token_file"
    fi
    if [ -e "$pending_token_root" ]; then
        [ -d "$pending_token_root" ] && [ ! -L "$pending_token_root" ] || {
            echo "Stored join state directory is unsafe" >&2
            exit 1
        }
        rmdir "$pending_token_root" 2>/dev/null || true
    fi
}

prepare_registration_control_token() {
    REGISTRATION_AGENT_TOKEN="$AGENT_TOKEN"
    if [ "${PKI_ACTIVE_REGISTRATION_PRESENT:-0}" = "1" ] || \
       [ "${PKI_STAGED_REGISTRATION_PRESENT:-0}" = "1" ] || \
       [ "${PKI_REENROLLMENT_REQUIRED:-0}" != "1" ]; then
        return 0
    fi
    [ "${PKI_ENROLLMENT_CONTEXT_READY:-0}" = "1" ] && \
        [ -n "$PKI_ENROLLMENT_REQUEST_ID" ] && \
        [ -n "$PKI_ENROLLMENT_AGENT_ID" ] && \
        [ -n "$PKI_ENROLLMENT_DOMAIN_ID" ] || {
        echo "Bound tunnel re-enrollment is missing its durable request context" >&2
        exit 1
    }

    pending_token_root="$DATA_DIR/.join-state"
    pending_token_file="$pending_token_root/pending-control-token.json"
    if [ -e "$pending_token_root" ]; then
        [ -d "$pending_token_root" ] && [ ! -L "$pending_token_root" ] || {
            echo "Stored join state directory is unsafe" >&2
            exit 1
        }
    else
        mkdir -p "$pending_token_root"
    fi
    restrict_private_path "$pending_token_root" directory

    if [ -e "$pending_token_file" ]; then
        [ -f "$pending_token_file" ] && [ ! -L "$pending_token_file" ] || {
            echo "Stored pending control token is unsafe" >&2
            exit 1
        }
        restrict_private_path "$pending_token_file" file
        pending_token_json="$(tr -d '\r\n' < "$pending_token_file")"
        pending_token_request_id="$(extract_json_string "$pending_token_json" request_id)" || {
            echo "Stored pending control token request is invalid" >&2
            exit 1
        }
        pending_token_agent_id="$(extract_json_string "$pending_token_json" agent_id)" || {
            echo "Stored pending control token owner is invalid" >&2
            exit 1
        }
        pending_token_domain_id="$(extract_json_string "$pending_token_json" pki_domain_id)" || {
            echo "Stored pending control token domain is invalid" >&2
            exit 1
        }
        proposed_agent_token="$(extract_json_string "$pending_token_json" proposed_agent_token)" || {
            echo "Stored pending control token is invalid" >&2
            exit 1
        }
        [ "$pending_token_request_id" = "$PKI_ENROLLMENT_REQUEST_ID" ] && \
            [ "$pending_token_agent_id" = "$PKI_ENROLLMENT_AGENT_ID" ] && \
            [ "$pending_token_domain_id" = "$PKI_ENROLLMENT_DOMAIN_ID" ] || {
            echo "Stored pending control token belongs to a different enrollment request" >&2
            exit 1
        }
        [ -n "$proposed_agent_token" ] && [ "${#proposed_agent_token}" -le 512 ] || {
            echo "Stored pending control token has an invalid length" >&2
            exit 1
        }
    else
        if [ -n "${REQUESTED_AGENT_TOKEN:-}" ] && [ "$REQUESTED_AGENT_TOKEN" != "$AGENT_TOKEN" ]; then
            proposed_agent_token="$REQUESTED_AGENT_TOKEN"
        else
            proposed_agent_token="$(generate_token)"
        fi
        pending_token_tmp="$pending_token_file.tmp.$$"
        rm -f "$pending_token_tmp"
        printf '{"request_id":%s,"agent_id":%s,"pki_domain_id":%s,"proposed_agent_token":%s}' \
            "$(json_string "$PKI_ENROLLMENT_REQUEST_ID")" \
            "$(json_string "$PKI_ENROLLMENT_AGENT_ID")" \
            "$(json_string "$PKI_ENROLLMENT_DOMAIN_ID")" \
            "$(json_string "$proposed_agent_token")" > "$pending_token_tmp"
        restrict_private_path "$pending_token_tmp" file
        mv "$pending_token_tmp" "$pending_token_file"
        restrict_private_path "$pending_token_file" file
    fi
    REGISTRATION_AGENT_TOKEN="$proposed_agent_token"
}

load_security_ack_if_present() {
    PKI_SECURITY_ACK_JSON=""
    ack_file="$DATA_DIR/pki/security/ack.json"
    [ -f "$ack_file" ] || return 0
    ack_json="$(tr -d '\r\n' < "$ack_file")"
    if printf '%s' "$ack_json" | grep -Eiq 'token|secret|private[_-]?key|password'; then
        echo "Ignoring unsafe local PKI security acknowledgement" >&2
        return 0
    fi
    case "$ack_json" in
        \{*\}) PKI_SECURITY_ACK_JSON="$ack_json" ;;
        *) echo "Ignoring invalid local PKI security acknowledgement" >&2 ;;
    esac
}

load_active_registration_if_present() {
    active_file="$DATA_DIR/pki/identities/agent/active.json"
    [ -e "$active_file" ] || return 1
    [ -f "$active_file" ] && [ ! -L "$active_file" ] || {
        echo "Stored active tunnel credential pointer is unsafe" >&2
        exit 1
    }
    active_json="$(tr -d '\r\n' < "$active_file")"
    active_generation="$(extract_json_string "$active_json" generation)" || {
        echo "Stored active tunnel credential pointer is invalid" >&2
        exit 1
    }
    case "$active_generation" in
        ""|.|..|*[!A-Za-z0-9._-]*)
            echo "Stored active tunnel credential generation is invalid" >&2
            exit 1
            ;;
    esac

    active_manifest="$DATA_DIR/pki/identities/agent/generations/$active_generation/manifest.json"
    [ -f "$active_manifest" ] && [ ! -L "$active_manifest" ] || {
        echo "Stored active tunnel credential manifest is unavailable" >&2
        exit 1
    }
    manifest_json="$(tr -d '\r\n' < "$active_manifest")"
    manifest_expectation="$(extract_json_object "$manifest_json" expectation)" || {
        echo "Stored active tunnel credential expectation is invalid" >&2
        exit 1
    }
    manifest_credential="$(extract_json_object "$manifest_json" credential)" || {
        echo "Stored active tunnel credential metadata is invalid" >&2
        exit 1
    }
    active_agent_id="$(extract_json_string "$manifest_expectation" agent_id)"
    active_domain_id="$(extract_json_string "$manifest_json" pki_domain_id)"
    active_certificate_id="$(extract_json_string "$manifest_credential" certificate_id)"
    [ -n "$active_agent_id" ] && [ -n "$active_domain_id" ] && [ -n "$active_certificate_id" ] || {
        echo "Stored active tunnel credential metadata is incomplete" >&2
        exit 1
    }
    if [ -n "$AGENT_ID" ] && [ "$AGENT_ID" != "$active_agent_id" ]; then
        echo "Stored active tunnel credential belongs to a different agent" >&2
        exit 1
    fi
    if [ -n "$PKI_DOMAIN_ID" ] && [ "$PKI_DOMAIN_ID" != "$active_domain_id" ]; then
        echo "Stored active tunnel credential belongs to a different PKI domain" >&2
        exit 1
    fi
    if [ "${AGENT_CONTROL_TOKEN_PERSISTED:-0}" != "1" ] || [ -z "$AGENT_TOKEN" ]; then
        echo "Stored active tunnel credential requires its durable control token" >&2
        exit 1
    fi

    acknowledgement_file="$DATA_DIR/pki/security/ack.json"
    if [ -e "$acknowledgement_file" ]; then
        [ -f "$acknowledgement_file" ] && [ ! -L "$acknowledgement_file" ] || {
            echo "Stored PKI acknowledgement is unsafe" >&2
            exit 1
        }
        acknowledgement_json="$(tr -d '\r\n' < "$acknowledgement_file")"
        acknowledgement_domain="$(extract_json_string "$acknowledgement_json" pki_domain_id)"
        acknowledgement_certificate="$(extract_json_string "$acknowledgement_json" certificate_id)"
        [ "$acknowledgement_domain" = "$active_domain_id" ] && \
            [ "$acknowledgement_certificate" = "$active_certificate_id" ] || {
            echo "Stored PKI acknowledgement does not match the active tunnel credential" >&2
            exit 1
        }
    fi

    AGENT_ID="$active_agent_id"
    PKI_DOMAIN_ID="$active_domain_id"
    renewal_file="$DATA_DIR/pki/identities/agent/renewal.json"
    if [ -e "$renewal_file" ]; then
        [ -f "$renewal_file" ] && [ ! -L "$renewal_file" ] || {
            echo "Stored tunnel credential renewal state is unsafe" >&2
            exit 1
        }
        restrict_private_path "$renewal_file" file
        renewal_json="$(tr -d '\r\n' < "$renewal_file")"
        renewal_required="$(extract_json_boolean "$renewal_json" reenrollment_required)" || {
            echo "Stored tunnel credential renewal state is invalid" >&2
            exit 1
        }
        if [ "$renewal_required" = "true" ]; then
            PKI_REENROLLMENT_REQUIRED="1"
            echo "[JOIN] Active tunnel credential requires one-time-token re-enrollment"
            return 1
        fi
    fi
    if [ "${FORCE_PKI_REENROLL:-0}" = "1" ]; then
        PKI_REENROLLMENT_REQUIRED="1"
        echo "[JOIN] Explicit one-time-token tunnel re-enrollment requested"
        return 1
    fi
    PKI_ACTIVE_REGISTRATION_PRESENT="1"
    return 0
}

prepare_tunnel_enrollment() {
    PKI_STAGED_REGISTRATION_PRESENT="0"
    PKI_ACTIVE_REGISTRATION_PRESENT="0"
    PKI_REENROLLMENT_REQUIRED="0"
    if load_active_registration_if_present; then
        load_security_ack_if_present
        return 0
    fi

    command -v openssl >/dev/null 2>&1 || {
        echo "openssl is required to generate the local tunnel key and CSR" >&2
        exit 1
    }

    identity_root="$DATA_DIR/pki/identities/agent"
    pending_root="$identity_root/pending"
    pending_key="$pending_root/private-key.pem"
    pending_csr="$pending_root/request.csr.pem"
    pending_journal="$pending_root/request.json"

    if [ -s "$pending_key" ] && [ -s "$pending_csr" ] && [ -s "$pending_journal" ]; then
        for private_dir in "$DATA_DIR/pki" "$DATA_DIR/pki/identities" "$identity_root" "$pending_root"; do
            [ -d "$private_dir" ] && [ ! -L "$private_dir" ] || {
                echo "Stored tunnel enrollment contains an unsafe directory: $private_dir" >&2
                exit 1
            }
            restrict_private_path "$private_dir" directory
        done
        for private_file in "$pending_key" "$pending_csr" "$pending_journal"; do
            [ -f "$private_file" ] && [ ! -L "$private_file" ] || {
                echo "Stored tunnel enrollment contains an unsafe file: $private_file" >&2
                exit 1
            }
            restrict_private_path "$private_file" file
        done
        if [ -e "$pending_root/response.json" ]; then
            [ -f "$pending_root/response.json" ] && [ ! -L "$pending_root/response.json" ] || {
                echo "Stored tunnel registration response is unsafe" >&2
                exit 1
            }
            restrict_private_path "$pending_root/response.json" file
        fi
        pending_json="$(tr -d '\r\n' < "$pending_journal")"
        PKI_ENROLLMENT_REQUEST_ID="$(extract_json_string "$pending_json" request_id)"
        PKI_ENROLLMENT_AGENT_ID="$(extract_json_string "$pending_json" agent_id || true)"
        PKI_ENROLLMENT_DOMAIN_ID="$(extract_json_string "$pending_json" pki_domain_id || true)"
        PKI_ENROLLMENT_CONTEXT_READY="1"
        PKI_TUNNEL_CSR_PEM="$(cat "$pending_csr")"
        [ -n "$PKI_ENROLLMENT_REQUEST_ID" ] && [ -n "$PKI_TUNNEL_CSR_PEM" ] || {
            echo "Stored tunnel enrollment request is incomplete" >&2
            exit 1
        }
        if [ -s "$pending_root/request-id" ] && \
           [ "$(cat "$pending_root/request-id")" != "$PKI_ENROLLMENT_REQUEST_ID" ]; then
            echo "Stored tunnel enrollment request ID is inconsistent" >&2
            exit 1
        fi
        openssl req -in "$pending_csr" -noout -verify >/dev/null 2>&1 || {
            echo "Stored tunnel enrollment CSR is invalid" >&2
            exit 1
        }
        pending_key_fingerprint="$(openssl pkey -in "$pending_key" -pubout -outform DER 2>/dev/null | openssl dgst -sha256 | sed 's/^.*= //')"
        pending_csr_fingerprint="$(openssl req -in "$pending_csr" -pubkey -noout | openssl pkey -pubin -outform DER 2>/dev/null | openssl dgst -sha256 | sed 's/^.*= //')"
        [ -n "$pending_key_fingerprint" ] && [ "$pending_key_fingerprint" = "$pending_csr_fingerprint" ] || {
            echo "Stored tunnel enrollment key does not match its CSR" >&2
            exit 1
        }
        # response.json remains beside the only private key until the Go
        # credential store durably activates it and removes pending/.
        load_staged_registration_if_present
        load_security_ack_if_present
        return 0
    fi

    if [ -e "$pending_root" ]; then
        echo "Stored tunnel enrollment is incomplete; refusing to replace its private key" >&2
        exit 1
    fi
    mkdir -p "$identity_root"
    restrict_private_path "$DATA_DIR/pki" directory
    restrict_private_path "$DATA_DIR/pki/identities" directory
    restrict_private_path "$identity_root" directory
    pending_tmp="$identity_root/.pending-new"
    rm -rf "$pending_tmp"
    mkdir "$pending_tmp"
    restrict_private_path "$pending_tmp" directory

    openssl ecparam -name prime256v1 -genkey -noout -out "$pending_tmp/private-key.pem"
    restrict_private_path "$pending_tmp/private-key.pem" file
    if [ -n "$AGENT_ID" ] || [ -n "$PKI_DOMAIN_ID" ]; then
        [ -n "$AGENT_ID" ] && [ -n "$PKI_DOMAIN_ID" ] || {
            echo "Stable agent ID and PKI domain must both be available for re-enrollment" >&2
            exit 1
        }
        identity_uri="spiffe://$PKI_DOMAIN_ID/agent/$AGENT_ID"
        cat > "$pending_tmp/openssl.cnf" <<EOF
[req]
distinguished_name = dn
prompt = no
req_extensions = san
[dn]
CN = $identity_uri
[san]
subjectAltName = URI:$identity_uri
EOF
        openssl req -new -sha256 -key "$pending_tmp/private-key.pem" \
            -out "$pending_tmp/request.csr.pem" -config "$pending_tmp/openssl.cnf"
        rm -f "$pending_tmp/openssl.cnf"
    else
        cat > "$pending_tmp/openssl.cnf" <<'EOF'
[req]
distinguished_name = dn
prompt = no
[dn]
CN = ignored
EOF
        openssl req -new -sha256 -key "$pending_tmp/private-key.pem" \
            -out "$pending_tmp/request.csr.pem" -config "$pending_tmp/openssl.cnf" -subj /
        rm -f "$pending_tmp/openssl.cnf"
    fi
    restrict_private_path "$pending_tmp/request.csr.pem" file

    PKI_ENROLLMENT_REQUEST_ID="$(openssl rand -hex 16)"
    PKI_TUNNEL_CSR_PEM="$(cat "$pending_tmp/request.csr.pem")"
    PKI_ENROLLMENT_AGENT_ID="$AGENT_ID"
    PKI_ENROLLMENT_DOMAIN_ID="$PKI_DOMAIN_ID"
    PKI_ENROLLMENT_CONTEXT_READY="1"
    public_fingerprint="$(openssl req -in "$pending_tmp/request.csr.pem" -pubkey -noout | \
        openssl pkey -pubin -outform DER 2>/dev/null | openssl dgst -sha256 | sed 's/^.*= //')"
    created_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    request_json="$(printf '{"request_id":%s,"kind":"agent","purpose":"client_auth","csr_pem":%s}' \
        "$(json_string "$PKI_ENROLLMENT_REQUEST_ID")" "$(json_string "$PKI_TUNNEL_CSR_PEM")")"
    fingerprint_payload="$(printf '{"storage_identity":"agent","pki_domain_id":%s,"agent_id":%s,"request":%s}' \
        "$(json_string "$PKI_DOMAIN_ID")" "$(json_string "$AGENT_ID")" "$request_json")"
    request_fingerprint="$(printf '%s' "$fingerprint_payload" | openssl dgst -sha256 | sed 's/^.*= //')"
    printf '{"version":1,"storage_identity":"agent","request":%s,"pki_domain_id":%s,"agent_id":%s,' \
        "$request_json" "$(json_string "$PKI_DOMAIN_ID")" "$(json_string "$AGENT_ID")" > "$pending_tmp/request.json"
    printf '"request_fingerprint_sha256":%s,"public_key_fingerprint_sha256":%s,"created_at":%s}' \
        "$(json_string "$request_fingerprint")" "$(json_string "$public_fingerprint")" "$(json_string "$created_at")" >> "$pending_tmp/request.json"
    restrict_private_path "$pending_tmp/request.json" file
    mv "$pending_tmp" "$pending_root"
    restrict_private_path "$pending_root" directory
    restrict_private_path "$pending_root/private-key.pem" file
    restrict_private_path "$pending_root/request.csr.pem" file
    restrict_private_path "$pending_root/request.json" file
    load_security_ack_if_present
}

load_staged_registration_if_present() {
    staged_file="$DATA_DIR/pki/identities/agent/pending/response.json"
    [ -s "$staged_file" ] || return 0
    staged_json="$(tr -d '\r\n' < "$staged_file")"
    staged_credential="$(extract_json_object "$staged_json" tunnel_credential)" || {
        echo "Stored tunnel registration response is invalid" >&2
        exit 1
    }
    staged_security="$(extract_json_object "$staged_json" security_snapshot)" || {
        echo "Stored tunnel registration security snapshot is invalid" >&2
        exit 1
    }
    staged_agent_id="$(extract_json_string "$staged_json" agent_id)"
    staged_domain_id="$(extract_json_string "$staged_security" pki_domain_id)"
    staged_certificate_id="$(extract_json_string "$staged_credential" certificate_id)"
    [ -n "$staged_agent_id" ] && [ -n "$staged_domain_id" ] && [ -n "$staged_certificate_id" ] || {
        echo "Stored tunnel registration response is incomplete" >&2
        exit 1
    }
    # response.json deliberately contains no control token. Only skip the
    # replayable registration call when the token came from durable input
    # (agent.env or an explicit argument). If agent.env was lost after the
    # public response was published, replay the original request so the server
    # can return the already-bound token instead of trusting a new random one.
    if [ "${AGENT_CONTROL_TOKEN_PERSISTED:-0}" != "1" ]; then
        return 0
    fi
    AGENT_ID="$staged_agent_id"
    PKI_DOMAIN_ID="$staged_domain_id"
    PKI_STAGED_REGISTRATION_PRESENT="1"
    clear_pending_registration_control_token
}

stage_pki_registration_response() {
    pki_json="$(extract_json_object "$REGISTER_RESPONSE" pki)" || {
        echo "Registration response is missing PKI material" >&2
        exit 1
    }
    credential_json="$(extract_json_object "$pki_json" tunnel_credential)" || {
        echo "Registration response is missing tunnel credential" >&2
        exit 1
    }
    security_json="$(extract_json_object "$pki_json" security_snapshot)" || {
        echo "Registration response is missing signed security snapshot" >&2
        exit 1
    }
    registered_agent_id="$(extract_json_string "$pki_json" agent_id)"
    registered_agent_token="$(extract_json_string "$pki_json" agent_token)"
    registered_pki_domain="$(extract_json_string "$security_json" pki_domain_id)"
    certificate_id="$(extract_json_string "$credential_json" certificate_id)"
    [ -n "$registered_agent_id" ] && [ -n "$registered_agent_token" ] && \
        [ -n "$registered_pki_domain" ] && [ -n "$certificate_id" ] || {
        echo "Registration response contains incomplete PKI identity metadata" >&2
        exit 1
    }
    AGENT_ID="$registered_agent_id"
    AGENT_TOKEN="$registered_agent_token"
    REGISTRATION_AGENT_TOKEN="$registered_agent_token"
    PKI_DOMAIN_ID="$registered_pki_domain"
    # Persist the control credential before publishing the sanitized staged
    # response. A crash in either direction can then recover with the exact
    # original enrollment journal and stable token.
    write_agent_env "$ENV_FILE"
    AGENT_CONTROL_TOKEN_PERSISTED="1"

    response_file="$DATA_DIR/pki/identities/agent/pending/response.json"
    response_tmp="$response_file.tmp.$$"
    printf '{"agent_id":%s,"tunnel_credential":%s,"security_snapshot":%s}' \
        "$(json_string "$registered_agent_id")" "$credential_json" "$security_json" > "$response_tmp"
    restrict_private_path "$response_tmp" file
    mv "$response_tmp" "$response_file"
    restrict_private_path "$response_file" file
    clear_pending_registration_control_token
    PKI_STAGED_REGISTRATION_PRESENT="1"
    echo "[JOIN] Registered agent $AGENT_ID with tunnel certificate $certificate_id"
}

register_agent() {
    if [ "${PKI_ACTIVE_REGISTRATION_PRESENT:-0}" = "1" ]; then
        echo "[JOIN] Reusing active tunnel registration for agent $AGENT_ID"
        return 0
    fi
    if [ "${PKI_STAGED_REGISTRATION_PRESENT:-0}" = "1" ]; then
        echo "[JOIN] Reusing staged tunnel registration for agent $AGENT_ID"
        return 0
    fi
    PAYLOAD=$(build_register_payload)
    registration_agent_token="${REGISTRATION_AGENT_TOKEN:-$AGENT_TOKEN}"
    [ -n "$registration_agent_token" ] || {
        echo "Registration control token is unavailable" >&2
        exit 1
    }

    echo "[JOIN] Registering Go agent to: $MASTER_URL/panel-api/agents/register"
    REGISTER_RESPONSE=$(curl -fsS \
      -H "Content-Type: application/json" \
      -H "X-Register-Token: $REGISTER_TOKEN" \
      -H "X-Agent-Token: $registration_agent_token" \
      -d "$PAYLOAD" \
      "$MASTER_URL/panel-api/agents/register")
    REGISTERED_AGENT_ID="$(extract_registered_agent_id "$REGISTER_RESPONSE")"
    [ -n "$REGISTERED_AGENT_ID" ] || {
        echo "Registered agent id missing from register response" >&2
        exit 1
    }
    stage_pki_registration_response
}

install_systemd_service() {
    [ "$PLATFORM" = "linux" ] || { echo "--install-systemd is only supported on Linux" >&2; exit 1; }
    SUDO_BIN="$(require_root_or_sudo)" || {
        echo "Installing systemd services requires root or sudo" >&2
        exit 1
    }
    command -v systemctl >/dev/null 2>&1 || { echo "systemctl is required for --install-systemd" >&2; exit 1; }

    SERVICE_FILE="/etc/systemd/system/nginx-reverse-emby-agent.service"
    cat <<EOF | run_root_cmd tee "$SERVICE_FILE" >/dev/null
[Unit]
Description=Nginx Reverse Emby Go Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$ENV_FILE
WorkingDirectory=$DATA_DIR
ExecStart=$BIN_PATH
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
    run_root_cmd systemctl daemon-reload

    SERVICE_EXISTS="0"
    SERVICE_WAS_ACTIVE="0"
    if service_exists; then
        SERVICE_EXISTS="1"
        if service_is_active; then
            SERVICE_WAS_ACTIVE="1"
        fi
    fi
    if [ "$SERVICE_WAS_ACTIVE" = "1" ]; then
        run_root_cmd systemctl stop nginx-reverse-emby-agent.service
    fi

    run_root_cmd mv "$BIN_TMP_PATH" "$BIN_PATH"
    run_root_cmd mv "$BIN_TMP_PATH.manifest.json" "$BIN_PATH.manifest.json"
    if [ "$SERVICE_EXISTS" = "1" ]; then
        run_root_cmd systemctl enable nginx-reverse-emby-agent.service
        run_root_cmd systemctl start nginx-reverse-emby-agent.service
    else
        run_root_cmd systemctl enable --now nginx-reverse-emby-agent.service
    fi
    install_uninstall_wrapper
    echo "[JOIN] Installed and started systemd service: nginx-reverse-emby-agent.service"
}

install_launchd_service() {
    [ "$PLATFORM" = "darwin" ] || { echo "--install-launchd is only supported on macOS" >&2; exit 1; }
    command -v launchctl >/dev/null 2>&1 || { echo "launchctl is required for --install-launchd" >&2; exit 1; }
    LAUNCHD_DIR="$HOME/Library/LaunchAgents"
    SERVICE_LABEL="com.nginx-reverse-emby.agent"
    SERVICE_FILE="$LAUNCHD_DIR/$SERVICE_LABEL.plist"
    START_COMMAND="set -a && . $(shell_quote "$ENV_FILE") && set +a && exec $(shell_quote "$BIN_PATH")"
    mv "$BIN_TMP_PATH" "$BIN_PATH"
    mv "$BIN_TMP_PATH.manifest.json" "$BIN_PATH.manifest.json"
    mkdir -p "$LAUNCHD_DIR"
    cat > "$SERVICE_FILE" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$(xml_escape "$SERVICE_LABEL")</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-lc</string>
    <string>$(xml_escape "$START_COMMAND")</string>
  </array>
  <key>WorkingDirectory</key>
  <string>$(xml_escape "$DATA_DIR")</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>$(xml_escape "$DATA_DIR/agent.stdout.log")</string>
  <key>StandardErrorPath</key>
  <string>$(xml_escape "$DATA_DIR/agent.stderr.log")</string>
</dict>
</plist>
EOF
    launchctl unload "$SERVICE_FILE" >/dev/null 2>&1 || true
    launchctl load -w "$SERVICE_FILE"
    install_uninstall_wrapper
    echo "[JOIN] Installed and loaded launchd agent: $SERVICE_LABEL"
}

install_manual_runtime() {
    mv "$BIN_TMP_PATH" "$BIN_PATH"
    mv "$BIN_TMP_PATH.manifest.json" "$BIN_PATH.manifest.json"
    echo "[JOIN] Start command:"
    echo "  set -a && . $ENV_FILE && set +a && $BIN_PATH"
}

migrate_data_dir_contents() {
    old_data_dir="$1"
    new_data_dir="$2"

    [ -d "$old_data_dir" ] || return 0
    mkdir -p "$new_data_dir"
    cp -Rp "$old_data_dir"/. "$new_data_dir"/
    rm -rf "$old_data_dir"
}

load_existing_agent_env_if_present() {
    env_file="$1"
    [ -f "$env_file" ] || return 0

    NRE_MASTER_URL=""
    NRE_AGENT_NAME=""
    NRE_AGENT_TOKEN=""
    NRE_AGENT_URL=""
    NRE_AGENT_VERSION=""
    NRE_AGENT_TAGS=""
    NRE_AGENT_CAPABILITIES=""
    NRE_AGENT_ID=""
    NRE_PKI_DOMAIN_ID=""

    set -a
    . "$env_file"
    set +a

    if [ -n "$NRE_AGENT_TOKEN" ]; then
        AGENT_CONTROL_TOKEN_PERSISTED="1"
    fi

    MASTER_URL="${MASTER_URL:-$NRE_MASTER_URL}"
    AGENT_NAME="${AGENT_NAME:-$NRE_AGENT_NAME}"
    if [ -n "$NRE_AGENT_TOKEN" ]; then
        # Once an active token exists, explicit input is only a proposed
        # replacement. Never let it overwrite the last durable token before
        # the registration transaction succeeds.
        AGENT_TOKEN="$NRE_AGENT_TOKEN"
    fi
    AGENT_URL="${AGENT_URL:-$NRE_AGENT_URL}"
    AGENT_VERSION="${AGENT_VERSION:-$NRE_AGENT_VERSION}"
    AGENT_TAGS="${AGENT_TAGS:-$NRE_AGENT_TAGS}"
    AGENT_CAPABILITIES="${AGENT_CAPABILITIES:-$NRE_AGENT_CAPABILITIES}"
    AGENT_ID="${AGENT_ID:-$NRE_AGENT_ID}"
    PKI_DOMAIN_ID="${PKI_DOMAIN_ID:-$NRE_PKI_DOMAIN_ID}"
}

# A failed migrate may already have completed registration and credential
# activation before service/connectivity verification rolled back. Prefer the
# new data root's stable identity on retry; legacy env is only the source for
# fields that were never durably migrated.
load_migration_recovery_env_if_present() {
    env_file="$1"
    [ -f "$env_file" ] || return 0

    NRE_MASTER_URL=""
    NRE_AGENT_ID=""
    NRE_AGENT_NAME=""
    NRE_AGENT_TOKEN=""
    NRE_AGENT_URL=""
    NRE_AGENT_VERSION=""
    NRE_AGENT_TAGS=""
    NRE_AGENT_CAPABILITIES=""
    NRE_DATA_DIR=""
    NRE_PKI_DOMAIN_ID=""
    set -a
    . "$env_file"
    set +a

    if [ -n "$NRE_AGENT_ID$NRE_AGENT_TOKEN$NRE_PKI_DOMAIN_ID" ]; then
        [ -n "$NRE_AGENT_ID" ] && [ -n "$NRE_AGENT_TOKEN" ] && [ -n "$NRE_PKI_DOMAIN_ID" ] || {
            echo "New agent data root contains an incomplete stable PKI identity" >&2
            exit 1
        }
        if [ -n "$NRE_DATA_DIR" ] && [ "$(absolute_path "$NRE_DATA_DIR")" != "$DATA_DIR" ]; then
            echo "New agent env belongs to a different data root" >&2
            exit 1
        fi
        AGENT_ID="$NRE_AGENT_ID"
        AGENT_TOKEN="$NRE_AGENT_TOKEN"
        PKI_DOMAIN_ID="$NRE_PKI_DOMAIN_ID"
        AGENT_CONTROL_TOKEN_PERSISTED="1"
    fi
    MASTER_URL="${MASTER_URL:-$NRE_MASTER_URL}"
    AGENT_NAME="${AGENT_NAME:-$NRE_AGENT_NAME}"
    AGENT_URL="${AGENT_URL:-$NRE_AGENT_URL}"
    AGENT_VERSION="${AGENT_VERSION:-$NRE_AGENT_VERSION}"
    AGENT_TAGS="${AGENT_TAGS:-$NRE_AGENT_TAGS}"
    AGENT_CAPABILITIES="${AGENT_CAPABILITIES:-$NRE_AGENT_CAPABILITIES}"
}

systemd_unit_exists() {
    unit_name="$1"
    command -v systemctl >/dev/null 2>&1 || return 1
    systemctl list-unit-files "$unit_name" --no-legend 2>/dev/null | grep -q "^$unit_name[[:space:]]"
}

disable_systemd_unit_if_present() {
    unit_name="$1"
    if systemd_unit_exists "$unit_name"; then
        run_root_cmd systemctl disable --now "$unit_name"
    fi
}

backup_legacy_unit() {
    unit_name="$1"
    unit_path="/etc/systemd/system/$unit_name"
    backup_path="$DATA_DIR/$unit_name.bak"
    if [ -f "$unit_path" ]; then
        cp "$unit_path" "$backup_path"
    fi
}

restore_legacy_units() {
    restored=0
    for bak in "$DATA_DIR"/*.bak; do
        [ -f "$bak" ] || continue
        unit_name="$(basename "$bak" .bak)"
        unit_path="/etc/systemd/system/$unit_name"
        run_root_cmd cp "$bak" "$unit_path"
        run_root_cmd systemctl daemon-reload
        run_root_cmd systemctl enable --now "$unit_name"
        rm -f "$bak"
        restored=1
    done
    if [ "$restored" -eq 1 ]; then
        echo "[MIGRATE] Restored legacy agent services from backup"
    fi
}

verify_systemd_service_active() {
    attempts=0
    while [ "$attempts" -lt 10 ]; do
        if run_root_cmd systemctl is-active --quiet nginx-reverse-emby-agent.service; then
            return 0
        fi
        attempts=$((attempts + 1))
        sleep 1
    done
    return 1
}

verify_master_connectivity() {
    attempts=0
    while [ "$attempts" -lt 6 ]; do
        if curl -fsS -o /dev/null "$MASTER_URL/panel-api/health" 2>/dev/null; then
            return 0
        fi
        attempts=$((attempts + 1))
        sleep 5
    done
    return 1
}

verify_agent_heartbeat() {
    agent_heartbeat_interval="${NRE_HEARTBEAT_INTERVAL:-10}"
    wait_seconds=$((agent_heartbeat_interval * 2))
    if [ "$wait_seconds" -lt 15 ]; then
        wait_seconds=15
    fi
    echo "[MIGRATE] Waiting ${wait_seconds}s for agent to complete first heartbeat cycle"
    sleep "$wait_seconds"

    service_start="$(run_root_cmd systemctl show -p ActiveEnterTimestamp nginx-reverse-emby-agent.service 2>/dev/null | cut -d= -f2- || true)"
    if [ -n "$service_start" ]; then
        error_lines="$(run_root_cmd journalctl -u nginx-reverse-emby-agent.service --since "$service_start" --no-pager 2>/dev/null | grep -c 'sync error\|heartbeat failed\|runtime apply error' || true)"
        if [ "$error_lines" -gt 0 ]; then
            echo "[MIGRATE] Agent logged $error_lines heartbeat/sync error(s) since startup, aborting migration" >&2
            return 1
        fi
    fi

    echo "[MIGRATE] Probing heartbeat endpoint with agent credentials to confirm registration"
    heartbeat_resp="$(curl -fsS -o /dev/null -w '%{http_code}' \
        -H "X-Agent-Token: $AGENT_TOKEN" \
        -H "Content-Type: application/json" \
        -d '{"version":"1"}' \
        "$MASTER_URL/panel-api/agents/heartbeat" 2>/dev/null || true)"
    if [ "$heartbeat_resp" != "200" ]; then
        echo "[MIGRATE] Heartbeat probe returned HTTP $heartbeat_resp (expected 200), agent may be unauthorized or misconfigured" >&2
        return 1
    fi

    return 0
}

list_legacy_cert_domains() {
    tmp_domains=$(mktemp)
    if [ -d "$OLD_DIRECT_CERT_DIR" ]; then
        for cert_dir in "$OLD_DIRECT_CERT_DIR"/*; do
            [ -d "$cert_dir" ] || continue
            basename -- "$cert_dir" >> "$tmp_domains"
        done
    fi
    if [ -f "$OLD_MANAGED_CERTS_JSON" ]; then
        grep -o '"domain"[[:space:]]*:[[:space:]]*"[^"]*"' "$OLD_MANAGED_CERTS_JSON" 2>/dev/null | \
            sed 's/.*"domain"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/' >> "$tmp_domains" || true
    fi
    if [ -s "$tmp_domains" ]; then
        sort -u "$tmp_domains"
    fi
    rm -f "$tmp_domains"
}

cleanup_legacy_acme() {
    [ -x "$OLD_ACME_HOME/acme.sh" ] || return 0

    tmp_domains=$(mktemp)
    list_legacy_cert_domains > "$tmp_domains"
    [ -s "$tmp_domains" ] || {
        rm -f "$tmp_domains"
        return 0
    }

    while IFS= read -r cert_domain; do
        [ -n "$cert_domain" ] || continue
        echo "[MIGRATE] Removing legacy acme record: $cert_domain"
        acme_domain="$(normalize_legacy_acme_domain "$cert_domain")"
        if "$OLD_ACME_HOME/acme.sh" --home "$OLD_ACME_HOME" --config-home "$OLD_ACME_HOME" --cert-home "$OLD_ACME_HOME" --remove -d "$acme_domain" --ecc >/dev/null 2>&1; then
            continue
        fi
        if "$OLD_ACME_HOME/acme.sh" --home "$OLD_ACME_HOME" --config-home "$OLD_ACME_HOME" --cert-home "$OLD_ACME_HOME" --remove -d "$acme_domain" >/dev/null 2>&1; then
            continue
        fi
        rm -f "$tmp_domains"
        echo "failed to remove legacy acme record: $cert_domain" >&2
        exit 1
    done < "$tmp_domains"
    rm -f "$tmp_domains"
}

normalize_legacy_acme_domain() {
    case "$1" in
        \*.*) printf '%s\n' "${1#*.}" ;;
        *) printf '%s\n' "$1" ;;
    esac
}

cleanup_legacy_nginx_runtime() {
    if ! legacy_nginx_runtime_present; then
        return 0
    fi
    if command -v systemctl >/dev/null 2>&1; then
        disable_systemd_unit_if_present nginx.service >/dev/null 2>&1 || true
    fi
    run_root_cmd rm -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.include.conf
    run_root_cmd rm -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.globals.conf
    run_root_cmd rm -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.status.conf
    run_root_cmd rm -rf /etc/nginx/conf.d/dynamic
    run_root_cmd rm -rf /etc/nginx/stream-conf.d/dynamic
}

legacy_nginx_runtime_present() {
    [ -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.include.conf ] && return 0
    [ -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.globals.conf ] && return 0
    [ -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.status.conf ] && return 0
    [ -d /etc/nginx/conf.d/dynamic ] && return 0
    [ -d /etc/nginx/stream-conf.d/dynamic ] && return 0
    return 1
}

cleanup_legacy_runtime() {
    cleanup_legacy_acme

    disable_systemd_unit_if_present nginx-reverse-emby-agent-renew.service
    if [ -f /etc/systemd/system/nginx-reverse-emby-agent-renew.service ]; then
        run_root_cmd rm -f /etc/systemd/system/nginx-reverse-emby-agent-renew.service
        run_root_cmd systemctl daemon-reload
    fi
    cleanup_legacy_nginx_runtime
    run_root_cmd rm -rf "$OLD_SOURCE_DIR"
    rm -f "$DATA_DIR"/*.bak
}

cleanup_local_agent_runtime() {
    if [ "$PLATFORM" = "linux" ]; then
        SUDO_BIN="$(require_root_or_sudo)" || {
            echo "Uninstalling systemd services requires root or sudo" >&2
            exit 1
        }
        disable_systemd_unit_if_present nginx-reverse-emby-agent.service
        disable_systemd_unit_if_present nginx-reverse-emby-agent-renew.service
        run_root_cmd rm -f /etc/systemd/system/nginx-reverse-emby-agent.service
        run_root_cmd rm -f /etc/systemd/system/nginx-reverse-emby-agent-renew.service
        if command -v systemctl >/dev/null 2>&1; then
            run_root_cmd systemctl daemon-reload
        fi
        run_root_cmd rm -f "$UNINSTALL_WRAPPER_PATH"
        run_root_cmd rm -rf "$DATA_DIR"
        if [ -n "${SOURCE_DIR:-}" ]; then
            SOURCE_DIR="$(absolute_path "$SOURCE_DIR")"
            run_root_cmd rm -rf "$SOURCE_DIR"
        fi
    elif [ "$PLATFORM" = "darwin" ]; then
        SERVICE_FILE="$HOME/Library/LaunchAgents/com.nginx-reverse-emby.agent.plist"
        if [ -f "$SERVICE_FILE" ]; then
            launchctl unload "$SERVICE_FILE" >/dev/null 2>&1 || true
            rm -f "$SERVICE_FILE"
        fi
        remove_uninstall_wrapper
        rm -rf "$DATA_DIR"
        if [ -n "${SOURCE_DIR:-}" ]; then
            SOURCE_DIR="$(absolute_path "$SOURCE_DIR")"
            rm -rf "$SOURCE_DIR"
        fi
    else
        remove_uninstall_wrapper
        rm -rf "$DATA_DIR"
        if [ -n "${SOURCE_DIR:-}" ]; then
            SOURCE_DIR="$(absolute_path "$SOURCE_DIR")"
            rm -rf "$SOURCE_DIR"
        fi
    fi
    cleanup_legacy_nginx_runtime
}

load_legacy_runtime() {
    OLD_SOURCE_DIR="$(absolute_path "$SOURCE_DIR")"
    OLD_ENV_FILE="$OLD_SOURCE_DIR/agent.env"
    OLD_MANAGED_CERTS_JSON="$OLD_SOURCE_DIR/managed_certificates.json"

    [ -f "$OLD_ENV_FILE" ] || {
        echo "legacy agent env not found: $OLD_ENV_FILE" >&2
        exit 1
    }

    set -a
    . "$OLD_ENV_FILE"
    set +a

    OLD_DIRECT_CERT_DIR="${DIRECT_CERT_DIR:-$OLD_SOURCE_DIR/certs}"
    OLD_ACME_HOME="${ACME_HOME:-$OLD_SOURCE_DIR/.acme.sh}"

    MASTER_URL="${MASTER_URL:-${MASTER_PANEL_URL:-}}"
    AGENT_NAME="${AGENT_NAME:-${NRE_AGENT_NAME:-${AGENT_NAME:-}}}"
    AGENT_TOKEN="${AGENT_TOKEN:-${NRE_AGENT_TOKEN:-${AGENT_TOKEN:-}}}"
    AGENT_URL="${AGENT_URL:-${NRE_AGENT_URL:-${AGENT_PUBLIC_URL:-}}}"
    AGENT_VERSION="${AGENT_VERSION:-${NRE_AGENT_VERSION:-${AGENT_VERSION:-}}}"
    AGENT_TAGS="${AGENT_TAGS:-${NRE_AGENT_TAGS:-${AGENT_TAGS:-}}}"
    AGENT_CAPABILITIES="${AGENT_CAPABILITIES:-${AGENT_CAPABILITIES:-http_rules,local_acme,cert_install,l4}}"
    AGENT_ID="${AGENT_ID:-${NRE_AGENT_ID:-}}"
    PKI_DOMAIN_ID="${PKI_DOMAIN_ID:-${NRE_PKI_DOMAIN_ID:-}}"
}

run_join() {
    [ "$INSTALL_SYSTEMD$INSTALL_LAUNCHD" != "11" ] || {
        echo "Use either --install-systemd or --install-launchd, not both" >&2
        exit 1
    }

    case "$PLATFORM" in
        linux|darwin) ;;
        *) echo "Unsupported platform for join-agent.sh: $PLATFORM" >&2; exit 1 ;;
    esac
    case "$ARCH" in
        amd64|arm64) ;;
        *) echo "Unsupported architecture for join-agent.sh: $ARCH" >&2; exit 1 ;;
    esac

    command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }

    if [ "$INSTALL_SYSTEMD" = "1" ] && [ "$USER_DATA_DIR_DEFAULT" = "1" ]; then
        OLD_DATA_DIR="$(absolute_path "./agent-data")"
    elif [ "$USER_DATA_DIR_DEFAULT" = "1" ]; then
        DATA_DIR="$HOME/.nre-agent"
    fi

    DATA_DIR="$(absolute_path "$DATA_DIR")"
    if [ "${OLD_DATA_DIR:-}" ] && [ ! -f "$DATA_DIR/agent.env" ] && [ -f "$OLD_DATA_DIR/agent.env" ]; then
        echo "[JOIN] Migrating agent data from $OLD_DATA_DIR to $DATA_DIR"
        migrate_data_dir_contents "$OLD_DATA_DIR" "$DATA_DIR"
    fi

    BIN_DIR="$DATA_DIR/bin"
    ENV_FILE="$DATA_DIR/agent.env"
    BIN_PATH="$BIN_DIR/nre-agent"
    BIN_TMP_PATH="$BIN_PATH.tmp.$$"
    JOIN_SCRIPT_PATH="$BIN_DIR/join-agent.sh"
    UNINSTALL_WRAPPER_PATH="/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh"
    ASSET_NAME="nre-agent-$PLATFORM-$ARCH"

    if [ -n "$AGENT_TOKEN" ]; then
        AGENT_CONTROL_TOKEN_PERSISTED="1"
    fi
    load_existing_agent_env_if_present "$ENV_FILE"

    AGENT_NAME="${AGENT_NAME:-${HOSTNAME:-$(hostname)}}"
    AGENT_VERSION="${AGENT_VERSION:-1}"

    [ -n "$REGISTER_TOKEN" ] || { echo "Missing --register-token" >&2; exit 1; }
    [ -n "$MASTER_URL" ] || {
        echo "Missing --master-url and no embedded control-plane URL is available" >&2
        exit 1
    }
    MASTER_URL="$(normalize_master_url "$MASTER_URL")"
    if ! is_valid_master_url "$MASTER_URL"; then
        echo "Invalid --master-url: $MASTER_URL" >&2
        echo "Expected format: http://host:port or https://host" >&2
        exit 1
    fi

    ASSET_BASE_URL="$(trim_slash "$ASSET_BASE_URL")"
    AGENT_URL="$(trim_slash "$AGENT_URL")"
    AGENT_TOKEN="${AGENT_TOKEN:-$(generate_token)}"

    mkdir -p "$BIN_DIR"
    echo "[JOIN] Installing nre-agent to: $BIN_PATH"
    rm -f "$BIN_TMP_PATH"
    rm -f "$BIN_TMP_PATH.manifest.json"
    copy_or_download_binary "$ASSET_NAME" "$BIN_TMP_PATH"
    persist_installed_join_script
    prepare_tunnel_enrollment
    prepare_registration_control_token
    write_agent_env "$ENV_FILE"
    register_agent
    write_agent_env "$ENV_FILE"

    echo "[JOIN] Agent binary: $BIN_PATH"
    echo "[JOIN] Agent env: $ENV_FILE"

    if [ "$INSTALL_SYSTEMD" = "1" ]; then
        install_systemd_service
    elif [ "$INSTALL_LAUNCHD" = "1" ]; then
        install_launchd_service
    else
        install_manual_runtime
    fi
}

run_migrate_from_main() {
    [ "$PLATFORM" = "linux" ] || { echo "migrate-from-main is only supported on Linux" >&2; exit 1; }
    INSTALL_LAUNCHD="0"
    if [ "$INSTALL_SYSTEMD" != "1" ]; then
        INSTALL_SYSTEMD="1"
    fi

    load_legacy_runtime

    AGENT_NAME="${AGENT_NAME:-${HOSTNAME:-$(hostname)}}"
    AGENT_VERSION="${AGENT_VERSION:-1}"

    [ -n "$REGISTER_TOKEN" ] || { echo "Missing --register-token" >&2; exit 1; }
    [ -n "$MASTER_URL" ] || {
        echo "Missing --master-url and legacy agent env does not provide MASTER_PANEL_URL" >&2
        exit 1
    }
    [ -n "$AGENT_TOKEN" ] || {
        echo "legacy agent token missing" >&2
        exit 1
    }

    MASTER_URL="$(normalize_master_url "$MASTER_URL")"
    if ! is_valid_master_url "$MASTER_URL"; then
        echo "Invalid --master-url: $MASTER_URL" >&2
        echo "Expected format: http://host:port or https://host" >&2
        exit 1
    fi

    command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }
    command -v systemctl >/dev/null 2>&1 || { echo "systemctl is required for migrate-from-main" >&2; exit 1; }

    DATA_DIR="$(absolute_path "$DATA_DIR")"
    if [ "$DATA_DIR" = "$OLD_SOURCE_DIR" ]; then
        echo "--data-dir must not be the same as --source-dir during migration" >&2
        exit 1
    fi

    ASSET_BASE_URL="$(trim_slash "$ASSET_BASE_URL")"
    AGENT_URL="$(trim_slash "$AGENT_URL")"
    BIN_DIR="$DATA_DIR/bin"
    ENV_FILE="$DATA_DIR/agent.env"
    BIN_PATH="$BIN_DIR/nre-agent"
    BIN_TMP_PATH="$BIN_PATH.tmp.$$"
    JOIN_SCRIPT_PATH="$BIN_DIR/join-agent.sh"
    UNINSTALL_WRAPPER_PATH="/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh"
    WRAPPER_SOURCE_DIR="$SOURCE_DIR"
    ASSET_NAME="nre-agent-$PLATFORM-$ARCH"

    load_migration_recovery_env_if_present "$ENV_FILE"

    mkdir -p "$BIN_DIR"
    echo "[MIGRATE] Preparing go-agent install: $BIN_PATH"
    rm -f "$BIN_TMP_PATH"
    rm -f "$BIN_TMP_PATH.manifest.json"
    copy_or_download_binary "$ASSET_NAME" "$BIN_TMP_PATH"
    persist_installed_join_script
    prepare_tunnel_enrollment
    prepare_registration_control_token
    write_agent_env "$ENV_FILE"

    SUDO_BIN="$(require_root_or_sudo)" || {
        echo "Migrating systemd services requires root or sudo" >&2
        exit 1
    }

    register_agent
    write_agent_env "$ENV_FILE"

    echo "[MIGRATE] Backing up legacy unit files"
    backup_legacy_unit nginx-reverse-emby-agent.service
    backup_legacy_unit nginx-reverse-emby-agent-renew.service

    echo "[MIGRATE] Stopping legacy lightweight Agent services"
    disable_systemd_unit_if_present nginx-reverse-emby-agent.service
    disable_systemd_unit_if_present nginx-reverse-emby-agent-renew.service

    install_systemd_service

    if ! verify_systemd_service_active; then
        echo "[MIGRATE] new go-agent service failed to become active, restoring legacy services" >&2
        disable_systemd_unit_if_present nginx-reverse-emby-agent.service
        restore_legacy_units
        exit 1
    fi

    if ! verify_master_connectivity; then
        echo "[MIGRATE] master is not reachable at $MASTER_URL, restoring legacy services" >&2
        disable_systemd_unit_if_present nginx-reverse-emby-agent.service
        restore_legacy_units
        exit 1
    fi

    if ! verify_agent_heartbeat; then
        echo "[MIGRATE] agent heartbeat verification failed, restoring legacy services" >&2
        disable_systemd_unit_if_present nginx-reverse-emby-agent.service
        restore_legacy_units
        exit 1
    fi

    echo "[MIGRATE] New go-agent service is active, heartbeat verified, cleaning legacy runtime"
    cleanup_legacy_runtime
}

run_uninstall_agent() {
    if [ "$USER_DATA_DIR_DEFAULT" = "1" ]; then
        if [ -d "/var/lib/nre-agent" ]; then
            DATA_DIR="/var/lib/nre-agent"
        elif [ -n "${HOME:-}" ] && [ -d "$HOME/.nre-agent" ]; then
            DATA_DIR="$HOME/.nre-agent"
        fi
    fi
    DATA_DIR="$(absolute_path "$DATA_DIR")"
    cleanup_local_agent_runtime
    echo "[UNINSTALL] Local agent runtime removed. Delete the agent record from the control panel if it is no longer needed."
}

COMMAND="join"
MASTER_URL="$DEFAULT_MASTER_URL"
ASSET_BASE_URL="$DEFAULT_ASSET_BASE_URL"
REGISTER_TOKEN=""
AGENT_NAME=""
AGENT_TOKEN=""
AGENT_ID=""
AGENT_URL=""
DATA_DIR="/var/lib/nre-agent"
USER_DATA_DIR_DEFAULT="1"
AGENT_VERSION=""
AGENT_TAGS=""
AGENT_CAPABILITIES=""
PKI_DOMAIN_ID=""
PKI_ENROLLMENT_REQUEST_ID=""
PKI_TUNNEL_CSR_PEM=""
PKI_SECURITY_ACK_JSON=""
PKI_ENROLLMENT_AGENT_ID=""
PKI_ENROLLMENT_DOMAIN_ID=""
PKI_ENROLLMENT_CONTEXT_READY="0"
PKI_STAGED_REGISTRATION_PRESENT="0"
PKI_ACTIVE_REGISTRATION_PRESENT="0"
PKI_REENROLLMENT_REQUIRED="0"
FORCE_PKI_REENROLL="0"
AGENT_CONTROL_TOKEN_PERSISTED="0"
REGISTRATION_AGENT_TOKEN=""
REQUESTED_AGENT_TOKEN=""
INSTALL_SYSTEMD="0"
INSTALL_LAUNCHD="0"
BINARY_URL=""
MANIFEST_URL=""
SOURCE_DIR="/opt/nginx-reverse-emby-agent"
WRAPPER_SOURCE_DIR=""
UNINSTALL_WRAPPER_PATH="/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh"
SCRIPT_DIR="$(resolve_script_dir 2>/dev/null || true)"
PLATFORM="$(detect_platform)"
ARCH="$(detect_arch)"

if [ $# -gt 0 ] && [ "$1" = "migrate-from-main" ]; then
    COMMAND="migrate-from-main"
    shift 1
elif [ $# -gt 0 ] && [ "$1" = "uninstall-agent" ]; then
    COMMAND="uninstall-agent"
    shift 1
fi

while [ $# -gt 0 ]; do
    case "$1" in
        --master-url) MASTER_URL="$2"; shift 2 ;;
        --asset-base-url) ASSET_BASE_URL="$2"; shift 2 ;;
        --register-token) REGISTER_TOKEN="$2"; shift 2 ;;
        --agent-name) AGENT_NAME="$2"; shift 2 ;;
        --agent-token) AGENT_TOKEN="$2"; REQUESTED_AGENT_TOKEN="$2"; shift 2 ;;
        --agent-url) AGENT_URL="$2"; shift 2 ;;
        --data-dir) DATA_DIR="$2"; USER_DATA_DIR_DEFAULT="0"; shift 2 ;;
        --version) AGENT_VERSION="$2"; shift 2 ;;
        --tags) AGENT_TAGS="$2"; shift 2 ;;
        --binary-url) BINARY_URL="$2"; shift 2 ;;
        --manifest-url) MANIFEST_URL="$2"; shift 2 ;;
        --force-pki-reenroll) FORCE_PKI_REENROLL="1"; shift 1 ;;
        --source-dir) SOURCE_DIR="$2"; WRAPPER_SOURCE_DIR="$2"; shift 2 ;;
        --install-systemd) INSTALL_SYSTEMD="1"; shift 1 ;;
        --install-launchd) INSTALL_LAUNCHD="1"; shift 1 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "Unknown argument: $1" >&2; usage >&2; exit 1 ;;
    esac
done

if [ -n "$MANIFEST_URL" ] && [ -z "$BINARY_URL" ]; then
    echo "--manifest-url requires --binary-url" >&2
    exit 1
fi

case "$COMMAND" in
    join) run_join ;;
    migrate-from-main) run_migrate_from_main ;;
    uninstall-agent) run_uninstall_agent ;;
    *) echo "Unknown command: $COMMAND" >&2; exit 1 ;;
esac
