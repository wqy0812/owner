"""Offline reference gate. Only temporary files and local command doubles mutate."""
import ast
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

import yaml

ROOT = Path(__file__).resolve().parents[1] / 'examples/ansible/kubernetes-1.17.5'
CONTRACTS = {p.parent.name: json.loads(p.read_text()) for p in sorted(ROOT.glob('roles/*/contract.json'))}
SCENARIO = json.loads((ROOT / 'scenario.json').read_text())
LOCK = json.loads((ROOT / 'source-lock.json').read_text())
ENTRIES = [ROOT / 'roles' / slug / a['entry'] for slug, c in CONTRACTS.items() for a in c['actions']]
ENTRIES.append(ROOT / SCENARIO['acceptance']['job']['entry'])


def yaml_read(path):
    return yaml.safe_load(path.read_text())


def inventory():
    return {'all': {'children': {
        'control_plane': {'hosts': {'control'+str(i): {'ansible_host': '192.0.2.'+str(i)} for i in range(1, 4)}},
        'worker_nodes': {'hosts': {'worker'+str(i): {'ansible_host': '192.0.2.'+str(i+3)} for i in range(1, 4)}}
    }, 'vars': {'ansible_connection': 'local', 'ansible_python_interpreter': sys.executable}}}


class ReferenceContracts(unittest.TestCase):
    def test_source_lock_and_workspace_completeness(self):
        self.assertEqual(len(CONTRACTS), 15)
        self.assertEqual(len(LOCK['files']), 93)
        self.assertEqual(len(ENTRIES), 76)
        listed = set()
        for entry in LOCK['files']:
            path = ROOT / entry['path']
            self.assertNotIn(entry['path'], listed)
            listed.add(entry['path'])
            self.assertEqual(hashlib.sha256(path.read_bytes()).hexdigest(), entry['referenceSha256'], str(path))
            if path.suffix == '.py':
                # Runtime behavior is preserved byte-for-byte from each published workspace.
                self.assertEqual(entry['referenceSha256'], entry['sourceSha256'])
                ast.parse(path.read_text(), filename=str(path))
            else:
                self.assertNotRegex(path.read_text(), r'group_[a-f0-9]{20,}|192\.168\.88\.|ansible\.builtin\.')
        actual = {str(p.relative_to(ROOT)) for p in ROOT.rglob('*') if p.is_file() and ('tasks' in p.parts or 'files' in p.parts)}
        self.assertEqual(actual, listed)

    def test_exact_dependencies_order_and_parameter_ownership(self):
        nodes = {n['id']: n for n in SCENARIO['nodes']}
        order = SCENARIO['installOrder']
        self.assertEqual(set(order), set(nodes))
        self.assertEqual(len(order), len(nodes))
        self.assertEqual(SCENARIO['rollbackOrder'], list(reversed(order)))
        expected_edges = set()
        for slug, c in CONTRACTS.items():
            self.assertEqual(nodes[slug]['version'], c['referenceVersion'])
            params = {p['name']: p for p in c['parameters']}
            mapped = set()
            for d in c['dependencies']:
                upstream = CONTRACTS[d['component']]
                self.assertEqual(d['version'], upstream['referenceVersion'])
                if d['kind'] == 'execution':
                    expected_edges.add((d['component'], slug, 'dependency'))
                else:
                    self.assertEqual(d['kind'], 'configuration')
                public = {p['name']: p for p in upstream['parameters'] if p['visibility'] == 'public'}
                for m in d['parameterMappings']:
                    self.assertIn(m['upstreamParameter'], public)
                    self.assertEqual(params[m['targetParameter']]['valueProvider'], 'upstream_mapping')
                    self.assertNotIn(m['targetParameter'], mapped)
                    mapped.add(m['targetParameter'])
            self.assertEqual(mapped, {p['name'] for p in c['parameters'] if p['valueProvider'] == 'upstream_mapping'})
            actions = {a['id']: a for a in c['actions']}
            self.assertEqual(set(actions), {'precheck', 'install', 'postcheck', 'rollback', 'rollback-postcheck'})
            self.assertEqual(actions['install']['preCheckActionId'], 'precheck')
            self.assertEqual(actions['install']['postCheckActionId'], 'postcheck')
            self.assertEqual(actions['rollback']['postCheckActionId'], 'rollback-postcheck')
            for image in c['images']:
                self.assertRegex(image['digest'], r'^sha256:[a-f0-9]{64}$')
                self.assertTrue(image['sourceRef'].endswith('@'+image['digest']))
        actual = {(e['source'], e['target'], e['kind']) for e in SCENARIO['edges']}
        self.assertEqual({e for e in actual if e[2] == 'dependency'}, expected_edges)
        self.assertEqual(len(expected_edges), 30)
        self.assertEqual({e for e in actual if e[2] == 'sequence'}, {
            ('docker-runtime', 'kubernetes-distribution', 'sequence'),
            ('kubernetes-encryption-configuration', 'kubelet', 'sequence'),
            ('kube-controller-manager', 'kube-scheduler', 'sequence'),
            ('kube-scheduler', 'kubernetes-bootstrap-rbac', 'sequence')})
        for source, target, _ in actual:
            self.assertLess(order.index(source), order.index(target))
        self.assertEqual(SCENARIO['acceptance']['bindings'], [{'parameter': 'admin_kubeconfig_path', 'source': 'node', 'nodeId': 'cluster-pki', 'sourceParameter': 'admin_kubeconfig_path'}])

    def test_credentials_are_scoped_and_censored(self):
        for slug, contract in CONTRACTS.items():
            for action in contract['actions']:
                for task in yaml_read(ROOT / 'roles' / slug / action['entry']):
                    environment = task.get('environment', {})
                    for value in environment.values():
                        for credential in re.findall(r'cf\.credentials\.([A-Z_][A-Z0-9_]*)', value):
                            self.assertIn(credential, action['requiredCredentials'])
                            self.assertIs(task.get('no_log'), True)
                    if 'CF_PKI_BUNDLE' in environment or 'slurp' in task:
                        self.assertIs(task.get('no_log'), True)


