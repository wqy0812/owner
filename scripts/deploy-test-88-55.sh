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
MIGRATE_USER_EXPERIENCE=false
INITIALIZE_EMPTY_DB=false
DISABLE_CATALOG_BACKUP=false
BUSINESS_RESET_DIR=""

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
  --migrate-user-experience Legacy migration gate (unavailable for the current schema)
  --initialize-empty-db    Initialize an absent database while the service is stopped
  --disable-catalog-backup Deploy with Git Catalog backup explicitly disabled
  --business-reset-dir DIR Use a verified, copied business-only reset bundle
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
    --disable-catalog-backup)
      DISABLE_CATALOG_BACKUP=true
      shift
      ;;
    --business-reset-dir)
      [[ $# -ge 2 ]] || die "--business-reset-dir requires a remote directory"
      BUSINESS_RESET_DIR="$2"
      [[ "$BUSINESS_RESET_DIR" =~ ^/var/lib/clusterforge/business-reset-backups/[A-Za-z0-9_-]+$ ]] || die "invalid business reset directory"
      REBUILD_V1_DB=true
      DISABLE_CATALOG_BACKUP=true
      shift 2
      ;;
    --migrate-user-experience)
      MIGRATE_USER_EXPERIENCE=true
      shift
      ;;
    --rebuild-v1-db)
      REBUILD_V1_DB=true
      shift
      ;;
    --initialize-empty-db)
      INITIALIZE_EMPTY_DB=true
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
if [[ "$INITIALIZE_EMPTY_DB" == true && ( "$REBUILD_V1_DB" == true || "$ALLOW_ACTIVE_RUNS" == true ) ]]; then
  die "--initialize-empty-db cannot be combined with rebuild or active-Run overrides"
fi

if [[ "$MIGRATE_USER_EXPERIENCE" == true && ( "$REBUILD_V1_DB" == true || "$ALLOW_ACTIVE_RUNS" == true || "$INITIALIZE_EMPTY_DB" == true ) ]]; then
  die "--migrate-user-experience cannot combine with reset, initialization, or active-Run overrides"
fi

for command_name in git go make pnpm python3 scp ssh; do
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

# Keep tests and delivered artifacts bound to the same checkout, including
# untracked source files, while other tasks may be editing this workspace.
workspace_checksum() {
  python3 - <<'PY_WORKSPACE'
import hashlib, os, pathlib, subprocess
digest = hashlib.sha256()
paths = subprocess.check_output(['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard'])
for name in sorted(set(paths.split(b'\0')) - {b''}):
    path = pathlib.Path(os.fsdecode(name))
    digest.update(name + b'\0')
    if not path.exists() and not path.is_symlink():
        digest.update(b'deleted\0')
        continue
    digest.update(str(path.lstat().st_mode).encode() + b'\0')
    digest.update(os.fsencode(os.readlink(path)) if path.is_symlink() else path.read_bytes())
    digest.update(b'\0')
print(digest.hexdigest())
PY_WORKSPACE
}
source_checksum="$(workspace_checksum)"
assert_workspace_unchanged() {
  [[ "$(workspace_checksum)" == "$source_checksum" ]] || die "workspace changed during deployment checks; rerun after edits finish"
}

echo "==> Building embedded frontend"
make build-web
ui_index_checksum="$(checksum_file web/dist/index.html)"
ui_version_checksum="$(checksum_file web/dist/version.json)"

if [[ "$SKIP_TESTS" == false ]]; then
  echo "==> Running deployment gates"
  ./scripts/test-deploy-test-88-55.sh
  go test ./...
  pnpm --dir web test:coverage
  ./scripts/test-live-api-e2e.sh
  git diff --check
else
  echo "==> Skipping deployment gates"
fi
assert_workspace_unchanged

