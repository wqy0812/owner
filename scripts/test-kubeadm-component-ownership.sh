#!/bin/sh
set -eu

if ! command -v ansible-playbook >/dev/null 2>&1; then
  echo "ansible-playbook is not installed; skipping kubeadm component ownership checks"
  exit 0
fi

python3 - <<'PY'
import hashlib
import json
import os
import pathlib
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import unittest

playbooks = pathlib.Path('examples/ansible/k8s-1.17.5-kubeadm/components')
ansible = shutil.which('ansible-playbook')


class OwnershipChecks(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='kubeadm-ownership-')
        self.addCleanup(temporary.cleanup)
        self.root = pathlib.Path(temporary.name)
        self.state = self.root / 'node-a/state'
        self.state.mkdir(parents=True)
        self.paths = [
            self.root / 'kubernetes/manifests/kube-apiserver.yaml',
            self.root / 'kubernetes/clusterforge-kubeadm.yaml',
            self.root / 'kubernetes/admin.conf',
            self.root / 'kube/config',
        ]
        for file in self.paths:
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_text('owned ' + file.name + '\n')
        self.resources = [
            'deployment/coredns', 'service/kube-dns', 'configmap/coredns',
            'serviceaccount/coredns', 'clusterrole/system:coredns', 'clusterrolebinding/system:coredns',
        ]
        self.runtime = {
            'resources': {name: 'uid-' + str(index) for index, name in enumerate(self.resources)},
            'containers': [self.container('original-container', 'original-static-pod')],
        }
        self.runtime_file = self.root / 'runtime.json'
        self.save_runtime()
        self.core_receipt = {'format': 1, 'resource_uids': list(self.runtime['resources'].values())}
        self.api_receipt = {
            'format': 1,
            'files': {str(file): hashlib.sha256(file.read_bytes()).hexdigest() for file in self.paths},
            'pod_uids': ['original-static-pod'],
        }
        self.write_receipt('coredns', self.core_receipt)
        self.write_receipt('kube-apiserver', self.api_receipt)
        self.commands = self.root / 'commands.log'
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        for name in ['kubectl', 'docker', 'kubeadm']:
            command = self.bin / name
            command.write_text('#!' + sys.executable + '\n' + '''
import json, os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ['OWNERSHIP_COMMAND_LOG'], 'a') as log:
    log.write(name + ' ' + ' '.join(args) + '\\n')
path = pathlib.Path(os.environ['OWNERSHIP_RUNTIME'])
state = json.loads(path.read_text())
if name == 'kubectl':
    if 'delete' in args:
        if os.environ.get('OWNERSHIP_FAIL_DELETE') == '1':
            sys.exit(23)
        for resource in args:
            state['resources'].pop(resource, None)
        path.write_text(json.dumps(state))
    print(json.dumps({'items': [{'metadata': {'uid': uid}} for key, uid in state['resources'].items() if key in args]}))
elif name == 'docker':
    if args[0] == 'ps':
        filters = [args[i + 1].removeprefix('label=') for i, arg in enumerate(args) if arg == '--filter']
        containers = [c for c in state['containers'] if all(c['Config']['Labels'].get(f.split('=', 1)[0]) == f.split('=', 1)[1] for f in filters)]
        print('\\n'.join(c['Id'] for c in containers))
    elif args[0] == 'inspect':
        print(json.dumps([c for c in state['containers'] if c['Id'] in args[1:]]))
    elif args[0] == 'rm':
        if os.environ.get('OWNERSHIP_FAIL_DELETE') == '1':
            sys.exit(23)
        state['containers'] = [c for c in state['containers'] if c['Id'] not in args[1:]]
        path.write_text(json.dumps(state))
else:
    raise SystemExit('installation is not permitted in the ownership sandbox')
''')
            command.chmod(0o700)
        self.inventory = self.root / 'inventory'
        self.inventory.write_text('[primary_control_plane]\nnode-a ansible_connection=local\n')
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            self.closed_port = listener.getsockname()[1]

    @staticmethod
    def container(identifier, uid, namespace='kube-system'):
        return {'Id': identifier, 'Config': {'Labels': {
            'io.kubernetes.container.name': 'kube-apiserver',
            'io.kubernetes.pod.namespace': namespace, 'io.kubernetes.pod.uid': uid,
        }}}

    def save_runtime(self):
        self.runtime_file.write_text(json.dumps(self.runtime))

    def receipt(self, component):
        return self.state / (component + '.owned')

    def write_receipt(self, component, value):
        self.receipt(component).write_text(json.dumps(value))

    def run_tasks(self, component, mode, success, message='', fail_delete=False):
        source_mode = 'install' if mode in ['record', 'install_full'] else 'rollback' if mode == 'complete' else mode
        source = (playbooks / (component + '-' + source_mode + '.yml')).read_text()
        if mode == 'install_full':
            tasks = source.replace('become: true', 'become: false')
        elif mode == 'complete':
            tasks = source.split('  tasks:\n', 1)[1]
        else:
            if mode == 'record':
                label = ('Record CoreDNS ownership before readiness verification' if component == 'coredns'
                         else 'Record API server ownership before declaring installation complete')
                start = source.index('    - name: ' + label)
            else:
                start = source.index('  tasks:\n') + len('  tasks:\n')
            tasks = source[start:]
            end = re.search(r'^(?:    - |\S)', tasks[1:], re.MULTILINE)
            if end:
                tasks = tasks[:end.start() + 1]
        # Execute the actual production tasks with commands stubbed and every
        # affected filesystem path redirected into this temporary directory.
        tasks = tasks.replace('/etc/kubernetes', str(self.root / 'kubernetes'))
        tasks = tasks.replace('/root/.kube', str(self.root / 'kube'))
        tasks = tasks.replace('port: 6443', 'port: ' + str(self.closed_port))
        test_playbook = self.root / 'tasks.yml'
        test_playbook.write_text(tasks if mode == 'install_full' else '---\n- hosts: all\n  gather_facts: false\n  become: false\n  any_errors_fatal: true\n  tasks:\n' + tasks)
        variables = self.root / 'variables.json'
        variables.write_text(json.dumps({
            'sandbox_root': str(self.root),
            'component_state_dir': '{{ sandbox_root }}/{{ inventory_hostname }}/state',
            'admin_kubeconfig_path': str(self.paths[2]), 'coredns_namespace': 'kube-system',
            'ansible_python_interpreter': sys.executable,
        }))
        env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ['PATH'],
                   OWNERSHIP_COMMAND_LOG=str(self.commands), OWNERSHIP_RUNTIME=str(self.runtime_file),
                   OWNERSHIP_FAIL_DELETE='1' if fail_delete else '0',
                   ANSIBLE_NOCOLOR='1', ANSIBLE_LOCAL_TEMP=str(self.root / 'ansible'))
        result = subprocess.run([ansible, '-i', str(self.inventory), str(test_playbook), '-e', '@' + str(variables)],
                                env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=60)
        self.assertEqual(result.returncode == 0, success, result.stdout)
        if message:
            self.assertIn(message, result.stdout)
        return result

    def assert_no_delete(self):
        log = self.commands.read_text() if self.commands.exists() else ''
        self.assertNotIn(' delete ', log)
        self.assertNotIn('docker rm ', log)
        self.assertTrue(self.paths[0].exists())

    def test_component_playbooks_parse_with_their_imports(self):
        result = subprocess.run([ansible, '-i', str(self.inventory), '--syntax-check',
                                 *[str(file) for file in sorted(playbooks.glob('*.yml'))]],
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=45)
        self.assertEqual(result.returncode, 0, result.stdout)

    def test_coredns_missing_receipt_stops_before_cluster_commands(self):
        self.receipt('coredns').unlink()
        self.run_tasks('coredns', 'rollback', False, 'No CoreDNS ownership receipt')
        self.assertFalse(self.commands.exists())

    def test_coredns_replaced_role_is_preserved(self):
        self.runtime['resources']['clusterrole/system:coredns'] = 'replacement-role'
        self.save_runtime()
        self.run_tasks('coredns', 'complete', False, 'Refuse to delete replaced or unowned CoreDNS')
        self.assert_no_delete()

    def test_coredns_installer_refuses_existing_resources(self):
        self.receipt('coredns').unlink()
        self.run_tasks('coredns', 'install', False, 'Refuse adoption of existing CoreDNS')
        self.assert_no_delete()

    def test_coredns_installer_accepts_an_empty_cluster(self):
        self.receipt('coredns').unlink()
        self.runtime['resources'] = {}
        self.save_runtime()
        self.run_tasks('coredns', 'install', True)

    def test_coredns_install_receipt_can_authorize_rollback(self):
        self.receipt('coredns').unlink()
        self.run_tasks('coredns', 'record', True)
        self.assertEqual(json.loads(self.receipt('coredns').read_text()), self.core_receipt)
        self.run_tasks('coredns', 'complete', True)
        self.assertEqual(json.loads(self.runtime_file.read_text())['resources'], {})
        self.assertFalse(self.receipt('coredns').exists())

    def test_coredns_failure_keeps_receipt_and_allows_retry(self):
        self.run_tasks('coredns', 'complete', False, fail_delete=True)
        self.assertTrue(self.receipt('coredns').exists())
        self.run_tasks('coredns', 'complete', True)
        self.assertFalse(self.receipt('coredns').exists())

    def test_coredns_cleanup_resumes_with_only_roles_left(self):
        self.runtime['resources'] = {k: v for k, v in self.runtime['resources'].items() if k.startswith('clusterrole')}
        self.save_runtime()
        self.run_tasks('coredns', 'complete', True)
        self.assertFalse(self.receipt('coredns').exists())

    def test_coredns_missing_receipt_on_one_node_blocks_all_deletions(self):
        self.inventory.write_text('[primary_control_plane]\nnode-a ansible_connection=local\n[secondary_control_plane]\nnode-b ansible_connection=local\n')
        self.run_tasks('coredns', 'complete', False, 'No CoreDNS ownership receipt')
        self.assert_no_delete()

    def test_api_missing_receipt_preserves_manifest_and_containers(self):
        self.receipt('kube-apiserver').unlink()
        self.run_tasks('kube-apiserver', 'complete', False, 'No API server ownership receipt')
        self.assert_no_delete()

    def test_api_replaced_manifest_is_preserved(self):
        self.paths[0].write_text('replacement manifest\n')
        self.run_tasks('kube-apiserver', 'complete', False, 'Refuse replaced control-plane files')
        self.assert_no_delete()

    def test_api_replaced_kubeconfig_is_preserved(self):
        self.paths[3].write_text('replacement kubeconfig\n')
        self.run_tasks('kube-apiserver', 'complete', False, 'Refuse replaced control-plane files')
        self.assert_no_delete()

    def test_api_replaced_workload_is_preserved(self):
        self.runtime['containers'] = [self.container('other-container', 'other-static-pod')]
        self.save_runtime()
        self.run_tasks('kube-apiserver', 'complete', False, 'Refuse replaced or unowned API server')
        self.assert_no_delete()

    def test_api_installer_refuses_existing_control_plane(self):
        self.receipt('kube-apiserver').unlink()
        self.run_tasks('kube-apiserver', 'install', False, 'Refuse adoption of existing control-plane')
        self.assert_no_delete()

    def test_api_installer_accepts_pristine_targets(self):
        self.receipt('kube-apiserver').unlink()
        for file in self.paths:
            file.unlink()
        self.runtime['containers'] = []
        self.save_runtime()
        self.run_tasks('kube-apiserver', 'install', True)

    def test_api_install_receipt_accepts_restarted_containers(self):
        self.receipt('kube-apiserver').unlink()
        self.run_tasks('kube-apiserver', 'record', True)
        self.assertEqual(json.loads(self.receipt('kube-apiserver').read_text()), self.api_receipt)
        self.runtime['containers'] = [self.container('restarted-container', 'original-static-pod'),
                                      self.container('unrelated-container', 'other-pod', 'other-namespace')]
        self.save_runtime()
        self.run_tasks('kube-apiserver', 'complete', True)
        self.assertEqual([c['Id'] for c in json.loads(self.runtime_file.read_text())['containers']], ['unrelated-container'])
        self.assertFalse(self.receipt('kube-apiserver').exists())
        self.assertTrue(all(not file.exists() for file in self.paths))

    def test_api_failure_keeps_receipt_and_allows_retry_after_manifest_removal(self):
        self.run_tasks('kube-apiserver', 'complete', False, fail_delete=True)
        self.assertTrue(self.receipt('kube-apiserver').exists())
        self.assertFalse(self.paths[0].exists())
        self.run_tasks('kube-apiserver', 'complete', True)
        self.assertFalse(self.receipt('kube-apiserver').exists())

    def test_api_cleanup_resumes_when_files_and_containers_are_gone(self):
        for file in self.paths:
            file.unlink()
        self.runtime['containers'] = []
        self.save_runtime()
        self.run_tasks('kube-apiserver', 'complete', True)
        self.assertFalse(self.receipt('kube-apiserver').exists())

    def test_api_missing_receipt_on_one_node_blocks_all_deletions(self):
        self.inventory.write_text('[primary_control_plane]\nnode-a ansible_connection=local\n[secondary_control_plane]\nnode-b ansible_connection=local\n')
        self.run_tasks('kube-apiserver', 'complete', False, 'No API server ownership receipt')
        self.assert_no_delete()

    def test_api_full_install_checks_secondary_ownership_before_primary_mutation(self):
        self.inventory.write_text('[primary_control_plane]\nnode-a ansible_connection=local\n[secondary_control_plane]\nnode-b ansible_connection=local\n')
        before = [file.read_bytes() for file in self.paths]
        self.run_tasks('kube-apiserver', 'install_full', False, 'Refuse adoption of existing control-plane')
        self.assertEqual([file.read_bytes() for file in self.paths], before)
        self.assertNotIn('kubeadm ', self.commands.read_text())


unittest.main(verbosity=2)
PY