class RecoveryBehavior(unittest.TestCase):
    def runtime(self, slug, directory):
        inputs = {'clusterforge_backup_ref': str(directory / 'backup'),
                  'clusterforge_backup_marker': str(directory / 'backup/marker.json'),
                  'clusterforge_backup_metadata': 'isolated-reference-test'}
        env = {'CF_INPUTS': json.dumps(inputs), 'CF_NODE': 'control1', 'CF_IP': '192.0.2.1',
               'CF_NAMES': '["control1"]', 'CF_IPS': '["192.0.2.1"]', 'CF_MASTERS': '["control1"]'}
        namespace = {'__name__': 'isolated_reference'}
        source = ROOT / 'roles' / slug / 'files/runtime.py'
        with mock.patch.dict(os.environ, env), mock.patch.object(sys, 'argv', ['runtime.py', 'pre']):
            exec(compile(source.read_text(), str(source), 'exec'), namespace)
        namespace['run'] = mock.Mock(return_value='')
        return namespace

    def test_every_component_preserves_baseline_and_rejects_wrong_identity(self):
        for slug in CONTRACTS:
            with self.subTest(component=slug), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                g = self.runtime(slug, root)
                original = root / 'original'
                original.mkdir(mode=0o750)
                (original / 'config').write_text('original content')
                os.chmod(str(original / 'config'), 0o640)
                os.chown(str(original / 'config'), 1234, 1235)
                (original / 'link').symlink_to('config')
                absent = root / 'previously-absent'
                g.update(PATHS=[str(original), str(absent)], SERVICES=[], capture_extra=lambda: {},
                         restore_extra=lambda _: None, verify_extra=lambda _: None)
                before = g['fingerprint'](original)
                g['capture']()
                (original / 'config').write_text('changed')
                absent.write_text('created by install')
                marker = g['MARKER'].read_bytes()
                g['capture']()  # Re-entry must not replace the original recovery point.
                self.assertEqual(g['MARKER'].read_bytes(), marker)
                g['METADATA'] = 'different-run'
                with self.assertRaisesRegex(AssertionError, 'identity'):
                    g['restore']()
                self.assertEqual((original / 'config').read_text(), 'changed')
                g['run'].assert_not_called()
                g['METADATA'] = 'isolated-reference-test'
                g['restore']()
                self.assertEqual(g['fingerprint'](original), before)
                self.assertFalse(absent.exists())
                g['restored']()

    def test_api_resources_reject_adoption_and_replacement_before_any_delete(self):
        for slug in CONTRACTS:
            with self.subTest(component=slug), tempfile.TemporaryDirectory() as temp:
                g = self.runtime(slug, Path(temp))
                g['kubectl'] = mock.Mock()
                g['get_object'] = lambda *args: {'metadata': {'uid': 'external'}}
                with self.assertRaisesRegex(AssertionError, 'adopt'):
                    g['apply_objects']([{'kind': 'ConfigMap', 'metadata': {'name': 'owned'}}])
                g['kubectl'].assert_not_called()
                receipt = g['BACKUP'] / 'api-resources.json'
                entries = [{'kind': 'ConfigMap', 'name': n, 'namespace': None, 'uid': 'expected'} for n in ['first', 'second']]
                g['write'](receipt, entries)
                g['get_object'] = lambda kind, name, ns: {'metadata': {'uid': 'expected' if name == 'first' else 'replaced'}}
                with self.assertRaisesRegex(AssertionError, 'replaced'):
                    g['delete_objects']()
                g['kubectl'].assert_not_called()
                g['get_object'] = lambda *args: None
                g['delete_objects']()
                self.assertEqual([c[0][0][2] for c in g['kubectl'].call_args_list], ['second', 'first'])
                self.assertTrue(receipt.exists())

    def test_unrelated_firewall_drift_prevents_restore(self):
        baseline = '*filter\n:INPUT ACCEPT [0:0]\n-A INPUT -s 192.0.2.9 -j ACCEPT\nCOMMIT\n'
        drifted = baseline.replace('192.0.2.9', '192.0.2.10')
        for slug in CONTRACTS:
            with self.subTest(component=slug), tempfile.TemporaryDirectory() as temp:
                g = self.runtime(slug, Path(temp))
                g['run'] = mock.Mock(return_value=drifted)
                with self.assertRaisesRegex(AssertionError, 'Unrelated firewall'):
                    g['network_restore']({'iptables': baseline}, ['KUBE-', 'FLANNEL-'])
                self.assertEqual(g['run'].call_args_list, [mock.call(['iptables-save'])])