artifact="$(mktemp "${TMPDIR:-/tmp}/clusterforge-platform-linux-amd64.XXXXXX")"
backup_artifact="$(mktemp "${TMPDIR:-/tmp}/clusterforge-backup-linux-amd64.XXXXXX")"
job_artifact="$(mktemp "${TMPDIR:-/tmp}/clusterforge-job-linux-amd64.XXXXXX")"
remote_helper_source="$PROJECT_ROOT/scripts/deploy-test-88-55-remote-lib.sh"
cleanup_local() {
  rm -f "$artifact" "$backup_artifact" "$job_artifact"
}
trap cleanup_local EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo "==> Building Linux amd64 binaries"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -tags embed -trimpath -o "$artifact" ./cmd/server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -o "$backup_artifact" ./cmd/backup
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -o "$job_artifact" ./cmd/clusterforge-job
assert_workspace_unchanged
checksum="$(checksum_file "$artifact")"
backup_checksum="$(checksum_file "$backup_artifact")"
job_checksum="$(checksum_file "$job_artifact")"
remote_helper_checksum="$(checksum_file "$remote_helper_source")"
remote_artifact="/opt/clusterforge/platform/.clusterforge-platform.deploy-${checksum:0:12}-$$"
remote_backup_artifact="/opt/clusterforge/platform/.clusterforge-backup.deploy-${backup_checksum:0:12}-$$"
remote_job_artifact="/opt/clusterforge/platform/.clusterforge-job.deploy-${job_checksum:0:12}-$$"
remote_helper="/opt/clusterforge/platform/.deploy-remote-lib-${remote_helper_checksum:0:12}-$$.sh"

ssh_options=(-o BatchMode=yes -o ConnectTimeout=8 -p "$SSH_PORT")
scp_options=(-o BatchMode=yes -o ConnectTimeout=8 -P "$SSH_PORT")

disable_catalog_backup=0
[[ "$DISABLE_CATALOG_BACKUP" != true ]] || disable_catalog_backup=1
initialize_empty_db=0
[[ "$INITIALIZE_EMPTY_DB" != true ]] || initialize_empty_db=1

echo "==> Checking remote deployment prerequisites on $TARGET"
ssh "${ssh_options[@]}" "$TARGET" bash -s -- "$disable_catalog_backup" "$initialize_empty_db" <<'PREFLIGHT'
set -eu
for command_name in awk curl flock git grep install sha256sum systemctl; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "missing remote command: $command_name" >&2
    exit 1
  }
done
test -x /opt/clusterforge/platform/clusterforge-platform
if [ "$2" = 1 ]; then
  test "$(systemctl show clusterforge-platform -p ActiveState --value)" = inactive
  for path in /var/lib/clusterforge/platform.db{,-wal,-shm,-journal}; do
    test ! -e "$path" && test ! -L "$path"
  done
else
  test -f /var/lib/clusterforge/platform.db
fi
test -f /etc/clusterforge/platform.env
backup_enabled="$(awk -F= '$1=="CLUSTERFORGE_BACKUP_ENABLED" {enabled=tolower($2)} END {print enabled}' /etc/clusterforge/platform.env)"
if [ "$backup_enabled" != "true" ] && [ "$1" != 1 ]; then
  echo "Catalog backup capability is disabled; set CLUSTERFORGE_BACKUP_ENABLED=true before deployment so the Environment Owner UI can configure a repository" >&2
  exit 1
fi
ansible_binary="$(awk -F= '$1=="NEWPLATFORM_ANSIBLE_BIN" {sub(/^[^=]*=/,""); value=$0} END {print value}' /etc/clusterforge/platform.env)"
if [ -z "$ansible_binary" ]; then
  ansible_binary="ansible-playbook"
fi
command -v "$ansible_binary" >/dev/null 2>&1 || {
  echo "configured Ansible executable is unavailable: $ansible_binary" >&2
  exit 1
}
PREFLIGHT

if [[ -n "$BUSINESS_RESET_DIR" || "$INITIALIZE_EMPTY_DB" == true ]]; then
  ssh "${ssh_options[@]}" "$TARGET" '! systemctl is-active --quiet clusterforge-platform'
else
  ssh "${ssh_options[@]}" "$TARGET" 'systemctl is-active --quiet clusterforge-platform'
fi

echo "==> Uploading artifact $checksum"
scp "${scp_options[@]}" "$artifact" "$TARGET:$remote_artifact"
scp "${scp_options[@]}" "$backup_artifact" "$TARGET:$remote_backup_artifact"
scp "${scp_options[@]}" "$job_artifact" "$TARGET:$remote_job_artifact"
scp "${scp_options[@]}" "$remote_helper_source" "$TARGET:$remote_helper"

