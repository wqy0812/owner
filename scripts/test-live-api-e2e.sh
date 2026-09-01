#!/usr/bin/env bash
# Run Playwright against a real Go API backed by an isolated temporary database.
set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API_ADDRESS="${CLUSTERFORGE_LIVE_API_ADDR:-127.0.0.1:18081}"
[[ "$API_ADDRESS" =~ ^127\.0\.0\.1:([0-9]+)$ ]] || {
  echo "error: CLUSTERFORGE_LIVE_API_ADDR must use an isolated 127.0.0.1 port" >&2
  exit 1
}

for command_name in curl go pnpm; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "error: required command not found: $command_name" >&2
    exit 1
  }
done

test_root="$(mktemp -d "${TMPDIR:-/tmp}/clusterforge-live-api-e2e.XXXXXX")"
api_pid=""
cleanup() {
  if [[ -n "$api_pid" ]]; then
    kill "$api_pid" >/dev/null 2>&1 || true
    wait "$api_pid" >/dev/null 2>&1 || true
  fi
  chmod -R u+w "$test_root" >/dev/null 2>&1 || true
  rm -rf "$test_root"
}
trap cleanup EXIT INT TERM

cd "$PROJECT_ROOT"
NEWPLATFORM_ADDR="$API_ADDRESS" \
NEWPLATFORM_DB_PATH="$test_root/platform.db" \
NEWPLATFORM_RUN_ROOT="$test_root/runs" \
NEWPLATFORM_IMAGE_BUILD_ROOT="$test_root/image-builds" \
NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS="$PROJECT_ROOT/examples/ansible" \
NEWPLATFORM_ANSIBLE_BIN="false" \
NEWPLATFORM_SEED_PROFILE="catalog" \
CLUSTERFORGE_BACKUP_ENABLED="false" \
  go run ./cmd/server >"$test_root/api.log" 2>&1 &
api_pid="$!"

api_url="http://$API_ADDRESS"
ready=false
for _ in {1..40}; do
  if curl -fsS --max-time 1 "$api_url/api/v1/session/users" >/dev/null 2>&1; then
    ready=true
    break
  fi
  if ! kill -0 "$api_pid" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
if [[ "$ready" != true ]]; then
  echo "error: isolated Go API did not become ready" >&2
  sed -n '1,160p' "$test_root/api.log" >&2
  exit 1
fi

CI=1 LIVE_API=1 VITE_PROXY_TARGET="$api_url" pnpm --dir web exec playwright test
