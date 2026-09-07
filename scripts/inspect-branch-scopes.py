#!/usr/bin/env python3
"""Read-only inventory of existing branch scopes and category relationships.

This inspects declarations only. It never resolves credentials, changes schema,
rewrites historical versions, or assigns category parents by their names.
"""
import argparse
import datetime
import json
import re
import sqlite3
from pathlib import Path


def inspect(database, offline_snapshot=False):
    path = Path(database).resolve(strict=True)
    if offline_snapshot and Path(str(path) + '-wal').exists():
        raise ValueError('Offline snapshot must not have a WAL; use an SQLite-consistent backup.')
    connection = sqlite3.connect(path.as_uri() + '?mode=ro' + ('&immutable=1' if offline_snapshot else ''), uri=True)
    connection.row_factory = sqlite3.Row
    connection.execute('BEGIN')
    schema = Path(__file__).resolve().parents[1] / 'internal/store/schema.go'
    expected = re.search(r'schemaContract = "([^"]+)"', schema.read_text()).group(1)
    actual = connection.execute('SELECT version FROM schema_contract WHERE id=1').fetchone()
    if actual is None or actual[0] != expected:
        raise ValueError('Only the current schema contract is supported: ' + expected)

    def rows(table):
        return [dict(row) for row in connection.execute('SELECT * FROM ' + table)]

    def scope(raw):
        value = json.loads(raw or '{}') or {}
        return {key: sorted(set(values if isinstance(values, list) else [values])) for key, values in sorted(value.items()) if values}

    report = {'database': str(path), 'inspectedAt': datetime.datetime.now(datetime.timezone.utc).isoformat(),
              'readOnly': True, 'offlineSnapshot': offline_snapshot, 'schema': rows('schema_contract'), 'branchConflicts': [],
              'unassignedBranches': [], 'categories': [], 'options': [], 'unlinkedOptions': [], 'directoryParameters': []}
    for parent, child, foreign_key in [('component_release_lines', 'component_releases', 'line_id'), ('scenarios', 'scenario_revisions', 'scenario_id')]:
        versions = rows(child)
        grouped = {}
        for row in versions:
            branch_id = row[foreign_key]
            grouped.setdefault(branch_id, []).append({'id': row['id'], 'version': row.get('version', row.get('revision')),
                                                     'scope': scope(row['environment_constraints_json'])})
        branches = {row['id']: row for row in rows(parent)}
        for branch_id, members in grouped.items():
            stored = branches.get(branch_id)
            if stored is None:
                raise ValueError('Unknown branch identity: ' + str(branch_id))
            definition = scope(stored['environment_constraints_json'])
            entry = {'kind': parent, 'branchId': branch_id, 'branchScope': definition, 'versions': members}
            if any(member['scope'] != definition for member in members):
                report['branchConflicts'].append(entry)
            if not definition:
                report['unassignedBranches'].append(entry)
    categories = {row['id']: row for row in rows('platform_option_categories')}
    options = {row['id']: row for row in rows('platform_options')}
    for row in categories.values():
        report['categories'].append({key: row.get(key) for key in ['id', 'technical_key', 'label', 'parent_category_id', 'retired_at']})
    for row in options.values():
        report['options'].append({key: row.get(key) for key in ['id', 'category_id', 'technical_value', 'label', 'parent_option_id', 'retired_at']})
        category = categories.get(row['category_id'], {})
        parent = options.get(row.get('parent_option_id'), {})
        if category.get('parent_category_id') and parent.get('category_id') != category['parent_category_id']:
            report['unlinkedOptions'].append({key: row.get(key) for key in ['id', 'category_id', 'technical_value', 'label', 'parent_option_id']})
    for release in rows('component_releases'):
        for parameter in json.loads(release.get('parameters_json') or '[]') or []:
            if re.search(r'path|root|dir', parameter.get('name', ''), re.I):
                report['directoryParameters'].append({'releaseId': release['id'], 'componentId': release['component_id'],
                    'version': release.get('version'), 'parameter': parameter.get('name'), 'provider': parameter.get('valueProvider'),
                    'visibility': parameter.get('visibility'), 'declarationOnly': True})
    connection.rollback()
    connection.close()
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('database', help='Existing local SQLite database; opened read-only')
    parser.add_argument('--offline-snapshot', action='store_true', help='Only for a closed, complete snapshot with no WAL')
    args = parser.parse_args()
    print(json.dumps(inspect(args.database, args.offline_snapshot), ensure_ascii=False, indent=2))