allow_active_runs=0
if [[ "$ALLOW_ACTIVE_RUNS" == true ]]; then
  allow_active_runs=1
fi
rebuild_v1_db=0
if [[ "$REBUILD_V1_DB" == true ]]; then
  rebuild_v1_db=1
fi
migrate_user_experience=0
[[ "$MIGRATE_USER_EXPERIENCE" != true ]] || migrate_user_experience=1
echo "==> Activating release"
# SSH sends one command string through the remote shell. Quote every argument
# so the optional empty reset directory does not shift the following arguments.
printf -v activate_command '%q ' bash -s -- \
  "$remote_artifact" "$checksum" "$remote_backup_artifact" "$backup_checksum" "$remote_helper" "$remote_helper_checksum" "$allow_active_runs" "$rebuild_v1_db" "$ui_index_checksum" "$ui_version_checksum" "$disable_catalog_backup" "$BUSINESS_RESET_DIR" "$remote_job_artifact" "$job_checksum" "$initialize_empty_db" "$migrate_user_experience"
ssh "${ssh_options[@]}" "$TARGET" "$activate_command" <<'REMOTE_SCRIPT'
set -Eeuo pipefail

staged_artifact="$1"
expected_checksum="$2"
staged_backup_artifact="$3"
expected_backup_checksum="$4"
staged_remote_helper="$5"
expected_remote_helper_checksum="$6"
allow_active_runs="$7"
rebuild_v1_db="$8"
expected_ui_index_checksum="$9"
expected_ui_version_checksum="${10}"
disable_catalog_backup="${11}"
business_reset_dir="${12}"
staged_job_artifact="${13}"
expected_job_checksum="${14}"
initialize_empty_db="${15}"
migrate_user_experience="${16}"
service_name="clusterforge-platform"
live_binary="/opt/clusterforge/platform/clusterforge-platform"
live_backup_binary="/opt/clusterforge/platform/clusterforge-backup"
live_job_binary="/opt/clusterforge/platform/clusterforge-job"
database="/var/lib/clusterforge/platform.db"
expected_schema_contract="clusterforge-v1-20260906-workbench-run-observations"
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
  rm -f "$staged_artifact" "$staged_backup_artifact" "$staged_remote_helper" "$staged_job_artifact"
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
      if [[ -f "$backup_dir/clusterforge-job" ]]; then
        install -m 0755 "$backup_dir/clusterforge-job" "$live_job_binary"
      else
        rm -f "$live_job_binary"
      fi
      for unit in clusterforge-backup.service clusterforge-backup.timer; do
        if [[ -f "$backup_dir/$unit" ]]; then
          install -m 0644 "$backup_dir/$unit" "/etc/systemd/system/$unit"
        else
          rm -f "/etc/systemd/system/$unit"
        fi
      done
      systemctl daemon-reload
      if [[ "$backup_timer_was_enabled" -eq 1 && "$disable_catalog_backup" -eq 0 ]]; then
        systemctl enable --now clusterforge-backup.timer
      else
        systemctl disable --now clusterforge-backup.timer >/dev/null 2>&1 || true
      fi
      if [[ -f "$backup_dir/platform.env" ]]; then
        cp -a "$backup_dir/platform.env" /etc/clusterforge/platform.env
      fi
      rm -f "${database}-wal" "${database}-shm" "${database}-journal"
      if [[ "$initialize_empty_db" -eq 1 ]]; then
        rm -f "$database"
      else
        cp -a "$backup_dir/platform.db" "$database"
      fi
    fi
    if [[ "$initialize_empty_db" -ne 1 ]]; then
      systemctl start "$service_name"
    fi
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
actual_job_checksum="$(sha256sum "$staged_job_artifact" | awk '{print $1}')"
[[ "$actual_job_checksum" == "$expected_job_checksum" ]] || {
  echo "job executable checksum mismatch" >&2
  exit 1
}
actual_remote_helper_checksum="$(sha256sum "$staged_remote_helper" | awk '{print $1}')"
[[ "$actual_remote_helper_checksum" == "$expected_remote_helper_checksum" ]] || {
  echo "remote deployment helper checksum mismatch" >&2
  exit 1
}
# shellcheck source=deploy-test-88-55-remote-lib.sh
source "$staged_remote_helper"
CLUSTERFORGE_DEPLOY_DB_TOOL="$staged_backup_artifact"

