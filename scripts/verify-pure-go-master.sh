#!/bin/sh
set -eu

DATA_DIR="${1:-}"
if [ -z "$DATA_DIR" ]; then
  echo "usage: verify-pure-go-master.sh <copied-panel-data-dir>" >&2
  exit 1
fi

IMAGE="${NRE_VERIFY_IMAGE:-nginx-reverse-emby:pure-go}"
HOST_PORT="${NRE_VERIFY_PORT:-18080}"
PANEL_TOKEN="${NRE_VERIFY_PANEL_TOKEN:-test-token}"
REGISTER_TOKEN="${NRE_VERIFY_REGISTER_TOKEN:-test-register-token}"
CONTAINER_ID=""

cleanup() {
  if [ -n "$CONTAINER_ID" ]; then
    docker rm -f "$CONTAINER_ID" >/dev/null 2>&1 || true
  fi
}

trap cleanup EXIT INT TERM

docker image inspect "$IMAGE" >/dev/null 2>&1 || {
  echo "image not found: $IMAGE" >&2
  exit 1
}

CONTAINER_ID="$(docker run -d --rm \
  -e NRE_CONTROL_PLANE_ADDR=0.0.0.0:8080 \
  -e NRE_CONTROL_PLANE_DATA_DIR=/data \
  -e NRE_PANEL_TOKEN="$PANEL_TOKEN" \
  -e NRE_REGISTER_TOKEN="$REGISTER_TOKEN" \
  -e NRE_ENABLE_LOCAL_AGENT=1 \
  -e NRE_LOCAL_AGENT_ID=local \
  -e NRE_LOCAL_AGENT_NAME=local \
  -v "${DATA_DIR}:/data" \
  -p "${HOST_PORT}:8080" \
  "$IMAGE")"

attempt=0
until curl -fsS "http://127.0.0.1:${HOST_PORT}/panel-api/health" >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then
    docker logs "$CONTAINER_ID" >&2 || true
    echo "pure-go master did not become healthy on port ${HOST_PORT}" >&2
    exit 1
  fi
  sleep 1
done

curl -fsS -H "X-Panel-Token: ${PANEL_TOKEN}" "http://127.0.0.1:${HOST_PORT}/panel-api/health" >/dev/null
INFO_JSON="$(curl -fsS -H "X-Panel-Token: ${PANEL_TOKEN}" "http://127.0.0.1:${HOST_PORT}/panel-api/info")"
AGENTS_JSON="$(curl -fsS -H "X-Panel-Token: ${PANEL_TOKEN}" "http://127.0.0.1:${HOST_PORT}/panel-api/agents")"
RULES_JSON="$(curl -fsS -H "X-Panel-Token: ${PANEL_TOKEN}" "http://127.0.0.1:${HOST_PORT}/panel-api/agents/local/rules")"
CERTS_JSON="$(curl -fsS -H "X-Panel-Token: ${PANEL_TOKEN}" "http://127.0.0.1:${HOST_PORT}/panel-api/certificates")"
POLICIES_JSON="$(curl -fsS -H "X-Panel-Token: ${PANEL_TOKEN}" "http://127.0.0.1:${HOST_PORT}/panel-api/version-policies")"
JOIN_SCRIPT="$(curl -fsS "http://127.0.0.1:${HOST_PORT}/panel-api/public/join-agent.sh")"

echo "$INFO_JSON" | grep '"local_apply_runtime":"go-agent"' >/dev/null
echo "$AGENTS_JSON" | grep '"id":"local"' >/dev/null
echo "$AGENTS_JSON" | grep '"is_local":true' >/dev/null
echo "$RULES_JSON" | grep '"rules"' >/dev/null
echo "$CERTS_JSON" | grep '"certificates"' >/dev/null
echo "$POLICIES_JSON" | grep '"policies"' >/dev/null
echo "$JOIN_SCRIPT" | grep '/panel-api/public/agent-assets' >/dev/null

echo "pure-go master verification passed for ${DATA_DIR}"
