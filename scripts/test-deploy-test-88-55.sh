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
CLUSTERFORGE_DEPLOY_DB_TOOL="$test_root/database-tool"
go build -o "$CLUSTERFORGE_DEPLOY_DB_TOOL" "$PROJECT_ROOT/cmd/backup"

if removed_option_output="$("$PROJECT_ROOT/scripts/deploy-test-88-55.sh" --migrate-container-runtime 2>&1)"; then
  echo "removed migration option unexpectedly passed" >&2
  exit 1
fi
[[ "$removed_option_output" == *"unknown option: --migrate-container-runtime"* ]]

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

wal_database="$test_root/source-wal.db"
wal_backup="$test_root/backup/platform-wal.db"
python3 - "$wal_database" <<'PY_WAL'
import os
import sqlite3
import sys
connection = sqlite3.connect(sys.argv[1])
connection.execute("PRAGMA journal_mode=WAL")
connection.execute("PRAGMA wal_autocheckpoint=0")
connection.execute("CREATE TABLE retained(value TEXT)")
connection.execute("INSERT INTO retained VALUES('committed-in-wal')")
connection.commit()
# Keep the committed WAL on disk so the backup must include it.
os._exit(0)
PY_WAL
[[ -s "${wal_database}-wal" ]]
clusterforge_backup_sqlite "$wal_database" "$wal_backup"
python3 - "$wal_backup" <<'PY_WAL'
import sqlite3
import sys
connection = sqlite3.connect(sys.argv[1])
assert connection.execute("SELECT value FROM retained").fetchone() == ("committed-in-wal",)
assert connection.execute("PRAGMA integrity_check").fetchall() == [("ok",)]
connection.close()
PY_WAL

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

# Exercise the current schema through the same embedded SQLite snapshot path used
# by deployment; both generated values and indexes must survive the snapshot.
current_database="$test_root/current.db"
current_backup="$test_root/backup/current.db"
python3 - "$PROJECT_ROOT/internal/store/schema.sql" "$current_database" <<'PY_CURRENT'
import json
import pathlib
import sqlite3
import sys

connection = sqlite3.connect(sys.argv[2])
connection.executescript(pathlib.Path(sys.argv[1]).read_text())
stamp = '2026-09-03T00:00:00Z'
connection.execute("INSERT INTO users(id,name,role,created_at) VALUES('owner','Owner','component_owner',?)", (stamp,))
connection.execute("INSERT INTO users(id,name,role,created_at) VALUES('environment-owner','Environment owner','environment_owner',?)", (stamp,))
connection.execute("INSERT INTO components(id,slug,name,layer,owner_id,created_at,updated_at) VALUES('component','component','Component','runtime_state','owner',?,?)", (stamp, stamp))
connection.execute("INSERT INTO component_release_lines(id,component_id,name,created_at) VALUES('line','component','Line',?)", (stamp,))
connection.execute("INSERT INTO component_releases(id,component_id,line_id,version,status,compatibility,created_at) VALUES('release','component','line','1.0.0','draft','not_applicable',?)", (stamp,))
connection.execute("INSERT INTO environments(id,name,owner_id,created_at,updated_at) VALUES('environment','Environment','environment-owner',?,?)", (stamp, stamp))
connection.execute("INSERT INTO environment_revisions(id,environment_id,revision,created_at) VALUES('revision','environment',1,?)", (stamp,))
snapshot = json.dumps({'componentReleaseSpecDigest': 'contract', 'componentTestEvidence': 'install_verify', 'runtimeCompatibility': {'runtime': 'runtime-a', 'version': 'version-a'}})
connection.execute("INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,input_snapshot_json,created_at) VALUES('run','component_test','succeeded','owner','environment','revision','release',?,?)", (snapshot, stamp))
connection.commit()
connection.close()
PY_CURRENT
clusterforge_backup_sqlite "$current_database" "$current_backup"
python3 - "$current_backup" <<'PY_VERIFY'
import sqlite3
import sys

connection = sqlite3.connect(sys.argv[1])
assert connection.execute("SELECT component_spec_digest,component_evidence_kind,runtime_name,runtime_version,evidence_at FROM runs WHERE id='run'").fetchone() == ('contract', 'install_verify', 'runtime-a', 'version-a', '2026-09-03T00:00:00Z')
for name in ('idx_runs_component_evidence', 'idx_runs_runtime_evidence', 'idx_runs_active_component', 'idx_actions_release', 'idx_run_steps_run'):
    assert connection.execute("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", (name,)).fetchone() == (1,)
assert connection.execute("PRAGMA integrity_check").fetchall() == [('ok',)]
connection.close()
PY_VERIFY

echo "deploy rebuild policy tests passed"
