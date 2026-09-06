"""Durable local transport for a sealed native ansible-playbook job.

The platform supplies its transport explicitly. This transport is selected only
at process startup and never serves as a fallback for a failed platform socket.
"""
import collections
import copy
import datetime
import fcntl
import hashlib
import json
import os
import re
import socket
import stat
import subprocess
import sys
import tempfile
import threading
import time
import uuid

from cf_recovery import retry, rollback, action_transition


CONTRACT = 'clusterforge-native-job-v3'


def utcnow():
    return datetime.datetime.utcnow().isoformat(timespec='microseconds') + 'Z'


def encoded(value, sort=False):
    # Match Go's JSON encoding for the sealed manifest, retaining its wire order.
    text = json.dumps(value, ensure_ascii=False, separators=(',', ':'), sort_keys=sort)
    for source, target in (('&', '\\u0026'), ('<', '\\u003c'), ('>', '\\u003e'), ('\u2028', '\\u2028'), ('\u2029', '\\u2029')):
        text = text.replace(source, target)
    return text.encode('utf-8')


def digest(value, sort=False):
    return hashlib.sha256(encoded(value, sort)).hexdigest()


def read_json(path):
    with open(path, encoding='utf-8') as stream:
        return json.load(stream, object_pairs_hook=collections.OrderedDict)


def validate_bundle(root):
    manifest = read_json(os.path.join(root, 'manifest.json'))
    expected = manifest.get('digest')
    candidate = copy.deepcopy(manifest)
    candidate['digest'] = ''
    if manifest.get('contract') != CONTRACT or not expected or digest(candidate) != expected:
        raise ValueError('native job manifest digest or contract mismatch')
    if manifest.get('entryPoint') != 'site.yml' or manifest.get('fullPlanDigest') != digest(manifest['plan']):
        raise ValueError('native job full plan identity mismatch')
    members = manifest['files']
    for name, expected in members.items():
        path = os.path.join(root, name)
        if os.path.isabs(name) or '..' in name.split('/') or os.path.realpath(path) != os.path.abspath(path):
            raise ValueError('unsafe job member: ' + name)
        if not stat.S_ISREG(os.lstat(path).st_mode):
            raise ValueError('job member is not a regular file: ' + name)
        with open(path, 'rb') as stream:
            actual = hashlib.sha256(stream.read()).hexdigest()
        if actual != expected:
            raise ValueError('job file changed: ' + name)
    for base, directories, files in os.walk(root):
        for name in directories:
            if os.path.islink(os.path.join(base, name)):
                raise ValueError('job cannot contain directory symlinks')
        for name in files:
            relative = os.path.relpath(os.path.join(base, name), root).replace(os.sep, '/')
            if relative != 'manifest.json' and relative not in members:
                raise ValueError('unsealed job member: ' + relative)
    return manifest


