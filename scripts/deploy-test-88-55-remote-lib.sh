#!/usr/bin/env bash

# Shared, sourceable policy and SQLite-backup helpers used by the guarded test
# deployment. Keep this file free of top-level side effects so its production
# behavior can be exercised without touching a service or deployment path.

# A foundation reset must not seed example business definitions on startup.
clusterforge_prepare_reset_environment() {
  local config="$1"
  local pending
  pending="$(mktemp "${config}.reset.XXXXXX")" || return 1
  if ! cp -p "$config" "$pending" ||
     ! awk '!/^NEWPLATFORM_SEED_PROFILE=/ && !/^CLUSTERFORGE_BACKUP_ENABLED=/' "$config" > "$pending" ||
     ! printf '\nNEWPLATFORM_SEED_PROFILE=identities\nCLUSTERFORGE_BACKUP_ENABLED=false\n' >> "$pending" ||
     ! mv "$pending" "$config"; then
    rm -f "$pending"
    return 1
  fi
}

clusterforge_read_schema_contract() {
  "${CLUSTERFORGE_DEPLOY_DB_TOOL:?deployment database tool is required}" database contract --db "$1"
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
  "${CLUSTERFORGE_DEPLOY_DB_TOOL:?deployment database tool is required}" database active-runs --db "$1"
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
  "${CLUSTERFORGE_DEPLOY_DB_TOOL:?deployment database tool is required}" database snapshot --db "$1" --target "$2"
}
