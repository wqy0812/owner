#!/usr/bin/env bash
# Build the current workspace and deploy it to the 192.168.88.55 test environment.
set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEFAULT_TARGET="root@192.168.88.55"
TARGET="${CLUSTERFORGE_DEPLOY_TARGET:-$DEFAULT_TARGET}"
SSH_PORT="${CLUSTERFORGE_DEPLOY_SSH_PORT:-22}"
SKIP_TESTS=false
ALLOW_ACTIVE_RUNS=false
REBUILD_V1_DB=false

usage() {
  cat <<'EOF'
Usage: ./scripts/deploy-test-88-55.sh [options]

Build and deploy the current workspace to the ClusterForge test environment.

Options:
  --target USER@HOST       SSH target (default: root@192.168.88.55)
  --ssh-port PORT          SSH port (default: 22)
  --skip-tests             Skip Go, frontend, and whitespace checks
  --allow-active-runs      Restart even when active platform runs exist
  --rebuild-v1-db          Back up, then rebuild the incompatible V1 test database
  -h, --help               Show this help

Environment variables:
  CLUSTERFORGE_DEPLOY_TARGET
  CLUSTERFORGE_DEPLOY_SSH_PORT
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)
      [[ $# -ge 2 ]] || die "--target requires USER@HOST"
      TARGET="$2"
      shift 2
      ;;
    --ssh-port)
      [[ $# -ge 2 ]] || die "--ssh-port requires a port"
      SSH_PORT="$2"
      shift 2
      ;;
    --skip-tests)
      SKIP_TESTS=true
      shift
      ;;
    --allow-active-runs)
      ALLOW_ACTIVE_RUNS=true
      shift
      ;;
    --rebuild-v1-db)
      REBUILD_V1_DB=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

[[ "$SSH_PORT" =~ ^[0-9]+$ ]] || die "SSH port must be numeric"

for command_name in git go make pnpm scp ssh; do
  command -v "$command_name" >/dev/null 2>&1 || die "required command not found: $command_name"
done

if command -v shasum >/dev/null 2>&1; then
  checksum_file() {
    shasum -a 256 "$1" | awk '{print $1}'
  }
elif command -v sha256sum >/dev/null 2>&1; then
  checksum_file() {
    sha256sum "$1" | awk '{print $1}'
  }
else
  die "required checksum command not found: shasum or sha256sum"
fi

cd "$PROJECT_ROOT"

if [[ "$SKIP_TESTS" == false ]]; then
  echo "==> Running deployment gates"
  go test ./...
  pnpm --dir web test -- --run
  git diff --check
else
  echo "==> Skipping deployment gates"
fi

artifact="$(mktemp "${TMPDIR:-/tmp}/clusterforge-platform-linux-amd64.XXXXXX")"
backup_artifact="$(mktemp "${TMPDIR:-/tmp}/clusterforge-backup-linux-amd64.XXXXXX")"
cleanup_local() {
  rm -f "$artifact" "$backup_artifact"
}
trap cleanup_local EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo "==> Building embedded frontend and Linux amd64 binary"
make build-web
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -tags embed -trimpath -o "$artifact" ./cmd/server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -o "$backup_artifact" ./cmd/backup
checksum="$(checksum_file "$artifact")"
backup_checksum="$(checksum_file "$backup_artifact")"
remote_artifact="/opt/clusterforge/platform/.clusterforge-platform.deploy-${checksum:0:12}-$$"
remote_backup_artifact="/opt/clusterforge/platform/.clusterforge-backup.deploy-${backup_checksum:0:12}-$$"
remote_backup_service="/opt/clusterforge/platform/.clusterforge-backup.service.deploy-$$"
remote_backup_timer="/opt/clusterforge/platform/.clusterforge-backup.timer.deploy-$$"

ssh_options=(-o BatchMode=yes -o ConnectTimeout=8 -p "$SSH_PORT")
scp_options=(-o BatchMode=yes -o ConnectTimeout=8 -P "$SSH_PORT")

echo "==> Checking remote deployment prerequisites on $TARGET"
ssh "${ssh_options[@]}" "$TARGET" 'set -eu
for command_name in awk curl flock git install python3 sha256sum systemctl; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "missing remote command: $command_name" >&2
    exit 1
  }
done
test -x /opt/clusterforge/platform/clusterforge-platform
test -f /var/lib/clusterforge/platform.db
systemctl is-active --quiet clusterforge-platform
'

echo "==> Uploading artifact $checksum"
scp "${scp_options[@]}" "$artifact" "$TARGET:$remote_artifact"
scp "${scp_options[@]}" "$backup_artifact" "$TARGET:$remote_backup_artifact"
scp "${scp_options[@]}" deploy/platform/clusterforge-backup.service "$TARGET:$remote_backup_service"
scp "${scp_options[@]}" deploy/platform/clusterforge-backup.timer "$TARGET:$remote_backup_timer"

allow_active_runs=0
if [[ "$ALLOW_ACTIVE_RUNS" == true ]]; then
  allow_active_runs=1
