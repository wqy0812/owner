#!/bin/sh
set -eu

if ! command -v ansible-playbook >/dev/null 2>&1; then
  echo "ansible-playbook is not installed; skipping Flannel ownership checks"
  exit 0
fi

python3 - <<'PY'
import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

playbooks = pathlib.Path('examples/ansible/k8s-1.17.5-kubeadm/components')
ansible = shutil.which('ansible-playbook')


class OwnershipChecks(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='flannel-ownership-')
        self.addCleanup(self.temporary.cleanup)
        self.root = pathlib.Path(self.temporary.name)
        self.state = self.root / 'state'
        self.state.mkdir()
        self.manifest = self.root / 'manifest.yml'
        self.manifest.write_text('owned manifest\n')
        (self.root / 'kubeconfig').write_text('local command stub\n')
        self.uids = ['namespace-original', 'role-original', 'binding-original']
        self.receipt = {
            'format': 1, 'resource_uids': self.uids,
            'manifest_sha256': hashlib.sha256(self.manifest.read_bytes()).hexdigest(),
        }
        self.owner = self.state / 'flannel.owned'
        self.owner.write_text(json.dumps(self.receipt))
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        for command in ['kubectl', 'ip', 'docker']:
            executable = self.bin / command
            executable.write_text('#!' + sys.executable + '\n' + '''
import json, os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
with open(os.environ['OWNERSHIP_COMMAND_LOG'], 'a') as log:
    log.write(name + ' ' + ' '.join(sys.argv[1:]) + '\\n')
if name == 'kubectl':
    state = pathlib.Path(os.environ['OWNERSHIP_UID_FILE'])
    if 'delete' in sys.argv:
        state.write_text('[]')
    print(json.dumps({'items': [{'metadata': {'uid': uid}} for uid in json.loads(state.read_text())]}))
elif name == 'ip':
    sys.exit(int(os.environ.get('OWNERSHIP_IP_STATUS', '1')))
''')
            executable.chmod(0o700)
        (self.root / 'inventory').write_text('[primary_control_plane]\nnode-a ansible_connection=local\n')

    def check_guard(self, mode, success, message='', ip_status='1'):
        source_mode = 'install' if mode == 'record' else 'rollback' if mode == 'complete' else mode
        source = (playbooks / ('flannel-' + source_mode + '.yml')).read_text()
        tasks = source.split('  tasks:\n', 1)[1]
        # Reuse the production ownership tasks. The complete-cleanup case uses
        # stub commands and redirects every affected path into this sandbox.
        next_task = re.search(r'^    - name:', tasks[1:], re.MULTILINE)
        guard = tasks[:next_task.start() + 1] if next_task else tasks
        if mode == 'record':
            guard = tasks[tasks.index('    - name: Read the installed resource identities'):].split('- import_playbook:', 1)[0]
        if mode == 'complete':
            guard = tasks
        guard = guard.replace('/etc/kubernetes/kube-flannel.yml', str(self.manifest))
        guard = guard.replace('/run/flannel', str(self.root / 'runtime-state'))
        test_playbook = self.root / 'guard.yml'
        test_playbook.write_text('---\n- hosts: all\n  gather_facts: false\n  become: false\n  any_errors_fatal: true\n  tasks:\n' + guard)
        variables = self.root / 'variables.json'
        variables.write_text(json.dumps({
            'component_state_dir': str(self.state), 'admin_kubeconfig_path': str(self.root / 'kubeconfig'),
            'ansible_python_interpreter': sys.executable, 'flannel_namespace': 'kube-flannel',
        }))
        uid_file = self.root / 'uids.json'
        uid_file.write_text(json.dumps(self.uids))
        env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ['PATH'],
                   OWNERSHIP_COMMAND_LOG=str(self.root / 'commands.log'), OWNERSHIP_UID_FILE=str(uid_file),
                   OWNERSHIP_IP_STATUS=ip_status, ANSIBLE_NOCOLOR='1', ANSIBLE_LOCAL_TEMP=str(self.root / 'ansible'))
        result = subprocess.run([ansible, '-i', str(self.root / 'inventory'), str(test_playbook), '-e', '@' + str(variables)],
                                env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=45)
        self.assertEqual(result.returncode == 0, success, result.stdout)
        if message:
            self.assertIn(message, result.stdout)

    def test_missing_receipt_stops_before_cluster_commands(self):
        self.owner.unlink()
        self.check_guard('rollback', False, 'No Flannel ownership receipt')
        self.assertFalse((self.root / 'commands.log').exists())

    def test_replaced_resource_is_rejected(self):
        self.uids = ['namespace-replaced', 'role-original', 'binding-original']
        self.check_guard('rollback', False, 'Refuse to delete replaced or unowned resources')

    def test_changed_manifest_is_rejected(self):
        self.manifest.write_text('unowned replacement\n')
        self.check_guard('rollback', False, 'Require the original manifest')

    def test_matching_receipt_is_accepted(self):
        self.check_guard('rollback', True)

    def test_install_records_identities_accepted_by_rollback(self):
        self.owner.unlink()
        self.check_guard('record', True)
        self.assertEqual(json.loads(self.owner.read_text()), self.receipt)
        self.check_guard('rollback', True)

    def test_install_cannot_claim_existing_cluster_resources(self):
        self.owner.unlink()
        self.check_guard('install', False, 'Refuse adoption of existing cluster resources')
        self.assertFalse(self.owner.exists())

    def test_cleanup_can_resume_after_resources_are_gone(self):
        self.uids = []
        self.manifest.unlink()
        self.check_guard('rollback', True)

    def test_complete_cleanup_removes_receipt_after_owned_resources(self):
        self.check_guard('complete', True)
        self.assertFalse(self.owner.exists())
        self.assertFalse(self.manifest.exists())
        self.assertEqual(json.loads((self.root / 'uids.json').read_text()), [])

    def test_install_cannot_claim_existing_node_networking(self):
        self.uids = []
        self.owner.unlink()
        self.manifest.unlink()
        self.check_guard('install', False, 'Refuse adoption of existing node networking', ip_status='0')
        self.assertFalse(self.owner.exists())


unittest.main(verbosity=2)
PY
