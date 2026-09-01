#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=deploy-test-88-55-remote-lib.sh
source "$PROJECT_ROOT/scripts/deploy-test-88-55-remote-lib.sh"

test_root="$(mktemp -d "${TMPDIR:-/tmp}/clusterforge-deploy-test.XXXXXX")"
cleanup() {
  rm -rf "$test_root"
}
trap cleanup EXIT

database="$test_root/platform.db"
python3 - "$database" <<'PY'
import sqlite3
import sys

connection = sqlite3.connect(sys.argv[1])
connection.executescript("""
CREATE TABLE schema_contract(id INTEGER PRIMARY KEY, version TEXT NOT NULL);
INSERT INTO schema_contract VALUES(1, 'old-contract');
CREATE TABLE runs(id TEXT PRIMARY KEY, status TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE retained(id INTEGER PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO retained VALUES(1, 'kept');
""")
connection.commit()
connection.close()
PY

[[ "$(clusterforge_read_schema_contract "$database")" == "old-contract" ]]
if clusterforge_assert_schema_policy old-contract current-contract 0 2>/dev/null; then
  echo "old contract unexpectedly passed without --rebuild-v1-db" >&2
  exit 1
fi
clusterforge_assert_schema_policy old-contract current-contract 1

missing_database="$test_root/missing-contract.db"
python3 - "$missing_database" <<'PY'
import sqlite3
import sys
sqlite3.connect(sys.argv[1]).close()
PY
[[ "$(clusterforge_read_schema_contract "$missing_database")" == "__missing__" ]]
if clusterforge_assert_schema_policy __missing__ current-contract 0 2>/dev/null; then
  echo "missing contract unexpectedly passed without --rebuild-v1-db" >&2
  exit 1
fi
clusterforge_assert_schema_policy __missing__ current-contract 1

python3 - "$database" <<'PY'
import sqlite3
import sys
connection = sqlite3.connect(sys.argv[1])
connection.execute("INSERT INTO runs VALUES('run-active', 'running', '2026-09-01T00:00:00Z')")
connection.commit()
connection.close()
PY
active_runs="$(clusterforge_list_active_runs "$database")"
if clusterforge_assert_active_run_policy "$active_runs" 1 1 2>/dev/null; then
  echo "active Run unexpectedly passed destructive rebuild policy" >&2
  exit 1
fi
clusterforge_assert_active_run_policy "$active_runs" 1 0

selection="$test_root/repository.json"
backup_binary="$test_root/clusterforge-backup"
touch "$selection" "$backup_binary"
chmod 700 "$backup_binary"
snapshot_failure() { return 19; }
if clusterforge_snapshot_if_configured "$selection" "$backup_binary" snapshot_failure before-deploy; then
  echo "configured Catalog snapshot failure unexpectedly passed" >&2
  exit 1
fi
clusterforge_snapshot_if_configured "$test_root/not-configured.json" "$backup_binary" snapshot_failure before-deploy

backup="$test_root/backup/platform.db"
mkdir -p "$(dirname "$backup")"
clusterforge_backup_sqlite "$database" "$backup"
[[ "$(python3 - "$backup" <<'PY'
import sqlite3
import sys
connection = sqlite3.connect(sys.argv[1])
print(connection.execute("SELECT value FROM retained WHERE id=1").fetchone()[0])
connection.close()
PY
)" == "kept" ]]

failed_target="$test_root/failed-target"
mkdir "$failed_target"
if clusterforge_backup_sqlite "$database" "$failed_target" 2>/dev/null; then
  echo "invalid SQLite backup destination unexpectedly passed" >&2
  exit 1
fi
[[ -f "$database" ]]
[[ "$(python3 - "$database" <<'PY'
import sqlite3
import sys
connection = sqlite3.connect(sys.argv[1])
print(connection.execute("SELECT value FROM retained WHERE id=1").fetchone()[0])
connection.close()
PY
)" == "kept" ]]

echo "deploy rebuild policy tests passed"
