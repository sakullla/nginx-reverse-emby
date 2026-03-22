#!/bin/sh
set -eu

# Example only:
# 1. Read synced HTTP / L4 / certificate files
# 2. Generate your own nginx configs
# 3. Validate and reload nginx
#
# If you use join-agent.sh without --apply-command, the built-in
# nginx-reverse-emby-apply.sh already handles this for you.

[ -n "${RULES_JSON:-}" ] || { echo "RULES_JSON is required" >&2; exit 1; }
[ -f "$RULES_JSON" ] || { echo "Rules file not found: $RULES_JSON" >&2; exit 1; }

L4_RULES_JSON="${L4_RULES_JSON:-}"
MANAGED_CERTS_JSON="${MANAGED_CERTS_JSON:-}"
MANAGED_CERTS_POLICY_JSON="${MANAGED_CERTS_POLICY_JSON:-}"
NGINX_BIN="${NGINX_BIN:-nginx}"

echo "[example] HTTP rules: $RULES_JSON"
[ -n "$L4_RULES_JSON" ] && echo "[example] L4 rules: $L4_RULES_JSON"
[ -n "$MANAGED_CERTS_JSON" ] && echo "[example] Managed cert bundle: $MANAGED_CERTS_JSON"
[ -n "$MANAGED_CERTS_POLICY_JSON" ] && echo "[example] Managed cert policy: $MANAGED_CERTS_POLICY_JSON"
echo "[example] TODO: generate nginx configs from the synced files"

"$NGINX_BIN" -t
"$NGINX_BIN" -s reload
