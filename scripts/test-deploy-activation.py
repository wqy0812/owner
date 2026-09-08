#!/usr/bin/env python3
"""Execute the actual remote activation body in a temporary, offline filesystem."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent

# Only external host commands are replaced; backup/activation/rollback control
# flow is read directly from the deploy script and runs unchanged.
COMMANDS = r'''
import hashlib, json, os, pathlib, shutil, signal, sys
root = pathlib.Path(os.environ['ACTIVATION_TEST_ROOT'])
state_path = root/'state.json'
state = json.loads(state_path.read_text())
args = sys.argv[1:]
command = pathlib.Path(sys.argv[0]).name
live = root/'opt/clusterforge/platform/clusterforge-platform'
new = live.read_text() == 'candidate platform\n'
failure = state['failure']
def finish(code=0):
    state_path.write_text(json.dumps(state))
    sys.exit(code)
state['calls'].append([command, *args])
if args and args[0] == 'database':
    operation = args[1]
    database = pathlib.Path(args[args.index('--db')+1])
    if operation == 'contract': print('clusterforge-v1-20260908-typed-run-snapshot')
    elif operation == 'snapshot':
        if failure == 'backup': finish(19)
        target = pathlib.Path(args[args.index('--target')+1]); shutil.copy2(database, target)
    elif operation == 'verify-run-migration':
        state['migrationChecks'] = state.get('migrationChecks', 0) + 1
        if failure == 'migration-report' or (failure == 'source-drift' and state['migrationChecks'] > 1): finish(25)
    elif operation == 'verify':
        if new and failure == 'database' and database.name == 'platform.db': finish(20)
    elif operation not in ('active-runs', 'active-work'): raise RuntimeError(operation)
elif command == 'systemctl':
    op, unit = args[0], args[-1]
    if op == 'show':
        if '--value' in args: print(state['service'])
        else: print('ActiveState='+state['service']+'\nSubState='+('running' if state['service']=='active' else 'dead'))
    elif op == 'is-enabled': finish(0 if state['timer'] else 1)
    elif op == 'is-active': finish(0 if state['service']=='active' else 1)
    elif op == 'stop' and unit == 'clusterforge-platform': state['service']='inactive'
    elif op == 'start' and unit == 'clusterforge-platform':
        state['service']='active'
        if new:
            (root/'var/lib/clusterforge/platform.db').write_text('candidate database\n')
            if failure == 'start': state['service']='failed'; finish(21)
            if failure == 'interrupt':
                state_path.write_text(json.dumps(state)); os.kill(os.getppid(), signal.SIGTERM)
    elif op in ('enable', 'disable'): state['timer'] = op=='enable'
elif command == 'curl':
    if '-w' in args: print('http=200')
    elif args[-1].endswith('version.json'): print('wrong' if failure=='version' else 'candidate version')
    else: print('wrong' if failure=='ui' else 'candidate index')
elif command == 'sha256sum':
    for name in args:
        if name.startswith('-'): continue
        print(hashlib.sha256(pathlib.Path(name).read_bytes()).hexdigest()+'  '+name)
    if not args: print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest()+'  -')
elif command == 'install':
    source, target = pathlib.Path(args[-2]), pathlib.Path(args[-1])
    if failure=='install' and new and target.name=='clusterforge-job': finish(22)
    shutil.copy2(source, target)
    target.chmod(int(args[args.index('-m')+1],8))
elif command == 'sed':
    # GNU sed -i used to remove one environment entry. No host files involved.
    target=pathlib.Path(args[-1])
    target.write_text(''.join(line for line in target.read_text().splitlines(True) if not line.startswith('CLUSTERFORGE_BACKUP_ENABLED=')))
elif command not in ('sleep', 'flock', 'journalctl'): raise RuntimeError(command)
finish()
'''


class ActivationTests(unittest.TestCase):
    def run_activation(self, failure='', initialize=False, inactive=False, migration=False):
        temporary = tempfile.TemporaryDirectory(prefix='clusterforge-activation-test-')
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        for name in ('opt/clusterforge/platform', 'var/lib/clusterforge', 'var/lock', 'etc/clusterforge', 'etc/systemd/system', 'commands'):
            (root/name).mkdir(parents=True)
        commands = root/'commands'
        for name in ('systemctl', 'curl', 'sha256sum', 'install', 'sed', 'sleep', 'flock', 'journalctl'):
            (commands/name).write_text('#!'+sys.executable+'\n'+COMMANDS)
            (commands/name).chmod(0o700)
        live = root/'opt/clusterforge/platform'
        for name in ('clusterforge-platform', 'clusterforge-backup', 'clusterforge-job'):
            (live/name).write_text('previous '+name+'\n'); (live/name).chmod(0o700)
        config = root/'etc/clusterforge/platform.env'
        config.write_text('CLUSTERFORGE_BACKUP_ENABLED=true\nPRESERVED_SETTING=value\n')
        for name in ('clusterforge-backup.timer', 'clusterforge-backup.service'):
            (root/'etc/systemd/system'/name).write_text('previous '+name+'\n')
        database = root/'var/lib/clusterforge/platform.db'
        if not initialize: database.write_text('previous database\n')
        initial = {str(p.relative_to(root)): p.read_bytes() for p in [*live.iterdir(), config, * (root/'etc/systemd/system').iterdir(), *([database] if database.exists() else [])]}
        staged = []
        for name, content in [('platform','candidate platform\n'), ('backup','#!'+sys.executable+'\n'+COMMANDS), ('helper',(ROOT/'scripts/deploy-test-88-55-remote-lib.sh').read_text()), ('job','candidate job\n')]:
            path = root/('staged-'+name); path.write_text(content); path.chmod(0o700); staged.append(path)
        digest = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
        text_digest = lambda text: hashlib.sha256(text.encode()).hexdigest()
        args = [str(staged[0]), digest(staged[0]), str(staged[1]), digest(staged[1]), str(staged[2]), digest(staged[2]), '0', '0', text_digest('candidate index\n'), text_digest('candidate version\n'), '1', '', str(staged[3]), digest(staged[3]), '1' if initialize else '0']
        if migration:
            bundle = root/'var/lib/clusterforge/run-migrations/test'
            bundle.mkdir(parents=True)
            (bundle/'target.db').write_text('migrated database\n')
            (bundle/'ready.json').write_text('{"status":"ready"}\n')
            args.append(str(bundle))
        if failure == 'checksum': args[1] = '0'*64
        source = (ROOT/'scripts/deploy-test-88-55.sh').read_text().split("<<'REMOTE_SCRIPT'\n", 1)[1].split('\nREMOTE_SCRIPT', 1)[0]
        for prefix in ('/opt/clusterforge', '/var/lib/clusterforge', '/var/lock', '/etc/clusterforge', '/etc/systemd/system'):
            source = source.replace(prefix, str(root/prefix.lstrip('/')))
        initial_state = 'inactive' if initialize or inactive or migration else 'active'
        (root/'state.json').write_text(json.dumps({'failure': failure, 'service': initial_state, 'timer': not migration, 'calls': []}))
        env = {**os.environ, 'PATH': str(commands)+os.pathsep+os.environ['PATH'], 'ACTIVATION_TEST_ROOT': str(root)}
        result = subprocess.run(['bash', '-s', '--', *args], input=source, text=True, capture_output=True, env=env, timeout=20)
        state = json.loads((root/'state.json').read_text())
        after_open = migration and failure in ('start', 'ui', 'version', 'database', 'interrupt')
        if after_open:
            self.assertNotEqual(result.returncode, 0, result.stdout+result.stderr)
            self.assertEqual((live/'clusterforge-platform').read_text(), 'candidate platform\n')
            self.assertEqual(database.read_text(), 'candidate database\n')
            self.assertIn('automatic database rollback is disabled', result.stderr)
            self.assertEqual(sum(call == ['systemctl', 'start', 'clusterforge-platform'] for call in state['calls']), 1)
        elif failure:
            self.assertNotEqual(result.returncode, 0, result.stdout+result.stderr)
            for name, content in initial.items(): self.assertEqual((root/name).read_bytes(), content, name+'\n'+result.stderr)
            self.assertEqual(state['service'], initial_state, result.stderr)
            self.assertEqual(state['timer'], not migration, result.stderr)
            if initialize: self.assertFalse(database.exists(), result.stderr)
        else:
            self.assertEqual(result.returncode, 0, result.stdout+result.stderr)
            self.assertEqual((live/'clusterforge-platform').read_text(), 'candidate platform\n')
            self.assertEqual((live/'clusterforge-job').read_text(), 'candidate job\n')
            self.assertEqual(state['service'], 'active')
            self.assertFalse(state['timer'])
            self.assertIn('deployment succeeded', result.stdout)
        self.assertFalse(any(path.exists() for path in staged), 'staged binaries were not cleaned')

    def test_migration_switch_success(self): self.run_activation(migration=True)
    def test_migration_rejects_report_and_late_source_drift(self):
        for failure in ('migration-report', 'source-drift', 'install'):
            with self.subTest(failure=failure): self.run_activation(failure, migration=True)
    def test_migration_never_restores_old_database_after_opening_writes(self):
        for failure in ('start', 'ui', 'version', 'database', 'interrupt'):
            with self.subTest(failure=failure): self.run_activation(failure, migration=True)

    def test_success(self): self.run_activation()
    def test_empty_database_initialization_success(self): self.run_activation(initialize=True)
    def test_failures_restore_binaries_database_configuration_and_timer(self):
        for failure in ('checksum', 'backup', 'install', 'start', 'ui', 'version', 'database', 'interrupt'):
            with self.subTest(failure=failure): self.run_activation(failure)
    def test_initialization_failure_restores_absent_database_and_inactive_service(self): self.run_activation('database', initialize=True)
    def test_failure_preserves_an_initially_inactive_service(self): self.run_activation('ui', inactive=True)


if __name__ == '__main__': unittest.main()
