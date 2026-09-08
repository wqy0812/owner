"""Failure boundaries for the shared gate; no Docker or deployment targets used."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch

sys.dont_write_bytecode = True
SCRIPTS = Path(__file__).resolve().parent


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, SCRIPTS / filename)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


gate = module('gate', 'deployment-gate.py')
roles = module('role_gate', 'test-role-job.py')
deploy = module('docker_deploy', 'deploy-local-docker.py')


class FakeGate(gate.Gate):
    def __init__(self, root, failure=None):
        super().__init__(root)
        self.failure = failure
        self.visited = []
        self.container = 'isolated-candidate'
        self.owned = False

    def runtime(self):
        if self.failure == 'missing-runtime':
            raise RuntimeError('Missing pinned runtime')

    def copy_source(self):
        self.checkout = '/isolated/source'

    def check_container_source(self):
        if self.failure == 'container-drift':
            raise RuntimeError('Container source differs')

    def stage(self, name, args):
        self.visited.append((name, args))
        body = 'raise SystemExit(13)' if self.failure == name else 'print("stage passed")'
        if self.failure == 'source-drift':
            body = 'from pathlib import Path; Path("input").write_text("changed")'
        # Exercise the real subprocess exit handling, evidence and source check.
        super().stage(name, [sys.executable, '-c', body])


class PipelineTests(unittest.TestCase):
    def test_gate_and_browser_evidence_survive_temporary_source_cleanup(self):
        with tempfile.TemporaryDirectory() as temp:
            retained = Path(temp) / 'retained'
            with tempfile.TemporaryDirectory() as source:
                root = Path(source)
                subprocess.run(['git', 'init', '-q'], cwd=root, check=True)
                (root / 'input').write_text('source')
                with patch.dict(os.environ, {'CLUSTERFORGE_GATE_EVIDENCE_ROOT': str(retained)}):
                    task = FakeGate(root)
                body = ('import os; from pathlib import Path; '
                        'p=Path(os.environ["CLUSTERFORGE_TEST_EVIDENCE_ROOT"]); '
                        'p.mkdir(parents=True,exist_ok=True); (p/"trace").write_text("retained")')
                task.stage = lambda name, args: gate.Gate.stage(task, name, [sys.executable, '-c', body])
                task.execute()
            self.assertEqual((task.record / 'browser/trace').read_text(), 'retained')
            self.assertEqual(json.loads((task.record / 'result.json').read_text())['status'], 'passed')

    def test_complete_gate_and_every_failure_prevent_success(self):
        for failure in [None, 'missing-runtime', 'local', 'runtime', 'source-drift', 'container-drift']:
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                (root / '.gitignore').write_text('output/\n')
                (root / 'input').write_text('original')
                subprocess.run(['git', 'init', '-q'], cwd=root, check=True)
                task = FakeGate(root, failure)
                if failure:
                    with self.assertRaises(RuntimeError):
                        task.execute()
                else:
                    task.execute()
                result = json.loads((task.record / 'result.json').read_text())
                self.assertEqual(result['status'], 'failed' if failure else 'passed')
                self.assertEqual(result['sourceUnchanged'], failure is None)
                if failure in ['missing-runtime', 'local', 'source-drift']:
                    self.assertNotIn('runtime', [name for name, _ in task.visited])
                if failure is None:
                    self.assertEqual(task.visited[0][1], ['make', 'test-local'])
                    self.assertIn('test-deploy-runtime', task.visited[1][1])

    def test_supplied_container_cannot_mount_platform_data(self):
        with tempfile.TemporaryDirectory() as temp:
            task = gate.Gate(Path(temp))
            task.owned = False
            task.container = 'candidate'
            def command(args, **kwargs):
                if args[1:3] == ['context', 'show']:
                    return 'local-test'
                if args[1:3] == ['context', 'inspect']:
                    return json.dumps([{'Endpoints': {'docker': {'Host': 'unix:///test/docker.sock'}}}])
                if 'inspect' in args:
                    return json.dumps([{'State': {'Running': True}, 'Mounts': [{'Destination': '/var/lib/clusterforge-test'}]}])
                self.fail('Runtime executed against a business volume')
            task.command = command
            with patch.dict(os.environ, {}, clear=True), self.assertRaisesRegex(RuntimeError, 'business data'):
                task.runtime()

    def test_local_deployment_stops_before_activation_when_shared_gate_fails(self):
        with tempfile.TemporaryDirectory() as temp:
            task = deploy.Deployment.__new__(deploy.Deployment)
            task.options = argparse.Namespace(check=False, skip_tests=False)
            task.record = Path(temp)
            task.source = Path(temp)
            task.context = 'local-test'
            task.frontend_env = {}
            task.candidate = 'isolated-candidate'
            task.stopped = False
            for name in ['preflight', 'prepare', 'build_image', 'activate', 'cleanup']:
                setattr(task, name, Mock())
            with patch.object(deploy, 'run', side_effect=RuntimeError('shared gate failed')) as run:
                with self.assertRaisesRegex(RuntimeError, 'shared gate'):
                    task.execute()
            self.assertEqual(run.call_args[0][0], ['make', 'test-deploy'])
            self.assertEqual(run.call_args[1]['env']['CLUSTERFORGE_GATE_CONTAINER'], task.candidate)
            self.assertEqual(run.call_args[1]['env']['CLUSTERFORGE_GATE_EVIDENCE_ROOT'], str(task.record / 'gate'))
            task.activate.assert_not_called()
            task.cleanup.assert_called_once()
            self.assertEqual(json.loads((task.record / 'result.json').read_text())['status'], 'not_deployed')

    def test_remote_deployment_gate_failure_never_reaches_ssh_or_upload(self):
        with tempfile.TemporaryDirectory() as temp:
            parent = Path(temp)
            root = parent / 'repo'
            scripts = root / 'scripts'
            scripts.mkdir(parents=True)
            for name in ['deploy-test-88-55.sh', 'deployment_source.py']:
                shutil.copy2(SCRIPTS / name, scripts / name)
            (root / '.gitignore').write_text('web/dist/\n')
            subprocess.run(['git', 'init', '-q'], cwd=root, check=True)
            fake = parent / 'bin'
            fake.mkdir()
            trace = parent / 'trace'
            for command in ['make', 'go', 'pnpm', 'ssh', 'scp']:
                body = '#!/bin/sh\nprintf "%s %s\\n" "'+command+'" "$*" >> "$GATE_TEST_TRACE"\n'
                if command == 'make':
                    body += 'case "$1" in\nbuild-web) mkdir -p web/dist; echo index > web/dist/index.html; echo version > web/dist/version.json;;\ntest-deploy) exit 17;;\n*) exit 99;;\nesac\n'
                else:
                    body += 'exit 99\n'
                path = fake / command
                path.write_text(body)
                path.chmod(0o755)
            env = dict(os.environ, PATH=str(fake)+':'+os.environ['PATH'], GATE_TEST_TRACE=str(trace))
            result = subprocess.run(['bash', str(scripts / 'deploy-test-88-55.sh')], env=env,
                                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT, universal_newlines=True)
            self.assertEqual(result.returncode, 17, result.stdout)
            self.assertEqual(trace.read_text().splitlines(), ['make build-web', 'make test-deploy'])


class RoleResultTests(unittest.TestCase):
    def passed(self):
        return [{'Action': 'pass', 'Package': 'module'+p[1:],
                 'Test': 'TestImageCommandProbeRealAnsible' if p.endswith('sshcheck') else 'TestRequired'} for p in roles.PACKAGES]

    def test_empty_partial_skipped_and_failed_results_are_rejected(self):
        passed = self.passed()
        self.assertEqual(roles.validate_results(passed, 0), 4)
        cases = [([], 0), (passed[:-1], 0), (passed, 1),
                 (passed+[{'Action': 'skip', 'Package': 'module/internal/ansible', 'Test': 'TestRequired/subcase'}], 0),
                 (passed+[{'Action': 'fail', 'Package': 'module/internal/service'}], 0)]
        for events, status in cases:
            with self.subTest(events=events, status=status), self.assertRaises(RuntimeError):
                roles.validate_results(events, status)


if __name__ == '__main__':
    unittest.main(verbosity=2)
