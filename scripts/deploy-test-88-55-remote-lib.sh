#!/usr/bin/env bash

# Shared, sourceable policy and SQLite-backup helpers used by the guarded test
# deployment. Keep this file free of top-level side effects so its production
# behavior can be exercised without touching a service or deployment path.

clusterforge_read_schema_contract() {
  local database="$1"
  python3 - "$database" <<'PY'
import sqlite3
import sys

database = sys.argv[1]
try:
    connection = sqlite3.connect(f"file:{database}?mode=ro", uri=True)
    row = connection.execute("SELECT version FROM schema_contract WHERE id=1").fetchone()
except sqlite3.Error:
    print("__missing__")
    raise SystemExit(0)
finally:
    try:
        connection.close()
    except NameError:
        pass
print(row[0] if row is not None else "__missing__")
PY
}

clusterforge_assert_schema_policy() {
  local actual="$1"
  local expected="$2"
  local rebuild_v1_db="$3"
  if [[ "$actual" == "$expected" ]]; then
    return 0
  fi
  if [[ "$rebuild_v1_db" -eq 1 ]]; then
    return 0
  fi
  echo "unsupported schema contract: $actual" >&2
  echo "this build accepts only $expected; rerun with --rebuild-v1-db only after reviewing the destructive rebuild" >&2
  return 1
}

clusterforge_list_active_runs() {
  local database="$1"
  python3 - "$database" <<'PY'
import sqlite3
import sys

database = sys.argv[1]
connection = sqlite3.connect(f"file:{database}?mode=ro", uri=True)
try:
    rows = connection.execute(
        """
        SELECT id, status, created_at
        FROM runs
        WHERE status IN ('running', 'queued', 'awaiting_approval')
        ORDER BY created_at
        """
    ).fetchall()
finally:
    connection.close()
for row in rows:
    print("\t".join(str(value) for value in row))
PY
}

clusterforge_assert_active_run_policy() {
  local active_runs="$1"
  local allow_active_runs="$2"
  local rebuild_v1_db="$3"
  [[ -z "$active_runs" ]] && return 0
  if [[ "$rebuild_v1_db" -eq 1 ]]; then
    echo "active runs block deployment:" >&2
    echo "$active_runs" >&2
    echo "database rebuild never permits active Runs; wait for them to finish" >&2
    return 1
  fi
  if [[ "$allow_active_runs" -ne 1 ]]; then
    echo "active runs block deployment:" >&2
    echo "$active_runs" >&2
    echo "wait for them to finish or rerun with --allow-active-runs" >&2
    return 1
  fi
}

clusterforge_snapshot_if_configured() {
  local selection_file="$1"
  local backup_binary="$2"
  local callback="$3"
  local source="$4"
  [[ -f "$selection_file" ]] || return 0
  if [[ ! -x "$backup_binary" ]]; then
    echo "Catalog repository is configured but the current backup binary is unavailable" >&2
    return 1
  fi
  "$callback" "$source"
}

clusterforge_backup_sqlite() {
  local source="$1"
  local destination="$2"
  local mode="${3:-auto}"
  python3 - "$source" "$destination" "$mode" <<'PY'
import os
import shutil
import sqlite3
import sys

source, destination, mode = sys.argv[1:]
source_stat = os.stat(source)
try:
    source_connection = sqlite3.connect(f"file:{source}?mode=ro", uri=True)
    try:
        backup_method = getattr(source_connection, "backup", None)
        if mode == "auto" and backup_method is not None:
            destination_connection = sqlite3.connect(destination)
            try:
                backup_method(destination_connection)
            finally:
                destination_connection.close()
        else:
            # Python 3.6 does not expose sqlite3.Connection.backup(). The
            # deployer calls this helper only after systemd has stopped the
            # sole database writer, so checkpoint WAL before copying the file.
            source_connection.close()
            source_connection = sqlite3.connect(source, timeout=30)
            journal_mode = source_connection.execute("PRAGMA journal_mode").fetchone()
            if journal_mode and str(journal_mode[0]).lower() == "wal":
                checkpoint = source_connection.execute("PRAGMA wal_checkpoint(TRUNCATE)").fetchone()
                if checkpoint and checkpoint[0] != 0:
                    raise RuntimeError(f"SQLite WAL checkpoint remained busy: {checkpoint!r}")
            source_connection.close()
            source_connection = None
            shutil.copyfile(source, destination)
    finally:
        if source_connection is not None:
            source_connection.close()

    destination_connection = sqlite3.connect(f"file:{destination}?mode=ro", uri=True)
    try:
        integrity = destination_connection.execute("PRAGMA integrity_check").fetchall()
        if integrity != [("ok",)]:
            raise RuntimeError(f"SQLite backup integrity check failed: {integrity!r}")
        violations = destination_connection.execute("PRAGMA foreign_key_check").fetchall()
        if violations:
            raise RuntimeError(f"SQLite backup foreign key violations: {violations!r}")
    finally:
        destination_connection.close()

    os.chmod(destination, source_stat.st_mode & 0o7777)
    os.chown(destination, source_stat.st_uid, source_stat.st_gid)
except Exception:
    try:
        os.remove(destination)
    except FileNotFoundError:
        pass
    raise
PY
}