if [[ "$initialize_empty_db" -eq 1 ]]; then
  clusterforge_assert_empty_database "$database" "$(systemctl show "$service_name" -p ActiveState --value)"
else
  active_runs="$(clusterforge_list_active_runs "$database")"
  predeploy_schema_contract="$(clusterforge_read_schema_contract "$database")"
  clusterforge_assert_schema_policy "$predeploy_schema_contract" "$expected_schema_contract" "$rebuild_v1_db" "$migrate_user_experience"
  clusterforge_assert_active_run_policy "$active_runs" "$allow_active_runs" "$rebuild_v1_db"
fi

platform_env_value() {
  awk -F= -v key="$1" '$1==key {sub(/^[^=]*=/,""); value=$0} END {print value}' /etc/clusterforge/platform.env
}

snapshot_environment=()
for key in \
  NEWPLATFORM_DB_PATH \
  NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS \
  NEWPLATFORM_RUN_ROOT \
  NEWPLATFORM_ANSIBLE_BIN \
  NEWPLATFORM_KILL_GRACE \
  NEWPLATFORM_MAX_LOG_BYTES \
  CLUSTERFORGE_BACKUP_DIR \
  CLUSTERFORGE_RUN_ARCHIVE_DIR \
  CLUSTERFORGE_CATALOG_REPO \
  CLUSTERFORGE_CATALOG_REMOTE \
  CLUSTERFORGE_CATALOG_BRANCH; do
  value="$(platform_env_value "$key")"
  if [[ "$key" == "NEWPLATFORM_DB_PATH" && -z "$value" ]]; then
    value="$database"
  fi
  if [[ -n "$value" ]]; then
    snapshot_environment+=("$key=$value")
  fi
done

run_automation_snapshot() {
  local source="$1"
  local output=""
  local attempt=1
  while [[ "$attempt" -le 31 ]]; do
    if output="$(env "${snapshot_environment[@]}" "$live_backup_binary" automation-snapshot --source "$source" 2>&1)"; then
      printf '%s\n' "$output"
      return 0
    fi
    if [[ "$output" != *"another ClusterForge backup is already running"* || "$attempt" -eq 31 ]]; then
      printf '%s\n' "$output" >&2
      return 1
    fi
    sleep 2
    attempt=$((attempt + 1))
  done
}

predeploy_backup_enabled="$(awk -F= '$1=="CLUSTERFORGE_BACKUP_ENABLED" {print tolower($2)}' /etc/clusterforge/platform.env | tail -1)"
predeploy_backup_dir="$(awk -F= '$1=="CLUSTERFORGE_BACKUP_DIR" {sub(/^[^=]*=/,""); print}' /etc/clusterforge/platform.env | tail -1)"
predeploy_selection_file="${predeploy_backup_dir:-/var/lib/clusterforge/catalog-backups}/repository.json"
if [[ "$initialize_empty_db" -eq 0 && "$predeploy_backup_enabled" == "true" && "$disable_catalog_backup" -eq 0 ]]; then
  clusterforge_snapshot_if_configured "$predeploy_selection_file" "$live_backup_binary" run_automation_snapshot before-deploy
fi

if [[ -n "$business_reset_dir" ]]; then
  [[ "$business_reset_dir" =~ ^/var/lib/clusterforge/business-reset-backups/[A-Za-z0-9_-]+$ ]]
  [[ ! -L "$business_reset_dir" && -f "$business_reset_dir/local-copy.verified" ]]
  ! systemctl is-active --quiet "$service_name"
  [[ "$(cat "$business_reset_dir/local-copy.verified")" == "$(sha256sum "$business_reset_dir/SHA256SUMS" | awk '{print $1}')" ]]
  (cd "$business_reset_dir" && sha256sum --quiet -c SHA256SUMS)
  sha256sum --quiet -c "$business_reset_dir/source-database.sha256"
  "$CLUSTERFORGE_DEPLOY_DB_TOOL" database verify --db "$business_reset_dir/platform.db" --expected-contract "$predeploy_schema_contract"
  "$CLUSTERFORGE_DEPLOY_DB_TOOL" database verify --db "$business_reset_dir/foundation.db" --expected-contract "$expected_schema_contract"
  "$CLUSTERFORGE_DEPLOY_DB_TOOL" database verify-business --db "$business_reset_dir/platform.db"
  "$CLUSTERFORGE_DEPLOY_DB_TOOL" database verify-foundation --db "$business_reset_dir/foundation.db"
  backup_dir="$business_reset_dir"
  backup_ready=1
  service_touched=1
