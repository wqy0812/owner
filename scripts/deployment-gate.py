#!/usr/bin/env python3
"""One regression gate for local and remote deployments; never activates a build."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid

sys.dont_write_bytecode = True
from deployment_source import source_digest, source_files

ROOT = Path(__file__).resolve().parents[1]
IMAGE = 'clusterforge-test-ubuntu:18.04-ansible2.8.8'
ANSIBLE = '/opt/ansible/bin/ansible-playbook'
PYTHON = '/opt/ansible/bin/python3'


class Gate:
    def __init__(self, root=ROOT):
        self.root = root
        self.identifier = time.strftime('%Y%m%d-%H%M%S') + '-' + uuid.uuid4().hex[:8]
        evidence_root = Path(os.environ.get('CLUSTERFORGE_GATE_EVIDENCE_ROOT', str(root / 'output/deployment-gate')))
        self.record = evidence_root.resolve() / self.identifier
        self.container = os.environ.get('CLUSTERFORGE_GATE_CONTAINER', '')
        self.owned = not self.container
        self.created = False
        self.docker = ['docker']
        self.result = {'status': 'running', 'stages': [], 'sourceUnchanged': False}

    def command(self, args, **kwargs):
        return subprocess.check_output(args, universal_newlines=True, stderr=subprocess.STDOUT, **kwargs).strip()

    def stage(self, name, args):
        print('==> Deployment gate: ' + name, flush=True)
        entry = {'name': name, 'command': args, 'status': 'running'}
        self.result['stages'].append(entry)
        self.save()
        with (self.record / (name + '.log')).open('w') as log:
            status = subprocess.call(args, cwd=self.root, stdout=log, stderr=subprocess.STDOUT,
                                     env=dict(os.environ, CLUSTERFORGE_TEST_EVIDENCE_ROOT=str(self.record / 'browser')))
        entry.update(status='passed' if status == 0 else 'failed', exitCode=status)
        self.save()
        if status:
            raise RuntimeError(name + ' failed; see ' + str(self.record / (name + '.log')))
        self.check_source()

    def save(self):
        (self.record / 'result.json').write_text(json.dumps(self.result, indent=2) + '\n')

    def check_source(self):
        if source_digest(self.root, source_files(self.root)) != self.source_hash:
            raise RuntimeError('Workspace changed during deployment gate; rerun when edits finish')

    def runtime(self):
        context = os.environ.get('CLUSTERFORGE_GATE_DOCKER_CONTEXT') or self.command(['docker', 'context', 'show'])
        self.docker = ['docker', '--context', context]
        info = json.loads(self.command(['docker', 'context', 'inspect', context]))[0]
        endpoint = info['Endpoints']['docker']['Host']
        if not endpoint.startswith('unix://') or os.environ.get('DOCKER_HOST', endpoint) != endpoint:
            raise RuntimeError('Deployment tests require the local Unix Docker endpoint')
        if self.owned:
            image_id = json.loads(self.command(self.docker + ['image', 'inspect', IMAGE]))[0]['Id']
            self.container = 'clusterforge-gate-' + self.identifier
            args = self.docker + ['run', '-d', '--init', '--name', self.container,
                                  '--cpus', '2', '--memory', '3g', '--pids-limit', '512']
            # Reuse only compiler/module caches, never platform/workspace volumes.
            try:
                existing = json.loads(self.command(self.docker + ['inspect', 'clusterforge-test-ubuntu']))[0]
            except subprocess.CalledProcessError:
                existing = {}
            for mount in existing.get('Mounts', []):
                if mount['Type'] == 'volume' and mount['Destination'] in ['/go/pkg/mod', '/var/cache/go-build']:
                    args += ['--mount', 'type=volume,src='+mount['Name']+',dst='+mount['Destination']]
            self.created = True
            self.command(args + [image_id])
        info = json.loads(self.command(self.docker + ['inspect', self.container]))[0]
        if not info['State']['Running']:
            raise RuntimeError('The isolated test container is not running')
        # A caller may supply a deployment candidate, never a container sharing business state.
        for mount in info.get('Mounts', []):
            destination = mount['Destination'].rstrip('/')
            if destination not in ['/go/pkg/mod', '/var/cache/go-build']:
                raise RuntimeError('The test container must not mount business data or a working checkout')
        self.command(self.docker + ['exec', self.container, PYTHON, '-c',
                     "import ansible,sys; assert ansible.__version__ == '2.8.8'; assert sys.version_info[:3] == (3,6,9)"])
        version = self.command(self.docker + ['exec', self.container, ANSIBLE, '--version'])
        (self.record / 'runtime-version.log').write_text(version + '\n')
        self.result['runtime'] = {'container': self.container, 'imageId': info['Image'],
                                  'ansible': '2.8.8', 'python': '3.6.9', 'dockerContext': context}
        self.save()
        deadline = time.monotonic() + 45
        while True:
            try:
                self.command(self.docker + ['exec', self.container, '/usr/local/bin/clusterforge-test-healthcheck'])
                break
            except subprocess.CalledProcessError:
                if time.monotonic() >= deadline:
                    raise RuntimeError('The isolated runtime did not become ready')
                time.sleep(1)

    def copy_source(self):
        self.checkout = '/workspace/gates/' + self.identifier
        self.command(self.docker + ['exec', self.container, 'mkdir', '-p', self.checkout])
        with tempfile.TemporaryFile() as stream:
            with tarfile.open(fileobj=stream, mode='w') as archive:
                for relative in self.files:
                    path = self.root / relative
                    if path.exists() or path.is_symlink():
                        archive.add(path, arcname=str(relative), recursive=False)
            self.check_source()
            stream.seek(0)
            self.command(self.docker + ['cp', '-', self.container + ':' + self.checkout], stdin=stream)
        self.check_container_source()

    def check_container_source(self):
        script = ('from pathlib import Path; import json,sys; from scripts.deployment_source import source_digest; '
                  'assert source_digest(Path.cwd(), [Path(p) for p in json.load(sys.stdin)]) == sys.argv[1], '
                  '"Container source differs from the deployment checkout"')
        with tempfile.TemporaryFile() as stream:
            stream.write(json.dumps([str(p) for p in self.files]).encode())
            stream.seek(0)
            self.command(self.docker + ['exec', '-i', '-w', self.checkout, self.container, PYTHON, '-c', script,
                                        self.source_hash], stdin=stream)

    def execute(self):
        self.record.mkdir(parents=True, mode=0o700)
        print('Deployment gate evidence: ' + str(self.record), flush=True)
        try:
            self.files = source_files(self.root)
            self.source_hash = source_digest(self.root, self.files)
            self.result['sourceSha256'] = self.source_hash
            self.result['sourceFiles'] = [str(p) for p in self.files]
            self.save()
            self.runtime()
            self.copy_source()
            self.stage('local', ['make', 'test-local'])
            self.stage('runtime', self.docker + ['exec', '-w', self.checkout, self.container,
                       'make', 'test-deploy-runtime', 'ANSIBLE_PLAYBOOK=' + ANSIBLE])
            self.check_container_source()
            self.check_source()
            self.result.update(status='passed', sourceUnchanged=True)
        except BaseException as error:
            self.result.update(status='failed', error=str(error))
            raise
        finally:
            try:
                if self.created:
                    try:
                        (self.record / 'container.log').write_text(self.command(self.docker + ['logs', self.container]) + '\n')
                    finally:
                        self.command(self.docker + ['rm', '-f', self.container])
            except subprocess.CalledProcessError as error:
                self.result.update(status='failed', cleanupError=str(error))
                raise
            finally:
                self.save()
        print('Deployment gate passed: ' + str(self.record), flush=True)


if __name__ == '__main__':
    def interrupted(signum, frame):
        raise KeyboardInterrupt('Deployment gate interrupted by signal ' + str(signum))
    signal.signal(signal.SIGTERM, interrupted)
    try:
        Gate().execute()
    except (RuntimeError, subprocess.CalledProcessError, OSError, KeyboardInterrupt) as error:
        print('error: ' + str(error), file=sys.stderr)
        sys.exit(1)