fi
rebuild_v1_db=0
if [[ "$REBUILD_V1_DB" == true ]]; then
  rebuild_v1_db=1
fi

echo "==> Activating release"
ssh "${ssh_options[@]}" "$TARGET" bash -s -- \
  "$remote_artifact" "$checksum" "$remote_backup_artifact" "$backup_checksum" "$remote_backup_service" "$remote_backup_timer" "$allow_active_runs" "$rebuild_v1_db" <<'REMOTE_SCRIPT'
set -Eeuo pipefail

staged_artifact="$1"
expected_checksum="$2"
staged_backup_artifact="$3"
expected_backup_checksum="$4"
staged_backup_service="$5"
staged_backup_timer="$6"
allow_active_runs="$7"
rebuild_v1_db="$8"
service_name="clusterforge-platform"
live_binary="/opt/clusterforge/platform/clusterforge-platform"
live_backup_binary="/opt/clusterforge/platform/clusterforge-backup"
database="/var/lib/clusterforge/platform.db"
backup_root="/var/lib/clusterforge/deploy-backups"
health_url="http://127.0.0.1:8080/"
service_touched=0
backup_ready=0
backup_dir=""
backup_timer_was_enabled=0
if systemctl is-enabled --quiet clusterforge-backup.timer 2>/dev/null; then
  backup_timer_was_enabled=1
fi

cleanup_staged() {
  rm -f "$staged_artifact" "$staged_backup_artifact" "$staged_backup_service" "$staged_backup_timer"
}

finish_failure() {
  rc="$1"
  trap - ERR EXIT INT TERM
  set +e
  if [[ "$service_touched" -eq 1 ]]; then
    systemctl stop "$service_name"
    if [[ "$backup_ready" -eq 1 ]]; then
      install -m 0755 "$backup_dir/clusterforge-platform" "$live_binary"
      if [[ -f "$backup_dir/clusterforge-backup" ]]; then
        install -m 0755 "$backup_dir/clusterforge-backup" "$live_backup_binary"
      else
        rm -f "$live_backup_binary"
      fi
      for unit in clusterforge-backup.service clusterforge-backup.timer; do
        if [[ -f "$backup_dir/$unit" ]]; then
          install -m 0644 "$backup_dir/$unit" "/etc/systemd/system/$unit"
        else
          rm -f "/etc/systemd/system/$unit"
        fi
      done
      systemctl daemon-reload
      if [[ "$backup_timer_was_enabled" -eq 1 ]]; then
        systemctl enable --now clusterforge-backup.timer
      else
        systemctl disable --now clusterforge-backup.timer >/dev/null 2>&1 || true
      fi
      rm -f "${database}-wal" "${database}-shm"
      cp -a "$backup_dir/platform.db" "$database"
    fi
    systemctl start "$service_name"
    echo "deployment failed; the previous service state was restored" >&2
    journalctl -u "$service_name" -n 40 --no-pager >&2 || true
  else
    echo "deployment failed before the service was changed" >&2
  fi
  cleanup_staged
  exit "$rc"
}

rollback_on_error() {
  finish_failure "$?"
}

trap cleanup_staged EXIT
trap rollback_on_error ERR
trap 'finish_failure 130' INT
trap 'finish_failure 143' TERM

exec 9>/var/lock/clusterforge-platform-deploy.lock
if ! flock -n 9; then
  echo "another ClusterForge deployment is already running" >&2
  exit 1
fi

actual_checksum="$(sha256sum "$staged_artifact" | awk '{print $1}')"
[[ "$actual_checksum" == "$expected_checksum" ]] || {
  echo "artifact checksum mismatch" >&2
  exit 1
}
actual_backup_checksum="$(sha256sum "$staged_backup_artifact" | awk '{print $1}')"
[[ "$actual_backup_checksum" == "$expected_backup_checksum" ]] || {
  echo "backup artifact checksum mismatch" >&2
  exit 1
}

active_runs="$(python3 - "$database" <<'PY'
import sqlite3
import sys

database = sys.argv[1]
connection = sqlite3.connect(f"file:{database}?mode=ro", uri=True)
rows = connection.execute(
    """
    SELECT id, status, created_at
    FROM runs
    WHERE status IN ('running', 'queued', 'awaiting_approval')
    ORDER BY created_at
    """
).fetchall()
for row in rows:
    print("\t".join(str(value) for value in row))
PY
)"

if [[ -n "$active_runs" && "$allow_active_runs" -ne 1 ]]; then
  echo "active runs block deployment:" >&2
  echo "$active_runs" >&2
  echo "wait for them to finish or rerun with --allow-active-runs" >&2
  exit 1
fi

