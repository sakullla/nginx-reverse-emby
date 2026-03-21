#!/bin/sh
set -eu

# Example only:
# 1. Read synced rules from $RULES_JSON
# 2. Generate your nginx conf files
# 3. Validate and reload nginx
#
# Replace this script with your own generator logic.

[ -n "${RULES_JSON:-}" ] || { echo "RULES_JSON is required" >&2; exit 1; }
[ -f "$RULES_JSON" ] || { echo "Rules file not found: $RULES_JSON" >&2; exit 1; }

echo "[example] Synced rules file: $RULES_JSON"
echo "[example] TODO: generate nginx configs from the rules JSON"

nginx -t
nginx -s reload