else
  stamp="$(date -u +%Y%m%dT%H%M%SZ)-$$"
  backup_dir="$backup_root/$stamp"
  mkdir -p -m 0700 "$backup_dir"
  install -m 0755 "$live_binary" "$backup_dir/clusterforge-platform"
  if [[ -x "$live_backup_binary" ]]; then
    install -m 0755 "$live_backup_binary" "$backup_dir/clusterforge-backup"
  fi
  if [[ -x "$live_job_binary" ]]; then
    install -m 0755 "$live_job_binary" "$backup_dir/clusterforge-job"
  fi
  for unit in clusterforge-backup.service clusterforge-backup.timer; do
    if [[ -f "/etc/systemd/system/$unit" ]]; then
      cp -a "/etc/systemd/system/$unit" "$backup_dir/$unit"
    fi
  done
  if [[ "$initialize_empty_db" -eq 1 ]]; then
    clusterforge_assert_empty_database "$database" "$(systemctl show "$service_name" -p ActiveState --value)"
    touch "$backup_dir/database.absent"
  else
    service_touched=1
    systemctl stop "$service_name"
    active_runs="$(clusterforge_list_active_runs "$database")"
    clusterforge_assert_active_run_policy "$active_runs" "$allow_active_runs" "$rebuild_v1_db"
    clusterforge_backup_sqlite "$database" "$backup_dir/platform.db"
    archive_root="$(platform_env_value CLUSTERFORGE_RUN_ARCHIVE_DIR)"
    if [[ -n "$archive_root" ]]; then
      "$CLUSTERFORGE_DEPLOY_DB_TOOL" database history-snapshot --db "$database" --archive-dir "$archive_root" --target "$backup_dir/run-history"
      "$CLUSTERFORGE_DEPLOY_DB_TOOL" database history-verify --db "$backup_dir/run-history"
    fi
  fi
  if [[ -f /etc/clusterforge/platform.env ]]; then
    cp -a /etc/clusterforge/platform.env "$backup_dir/platform.env"
  fi
  backup_ready=1
  service_touched=1
fi