def persist(path, value):
    value = copy.deepcopy(value)
    value['checksum'] = ''
    value['checksum'] = digest(value, True)
    descriptor, temporary = tempfile.mkstemp(prefix='.receipt-', dir=os.path.dirname(path))
    try:
        with os.fdopen(descriptor, 'wb') as stream:
            stream.write(encoded(value, True))
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        directory = os.open(os.path.dirname(path), os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def read_receipt(path):
    if os.path.islink(path) or not stat.S_ISREG(os.lstat(path).st_mode):
        raise ValueError('receipt must be a regular file')
    receipt = read_json(path)
    checksum = receipt.get('checksum')
    receipt['checksum'] = ''
    if not checksum or digest(receipt, True) != checksum:
        raise ValueError('execution receipt checksum mismatch')
    return receipt


def assign_backup_identity(plan, execution_id):
    replacements = {}
    for step in plan['steps']:
        values = step.get('variables') or {}
        old = values.get('clusterforge_backup_ref')
        if step['phase'] == 'execute' and old and values.get('clusterforge_backup_operation') == 'capture':
            replacements[old] = os.path.join(os.path.dirname(os.path.dirname(old)), execution_id, os.path.basename(old))
    captured = utcnow()
    def replace(value):
        if isinstance(value, str):
            for old, new in replacements.items():
                value = value.replace(old, new)
            return value
        if isinstance(value, list):
            return [replace(child) for child in value]
        if isinstance(value, dict):
            value = dict((key, replace(child)) for key, child in value.items())
            if value.get('clusterforge_backup_ref') in replacements.values():
                metadata = value.get('clusterforge_backup_metadata')
                if isinstance(metadata, dict):
                    metadata.update(install_run_id=execution_id, captured_at=captured)
            if value.get('backupRef') in replacements.values():
                if 'installRunId' in value:
                    value['installRunId'] = execution_id
                if isinstance(value.get('backup'), dict):
                    value['backup'].update(installRunId=execution_id, capturedAt=captured)
            return value
        return value
    return replace(plan)


class NativeController:
    def __init__(self, root, executable, runtime):
        self.root = os.path.realpath(root)
        self.manifest = validate_bundle(self.root)
        if self.manifest['plan']['runtime'] != runtime:
            raise ValueError('Ansible/Python runtime differs from locked job: ' + str(runtime))
        self.executable = executable
        self.token = uuid.uuid4().hex
        self.socket_dir = tempfile.mkdtemp(prefix='cfn-')
        self.socket_path = os.path.join(self.socket_dir, 'control.sock')
        self.listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.listener.bind(self.socket_path)
        self.listener.listen(16)
        self.mutex = threading.Lock()
        self.failure = ''
        self.finished = False
        self.closed = False
        self.initialized = False
        self.watchdog_seen = False
        self.watchdog_done = threading.Event()
        self.next = 0
        self.sequence = 0
        self.active = None
        self.deadline = 0
        self.secrets = []
        self.receipt = None
        self.lock = None
        self.events = None
        self.group = os.getpgrp()
        if self.group != os.getpid():
            os.setpgid(0, 0)
            self.group = os.getpgrp()
        os.environ['CLUSTERFORGE_JOB_SOCKET'] = self.socket_path
        os.environ['CLUSTERFORGE_JOB_TOKEN'] = self.token
        thread = threading.Thread(target=self.serve)
        thread.daemon = True
        thread.start()

    def redact(self, value):
        text = value if isinstance(value, str) else json.dumps(value, ensure_ascii=False, default=str)
        variants = set(self.secrets)
        for secret in self.secrets:
            variants.add(json.dumps(secret, ensure_ascii=False)[1:-1])
            variants.add(json.dumps(secret, ensure_ascii=True)[1:-1])
        for secret in sorted(variants, key=len, reverse=True):
            text = text.replace(secret, '[REDACTED]')
        return text

    def serve(self):
        while not self.closed:
            try:
                connection, _ = self.listener.accept()
            except OSError:
                break
            thread = threading.Thread(target=self.client, args=(connection,))
            thread.daemon = True
            thread.start()

    def client(self, connection):
        with connection:
            connection.settimeout(35)
            event = {}
            try:
                event = json.loads(connection.makefile('rb').readline(8 << 20))
                if event.get('token') != self.token:
                    raise ValueError('invalid controller token')
                if event['kind'] == 'heartbeat':
                    self.watchdog_seen = True
                    if self.deadline and time.monotonic() > self.deadline:
                        self.failure = 'stage timeout'
                    if self.failure:
                        raise ValueError(self.failure)
                    response = {}
                else:
                    with self.mutex:
                        if self.failure:
                            raise ValueError(self.failure)
                        response = self.handle(event)
                response.update(ok=True, done=self.finished, ansibleGroup=self.group)
            except Exception as error:
                self.failure = self.redact(str(error))
                response = dict(ok=False, error=self.failure, ansibleGroup=self.group)
            try:
                connection.sendall(encoded(response) + b'\n')
                if event.get('kind') == 'heartbeat' and response.get('ok') and response.get('done'):
                    self.watchdog_done.set()
            except OSError:
                if not self.finished:
                    self.failure = 'controller acknowledgement was lost'

    def initialize(self, event):
        if self.initialized:
            raise ValueError('job was already initialized')
        self.initialized = True
        options = event.get('options') or {}
        if not isinstance(options, dict) or set(options) - {'operation', 'results', 'expectedPlanDigest', 'nodes'}:
            raise ValueError('invalid cf_job options; locked inputs cannot be overridden')
        operation = options.get('operation', 'install')
        from cf_resources import validate as validate_resources
        validate_resources(self.manifest['plan'])
        if operation not in ('install', 'resume', 'rollback-preview', 'rollback'):
            raise ValueError('unknown job operation')
        results = options.get('results', '')
        if not os.path.isabs(results) or os.path.islink(results) or os.path.commonpath([self.root, os.path.realpath(results)]) == self.root:
            raise ValueError('cf_job.results must be an absolute private directory outside the job bundle')
        results = os.path.realpath(results)
        credentials = event.get('credentials') or {}
        for name in (self.manifest['plan'].get('requiredCredentials') or []):
            if operation != 'rollback-preview' and not credentials.get(name):
                raise ValueError('missing credential ' + name)
        def secrets(value):
            if isinstance(value, str) and value:
                self.secrets.append(value)
            elif isinstance(value, dict):
                for child in value.values(): secrets(child)
            elif isinstance(value, list):
                for child in value: secrets(child)
        secrets(credentials)
        if operation != 'rollback-preview':
            os.makedirs(results, mode=0o700, exist_ok=True)
        if os.stat(results).st_mode & 0o077:
            raise ValueError('result directory must have mode 0700')
        descriptor = os.open(os.path.join(results, '.lock'), os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        self.lock = os.fdopen(descriptor, 'a+b')
        try:
            fcntl.flock(self.lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError:
            raise ValueError('result directory is in use by another job')
        self.receipt_path = os.path.join(results, 'receipt.json')
        if operation == 'install':
            if os.path.lexists(self.receipt_path):
                raise ValueError('results already exist; use resume or a new result directory')
            plan = assign_backup_identity(copy.deepcopy(self.manifest['plan']), 'native-' + uuid.uuid4().hex)
            self.receipt = dict(contract=CONTRACT, bundleDigest=self.manifest['digest'], originalPlan=plan,
                                actions={}, attempts=[], checksum='')
            stages = plan['steps']
            run_operation = 'install'
        else:
            self.receipt = read_receipt(self.receipt_path)
            if self.receipt['bundleDigest'] != self.manifest['digest'] or self.receipt['contract'] != CONTRACT:
                raise ValueError('receipt belongs to a different job bundle')
            plan = self.receipt['originalPlan']
            last = self.receipt['attempts'][-1]
            run_operation = last['operation']
            if operation == 'resume':
                stages = retry(dict(steps=last['steps']), last['results'])
            else:
                if last['operation'] == 'rollback' and last['status'] != 'succeeded':
                    raise ValueError('rollback already started; resume its saved attempt')
                stages = rollback(plan, self.receipt['actions'], options.get('nodes'))
                expected = digest(dict(bundleDigest=self.manifest['digest'], actions=self.receipt['actions'], steps=stages), True)
                if operation == 'rollback-preview':
                    self.selected = []
                    self.finished = True
                    return dict(watchdog=False, preview=dict(planDigest=expected, steps=stages))
                if options.get('expectedPlanDigest') != expected:
                    raise ValueError('rollback requires expectedPlanDigest from a current rollback-preview')
                run_operation = 'rollback'
        # Parse every Role before any mutation. This child invocation is a
        # syntax-only preflight, never a second formal installation process.
        env = dict(os.environ, CLUSTERFORGE_JOB_PREFLIGHT='1', PYTHONDONTWRITEBYTECODE='1')
        command = [self.executable, '-i', os.path.join(self.root, 'inventory.ini'), '--syntax-check', os.path.join(self.root, 'syntax.yml')]
        check = subprocess.run(command, cwd=self.root, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=60)
        if check.returncode:
            raise ValueError('role syntax preflight failed: ' + self.redact(check.stdout.decode('utf-8', 'replace')))
        self.selected = stages
        self.attempt = dict(number=len(self.receipt['attempts']) + 1, operation=run_operation, status='running',
                            startedAt=utcnow(), steps=stages, results=[])
        self.receipt['attempts'].append(self.attempt)
        persist(self.receipt_path, self.receipt)
        event_path = os.path.join(results, 'events.jsonl')
        if os.path.islink(event_path):
            raise ValueError('event log cannot be a symlink')
        self.events = open(event_path, 'ab', buffering=0)
        os.chmod(event_path, 0o600)
        self.media_ready = False
        return dict(watchdog=True)

    def handle(self, event):
        kind = event['kind']
        if kind == 'abort':
            raise ValueError(event.get('message', 'job preflight failed'))
        if kind == 'initialize':
            return self.initialize(event)
        if not self.initialized:
            raise ValueError('job has not initialized its durable state')
        if kind == 'finish':
            if self.next != len(self.selected) or self.active is not None:
                raise ValueError('job ended without acknowledging every selected stage')
            if not self.finished:
                self.attempt.update(status='succeeded', finishedAt=utcnow())
                persist(self.receipt_path, self.receipt)
                self.finished = True
            return {}
        physical = event.get('stepId', '')
        step = self.selected[self.next] if self.next < len(self.selected) else None
        source_id = step['id'][8:] if step and step['id'].startswith('refresh-') else (step or {}).get('id')
        if kind in ('begin', 'end', 'current') and physical != source_id:
            # Sealed site.yml walks the full original/recovery graph. Selection
            # only suppresses acknowledged stages; it cannot inject source.
            if self.active is not None:
                raise ValueError('out-of-order stage boundary')
            return dict(run=False)
        if kind == 'current':
            return dict(run=self.active is not None, stepId=(step or {}).get('id', ''))
        if kind == 'begin':
            if self.active is not None:
                raise ValueError('stage already active')
            validate_bundle(self.root)
            if not self.media_ready and step.get('stage') != 'source_verify':
                from cf_media import prepare, verify_steps
                self.deadline = time.monotonic() + 1800
                if self.attempt['operation'] != 'rollback':
                    prepare(self.receipt['originalPlan'].get('metadata') or {})
                verify_steps(self.selected)
                self.media_ready = True
            self.deadline = time.monotonic() + step['timeoutSeconds']
            self.active = dict(stepId=step['id'], status='running', startedAt=utcnow(), hosts={})
            self.play_seen = False
            self.owner_observed = set()
            self.attempt['results'].append(self.active)
            self.receipt['actions'] = action_transition(self.receipt['actions'], self.receipt['originalPlan'], 'begin', step)
            persist(self.receipt_path, self.receipt)
            values = step.get('variables') or {}
            context = dict(environmentId=self.receipt['originalPlan'].get('environmentId'), releaseId=step.get('releaseId'),
                           componentId=step.get('componentId'), nodeId=step.get('nodeId'), stepId=step['id'],
                           actionId=step.get('actionId'), parentActionId=step.get('parentActionId'), phase=step['phase'],
                           backupRef=values.get('clusterforge_backup_ref'))
            return dict(run=True, inputs=values, context=context)
        if self.active is None or step is None:
            raise ValueError('event outside active stage')
        if kind == 'end':
            if not self.play_seen or not self.active['hosts']:
                raise ValueError('missing stage observations or target hosts')
            for host, recap in self.active['hosts'].items():
                if recap.get('failed') or recap.get('unreachable'):
                    raise ValueError('stage failed on host ' + host)
                if (step['action'] == 'check' or step.get('sourceType') == 'scenario_acceptance') and host not in self.owner_observed:
                    raise ValueError('check did not execute on host ' + host)
            validate_bundle(self.root)
            self.receipt['actions'] = action_transition(self.receipt['actions'], self.receipt['originalPlan'], 'end', step)
            self.active.update(status='succeeded', finishedAt=utcnow())
            persist(self.receipt_path, self.receipt)
            self.active = None
            self.deadline = 0
            self.next += 1
            return {}
        if event.get('stepId') != step['id'] or event.get('sequence') != self.sequence + 1:
            raise ValueError('missing, duplicated or out-of-order job event')
        self.sequence += 1
        if kind == 'play_start':
            if self.play_seen:
                raise ValueError('duplicate stage play')
            self.play_seen = True
        if kind == 'result':
            host = event['host']
            recap = self.active['hosts'].setdefault(host, dict(ok=0, changed=0, failed=0, unreachable=0, skipped=0))
            status = event['status']
            if status not in ('ok', 'failed', 'unreachable', 'skipped'):
                raise ValueError('invalid task status')
            recap[status] += 1
            if event.get('changed'): recap['changed'] += 1
            if event.get('ownerTask') and status == 'ok': self.owner_observed.add(host)
        if kind == 'waiting':
            import datetime
            remaining = max(0, self.deadline-time.monotonic())
            event['result']['waiting']['deadline'] = (datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(seconds=remaining)).isoformat()
        safe = dict(event, token='', attempt=self.attempt['number'])
        self.events.write(self.redact(safe).encode('utf-8') + b'\n')
        os.fsync(self.events.fileno())
        if kind == 'result' and event['status'] in ('failed', 'unreachable'):
            self.active.update(status='failed', finishedAt=utcnow(), error=self.redact(event.get('task', 'task failed')))
            self.attempt.update(status='failed', finishedAt=utcnow())
            persist(self.receipt_path, self.receipt)
            raise ValueError('stage failed: ' + event.get('task', event['host']))
        return {}

    def close(self):
        if self.closed: return
        # Keep the transport alive until the supervisor has received durable
        # completion. Closing first races its next heartbeat and can cause it
        # to terminate an otherwise successful ansible-playbook process.
        if self.finished and self.watchdog_seen:
            self.watchdog_done.wait(5)
        self.closed = True
        self.listener.close()
        if self.events: self.events.close()
        if self.lock: self.lock.close()
        try:
            os.unlink(self.socket_path)
            os.rmdir(self.socket_dir)
        except OSError:
            pass
