#!/usr/bin/env bash
# Run Playwright against a real Go API backed by an isolated temporary database.
set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API_ADDRESS="${CLUSTERFORGE_LIVE_API_ADDR:-127.0.0.1:18081}"
[[ "$API_ADDRESS" =~ ^127\.0\.0\.1:([0-9]+)$ ]] || {
  echo "error: CLUSTERFORGE_LIVE_API_ADDR must use an isolated 127.0.0.1 port" >&2
  exit 1
}

for command_name in curl go pnpm node python3; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "error: required command not found: $command_name" >&2
    exit 1
  }
done

api_url="http://$API_ADDRESS"
if curl -s --max-time 1 -o /dev/null "$api_url"; then
  echo "error: isolated API address is already serving HTTP: $API_ADDRESS" >&2
  exit 1
fi

test_root="$(mktemp -d "${TMPDIR:-/tmp}/clusterforge-live-api-e2e.XXXXXX")"
evidence_parent="${CLUSTERFORGE_TEST_EVIDENCE_ROOT:-$PROJECT_ROOT/output/playwright}"
mkdir -p "$evidence_parent"
evidence="$(mktemp -d "$evidence_parent/live-api-$(date -u +%Y%m%dT%H%M%SZ).XXXXXX")"
export CLUSTERFORGE_TEST_EVIDENCE_DIR="$evidence"
node "$PROJECT_ROOT/web/scripts/source-manifest.mjs" "$evidence/source-before.json"
{ node --version; pnpm --version; go version; } > "$evidence/runtime.log"
printf '%s\n' 'CI=1 LIVE_API=1 pnpm --dir web exec playwright test' > "$evidence/command.log"
printf 'Evidence: %s\n' "$evidence"
api_pid=""
cleanup() {
  result=$?
  trap - EXIT
  set +e
  if [[ -n "$api_pid" ]]; then
    kill "$api_pid" >/dev/null 2>&1 || true
    wait "$api_pid" >/dev/null 2>&1 || true
  fi
  if [[ -f "$test_root/api.log" ]]; then cp "$test_root/api.log" "$evidence/api.log"; fi
  node "$PROJECT_ROOT/web/scripts/source-manifest.mjs" "$evidence/source-after.json" || result=1
  python3 - "$evidence" "$result" <<'PY_RESULT'
import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
code = int(sys.argv[2])
before = json.loads((root/'source-before.json').read_text())
after = json.loads((root/'source-after.json').read_text())
unchanged = before['sourceSha256'] == after['sourceSha256']
(root/'result.json').write_text(json.dumps({'testExitCode': code, 'exitCode': code or (0 if unchanged else 1), 'sourceUnchanged': unchanged}, indent=2)+'\n')
if not unchanged: print('error: source changed during browser tests', file=sys.stderr)
sys.exit(code or (0 if unchanged else 1))
PY_RESULT
  result=$?
  chmod -R u+w "$test_root" >/dev/null 2>&1 || true
  rm -rf "$test_root"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

cd "$PROJECT_ROOT"
CLUSTERFORGE_LIVE_API_FIXTURE="$test_root/fixture" \
  go test ./internal/api -run '^TestLiveAPIComponentFixture$' -count=1 >"$evidence/fixture.log" 2>&1
# Run the built binary directly: killing `go run` leaves its server child alive
# with an unlinked database, and later tests can accidentally connect to it.
go build -o "$test_root/server" ./cmd/server >"$evidence/build.log" 2>&1
NEWPLATFORM_ADDR="$API_ADDRESS" \
NEWPLATFORM_DB_PATH="$test_root/fixture/platform.db" \
NEWPLATFORM_RUN_ROOT="$test_root/runs" \
NEWPLATFORM_IMAGE_BUILD_ROOT="$test_root/image-builds" \
NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS="$test_root/fixture/playbooks" \
NEWPLATFORM_ANSIBLE_BIN="false" \
NEWPLATFORM_SEED_PROFILE="catalog" \
CLUSTERFORGE_BACKUP_ENABLED="false" \
  "$test_root/server" >"$test_root/api.log" 2>&1 &
api_pid="$!"

ready=false
for _ in {1..40}; do
  if ! kill -0 "$api_pid" >/dev/null 2>&1; then
    break
  fi
  if curl -fsS --max-time 1 "$api_url/api/v1/session/users" >/dev/null 2>&1; then
    ready=true
    break
  fi
  if ! kill -0 "$api_pid" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
if [[ "$ready" != true ]] || ! kill -0 "$api_pid" >/dev/null 2>&1; then
  echo "error: isolated Go API did not become ready" >&2
  sed -n '1,160p' "$test_root/api.log" >&2
  exit 1
fi

CI=1 LIVE_API=1 VITE_PROXY_TARGET="$api_url" pnpm --dir web exec playwright test