if [[ "$migrate_user_experience" -eq 1 && "$predeploy_schema_contract" != "$expected_schema_contract" ]]; then
  # Conversion works on a new copy and proves old rows and workspace contents
  # unchanged. It never reseeds, clears history, or synthesizes declarations.
  migration_active_work="$("$CLUSTERFORGE_DEPLOY_DB_TOOL" database active-work --db "$database")"
  [[ -z "$migration_active_work" ]] || { echo "active work blocks migration" >&2; exit 1; }
  source_playbooks="$(platform_env_value NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS)"
  [[ -n "$source_playbooks" && "$source_playbooks" == /* && "$source_playbooks" != *:* ]] || { echo "migration requires one absolute managed Playbook root" >&2; exit 1; }
  migrated_playbooks="${source_playbooks%/}-ux-${stamp}"
  migrated_database="$backup_dir/migrated-platform.db"
  "$CLUSTERFORGE_DEPLOY_DB_TOOL" database convert-user-experience --db "$database" --target "$migrated_database" --source-playbook-root "$source_playbooks" --target-playbook-root "$migrated_playbooks"
  "$CLUSTERFORGE_DEPLOY_DB_TOOL" database verify --db "$migrated_database" --expected-contract "$expected_schema_contract"
  rm -f "${database}-wal" "${database}-shm" "${database}-journal"
  install -m 0600 "$migrated_database" "$database"
  pending_config="$backup_dir/platform.env.migrated"
  awk '!/^NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS=/' /etc/clusterforge/platform.env > "$pending_config"
  printf '\nNEWPLATFORM_ALLOWED_ANSIBLE_ROOTS=%s\n' "$migrated_playbooks" >> "$pending_config"
  install -m 0600 "$pending_config" /etc/clusterforge/platform.env
  echo "preserved-history migration completed; workspace=$migrated_playbooks"
fi

if [[ "$disable_catalog_backup" -eq 1 ]]; then
  # Preserve all unrelated environment entries and never echo secret values.
  sed -i '/^CLUSTERFORGE_BACKUP_ENABLED=/d' /etc/clusterforge/platform.env
  printf '\nCLUSTERFORGE_BACKUP_ENABLED=false\n' >> /etc/clusterforge/platform.env
fi

if [[ -n "$business_reset_dir" ]]; then
  clusterforge_prepare_reset_environment /etc/clusterforge/platform.env
fi

install -m 0755 "$staged_artifact" "$live_binary"
install -m 0755 "$staged_backup_artifact" "$live_backup_binary"
install -m 0755 "$staged_job_artifact" "$live_job_binary"
systemctl disable --now clusterforge-backup.timer >/dev/null 2>&1 || true
systemctl stop clusterforge-backup.service >/dev/null 2>&1 || true
rm -f /etc/systemd/system/clusterforge-backup.timer /etc/systemd/system/clusterforge-backup.service
systemctl daemon-reload
if [[ "$rebuild_v1_db" -eq 1 ]]; then
  echo "rebuilding V1 test database after backup: $backup_dir/platform.db"
  rm -f "$database" "${database}-wal" "${database}-shm"
  if [[ -n "$business_reset_dir" ]]; then
    install -m 0600 "$business_reset_dir/foundation.db" "$database"
  fi
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
served_ui_index_checksum="$(curl -fsS --max-time 3 "$health_url" | sha256sum | awk '{print $1}')"
[[ "$served_ui_index_checksum" == "$expected_ui_index_checksum" ]]
served_ui_version_checksum="$(curl -fsS --max-time 3 "${health_url}version.json" | sha256sum | awk '{print $1}')"
[[ "$served_ui_version_checksum" == "$expected_ui_version_checksum" ]]

backup_enabled="$(awk -F= '$1=="CLUSTERFORGE_BACKUP_ENABLED" {print tolower($2)}' /etc/clusterforge/platform.env | tail -1)"
catalog_backup_dir="$(awk -F= '$1=="CLUSTERFORGE_BACKUP_DIR" {sub(/^[^=]*=/,""); print}' /etc/clusterforge/platform.env | tail -1)"
catalog_selection_file="${catalog_backup_dir:-/var/lib/clusterforge/catalog-backups}/repository.json"
if [[ "$disable_catalog_backup" -eq 0 && "$backup_enabled" != "true" ]]; then
  echo "Catalog backup capability became disabled during deployment; refusing to leave an unusable Environment Owner repository workflow" >&2
  finish_failure 1
fi

"$CLUSTERFORGE_DEPLOY_DB_TOOL" database verify --db "$database" --expected-contract "$expected_schema_contract"
if [[ "$disable_catalog_backup" -eq 1 ]]; then
  [[ "$backup_enabled" == "false" ]]
fi


if [[ "$rebuild_v1_db" -eq 1 && "$disable_catalog_backup" -eq 0 && "$backup_enabled" == "true" && -f "$catalog_selection_file" ]]; then
  echo "creating recovery point for rebuilt V1 database"
  run_automation_snapshot after-v1-rebuild
fi
service_touched=0
trap - ERR INT TERM
cleanup_staged
trap - EXIT

echo "deployment succeeded"
echo "backup=$backup_dir"
echo "sha256=$installed_checksum"
echo "backup_sha256=$installed_backup_checksum"
echo "ui_index_sha256=$served_ui_index_checksum"
echo "ui_version_sha256=$served_ui_version_checksum"
systemctl show "$service_name" \
  -p ActiveState -p SubState -p MainPID -p ActiveEnterTimestamp --no-pager
curl -fsS --max-time 3 -o /dev/null -w 'http=%{http_code}\n' "$health_url"
REMOTE_SCRIPT

echo "==> Deployment complete on $TARGET (default URL: http://192.168.88.55:8080/)"