predeploy_backup_enabled="$(awk -F= '$1=="CLUSTERFORGE_BACKUP_ENABLED" {print tolower($2)}' /etc/clusterforge/platform.env | tail -1)"
predeploy_backup_dir="$(awk -F= '$1=="CLUSTERFORGE_BACKUP_DIR" {sub(/^[^=]*=/,""); print}' /etc/clusterforge/platform.env | tail -1)"
predeploy_selection_file="${predeploy_backup_dir:-/var/lib/clusterforge/catalog-backups}/repository.json"
if [[ "$predeploy_backup_enabled" == "true" && -x "$live_backup_binary" && -f "$predeploy_selection_file" ]]; then
  "$live_backup_binary" snapshot --selected-repository --reason before-deploy
fi

stamp="$(date -u +%Y%m%dT%H%M%SZ)-$$"
backup_dir="$backup_root/$stamp"
mkdir -p "$backup_dir"
install -m 0755 "$live_binary" "$backup_dir/clusterforge-platform"
if [[ -x "$live_backup_binary" ]]; then
  install -m 0755 "$live_backup_binary" "$backup_dir/clusterforge-backup"
fi
for unit in clusterforge-backup.service clusterforge-backup.timer; do
  if [[ -f "/etc/systemd/system/$unit" ]]; then
    cp -a "/etc/systemd/system/$unit" "$backup_dir/$unit"
  fi
done

service_touched=1
systemctl stop "$service_name"
cp -a "$database" "$backup_dir/platform.db"
if [[ -f /etc/clusterforge/platform.env ]]; then
  cp -a /etc/clusterforge/platform.env "$backup_dir/platform.env"
fi
backup_ready=1

install -m 0755 "$staged_artifact" "$live_binary"
install -m 0755 "$staged_backup_artifact" "$live_backup_binary"
install -m 0644 "$staged_backup_service" /etc/systemd/system/clusterforge-backup.service
install -m 0644 "$staged_backup_timer" /etc/systemd/system/clusterforge-backup.timer
systemctl daemon-reload
if [[ "$rebuild_v1_db" -eq 1 ]]; then
  echo "rebuilding V1 test database after backup: $backup_dir/platform.db"
  rm -f "$database" "${database}-wal" "${database}-shm"
fi
systemctl start "$service_name"

ready=0
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if systemctl is-active --quiet "$service_name" && \
     curl -fsS --max-time 2 "$health_url" >/dev/null; then
    ready=1
    break
  fi
  sleep 1
done
[[ "$ready" -eq 1 ]] || {
  echo "service did not become ready within 15 seconds" >&2
  finish_failure 1
}

installed_checksum="$(sha256sum "$live_binary" | awk '{print $1}')"
[[ "$installed_checksum" == "$expected_checksum" ]]
installed_backup_checksum="$(sha256sum "$live_backup_binary" | awk '{print $1}')"
[[ "$installed_backup_checksum" == "$expected_backup_checksum" ]]

backup_enabled="$(awk -F= '$1=="CLUSTERFORGE_BACKUP_ENABLED" {print tolower($2)}' /etc/clusterforge/platform.env | tail -1)"
catalog_backup_dir="$(awk -F= '$1=="CLUSTERFORGE_BACKUP_DIR" {sub(/^[^=]*=/,""); print}' /etc/clusterforge/platform.env | tail -1)"
catalog_selection_file="${catalog_backup_dir:-/var/lib/clusterforge/catalog-backups}/repository.json"
if [[ "$backup_enabled" == "true" && -f "$catalog_selection_file" ]]; then
  systemctl enable --now clusterforge-backup.timer
else
  systemctl disable --now clusterforge-backup.timer >/dev/null 2>&1 || true
  echo "Catalog backup timer stopped; select a private repository through the Environment Owner UI first"
fi

python3 - "$database" <<'PY'
import sqlite3
import sys

database = sys.argv[1]
connection = sqlite3.connect(f"file:{database}?mode=ro", uri=True)
contract = connection.execute("SELECT version FROM schema_contract WHERE id=1").fetchone()
if contract != ("clusterforge-v1-20260828-environment-lifecycle",):
    raise SystemExit(f"unexpected schema contract: {contract!r}")
violations = connection.execute("PRAGMA foreign_key_check").fetchall()
if violations:
    raise SystemExit(f"foreign key violations: {violations!r}")
PY

if [[ "$rebuild_v1_db" -eq 1 && "$backup_enabled" == "true" && -f "$catalog_selection_file" ]]; then
  echo "creating recovery point for rebuilt V1 database"
  "$live_backup_binary" snapshot --selected-repository --reason after-v1-rebuild
fi

service_touched=0
trap - ERR INT TERM
cleanup_staged
trap - EXIT

echo "deployment succeeded"
echo "backup=$backup_dir"
echo "sha256=$installed_checksum"
echo "backup_sha256=$installed_backup_checksum"
systemctl show "$service_name" \
  -p ActiveState -p SubState -p MainPID -p ActiveEnterTimestamp --no-pager
curl -fsS --max-time 3 -o /dev/null -w 'http=%{http_code}\n' "$health_url"
REMOTE_SCRIPT

echo "==> Deployment complete on $TARGET (default URL: http://192.168.88.55:8080/)"