class AnsibleRuntime(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.ansible = os.environ['ANSIBLE_PLAYBOOK']
        version = subprocess.check_output([cls.ansible, '--version'], universal_newlines=True)
        if version.splitlines()[0] != 'ansible-playbook 2.8.8' or sys.version_info[:3] != (3, 6, 9):
            raise RuntimeError('Use the fixed Ansible 2.8.8 / Python 3.6.9 container')
        print(version, flush=True)

    def run_play(self, temp, plays, inv=None, flags=()):
        root = Path(temp)
        (root / 'play.yml').write_text(yaml.safe_dump(plays, allow_unicode=True))
        (root / 'inventory.yml').write_text(yaml.safe_dump(inv or inventory()))
        # A private config prevents workstation settings and callbacks changing the test.
        (root / 'ansible.cfg').write_text('[defaults]\nretry_files_enabled=False\nstdout_callback=default\n')
        env = dict(os.environ, ANSIBLE_CONFIG=str(root / 'ansible.cfg'), ANSIBLE_NOCOLOR='1')
        return subprocess.run([self.ansible, '-i', str(root / 'inventory.yml'), str(root / 'play.yml')] + list(flags),
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT, universal_newlines=True,
                              env=env, timeout=120)

    def test_all_76_entries_parse_and_expand_with_ansible_288(self):
        plays = []
        for path in ENTRIES:
            plays.append({'name': str(path.relative_to(ROOT)), 'hosts': 'all', 'gather_facts': False,
                          'tasks': [{'import_role': {'name': str(path.parent.parent), 'tasks_from': path.name}}]})
        with tempfile.TemporaryDirectory() as temp:
            for flag in ['--syntax-check', '--list-tasks']:
                result = self.run_play(temp, plays, flags=[flag])
                self.assertEqual(result.returncode, 0, result.stdout)
                if flag == '--list-tasks':
                    self.assertEqual(result.stdout.count('Validate six-node inventory before running this entry'), 76)

    def test_topology_fails_before_the_first_mutation(self):
        canonical = yaml_read(ENTRIES[0])[0]
        for entry in ENTRIES:
            self.assertEqual(yaml_read(entry)[0], canonical)
        cases = {}
        valid = inventory()
        cases['valid'] = valid
        missing = copy.deepcopy(valid); del missing['all']['children']['worker_nodes']
        cases['missing'] = missing
        duplicate = copy.deepcopy(valid)
        duplicate['all']['children']['worker_nodes']['hosts']['worker1']['ansible_host'] = '192.0.2.1'
        cases['duplicate_address'] = duplicate
        extra = copy.deepcopy(valid); extra['all']['hosts'] = {'unexpected': {'ansible_host': '192.0.2.7'}}
        cases['extra_host'] = extra
        overlap = copy.deepcopy(valid)
        del overlap['all']['children']['worker_nodes']['hosts']['worker1']
        overlap['all']['children']['worker_nodes']['hosts']['control1'] = {'ansible_host': '192.0.2.1'}
        cases['overlap'] = overlap
        for name, inv in cases.items():
            with self.subTest(case=name), tempfile.TemporaryDirectory() as temp:
                marker = Path(temp) / 'executed'
                play = {'hosts': 'control1', 'gather_facts': False, 'tasks': [canonical,
                        {'command': {'argv': ['/usr/bin/touch', str(marker)]}}]}
                result = self.run_play(temp, [play], inv)
                self.assertEqual(result.returncode == 0, name == 'valid', result.stdout)
                self.assertEqual(marker.exists(), name == 'valid')

    def test_probe_uses_delivered_registry_and_rejects_changed_digest(self):
        tasks = yaml_read(ROOT / SCENARIO['acceptance']['job']['entry'])
        start = next(i for i, t in enumerate(tasks) if t.get('register') == 'reference_flannel')
        probe_tasks = tasks[start:start+3]
        digest = CONTRACTS['flannel']['images'][0]['digest']
        for case in ['valid', 'wrong_digest', 'lookup_failure']:
            with self.subTest(case=case), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                image = 'mirror.example.invalid/owner/flannel@' + (digest if case == 'valid' else 'sha256:'+'0'*64)
                ds = {'spec': {'template': {'spec': {'containers': [{'image': image}]}}}}
                (root / 'daemonset.json').write_text(json.dumps(ds))
                (root / 'kubectl').write_text('#!/bin/sh\n' + ('exit 1\n' if case == 'lookup_failure' else 'cat "'+str(root / 'daemonset.json')+'"\n'))
                (root / 'docker').write_text('#!/bin/sh\nprintf "%s\\n" "$@" > "'+str(root / 'docker.args')+'"\n')
                for name in ['kubectl', 'docker']:
                    os.chmod(str(root / name), 0o700)
                play = {'hosts': 'control1', 'gather_facts': False,
                        'vars': {'cf': {'inputs': {'admin_kubeconfig_path': '/isolated/admin.conf'}}},
                        'environment': {'PATH': str(root)+':'+os.environ['PATH']}, 'tasks': probe_tasks}
                result = self.run_play(temp, [play])
                self.assertEqual(result.returncode == 0, case == 'valid', result.stdout)
                args = root / 'docker.args'
                self.assertEqual(args.exists(), case == 'valid')
                if case == 'valid':
                    self.assertIn(image, args.read_text().splitlines())


if __name__ == '__main__':
    unittest.main(verbosity=2)
