"""Structured observations; cf_gate, not this callback, controls progression."""
import json
import os
import socket
import sys
from ansible.plugins.callback import CallbackBase


class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = 'notification'
    CALLBACK_NAME = 'cf_events'
    CALLBACK_NEEDS_ENABLED = True
    CALLBACK_NEEDS_WHITELIST = True

    def __init__(self):
        super().__init__()
        self.step_id = ''
        self.sequence = 0
        self.native = None

    def v2_playbook_on_start(self, playbook):
        if os.environ.get('CLUSTERFORGE_JOB_PREFLIGHT') == '1':
            return
        mode = os.environ.get('CLUSTERFORGE_JOB_TRANSPORT', 'native')
        if mode == 'platform':
            if not os.environ.get('CLUSTERFORGE_JOB_SOCKET') or not os.environ.get('CLUSTERFORGE_JOB_TOKEN'):
                raise RuntimeError('platform transport is unavailable')
            return
        if mode != 'native':
            raise RuntimeError('unknown job transport')
        os.environ['CLUSTERFORGE_JOB_TRANSPORT'] = 'native'
        root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        sys.path.insert(0, root)
        from ansible import __version__
        from ansible import context
        from ansible.parsing.dataloader import DataLoader
        from ansible.utils.vars import load_extra_vars
        from cf_native import NativeController
        try:
            if not sys.dont_write_bytecode:
                raise RuntimeError('set PYTHONDONTWRITEBYTECODE=1 to keep the job bundle immutable')
            for option in ('subset', 'skip_tags', 'start_at_task', 'step', 'check'):
                if context.CLIARGS.get(option):
                    raise RuntimeError('native jobs cannot override task selection: ' + option)
            if set(context.CLIARGS.get('tags') or ['all']) != {'all'}:
                raise RuntimeError('native jobs cannot override task tags')
            inventories = context.CLIARGS.get('inventory') or []
            if len(inventories) != 1 or os.path.realpath(inventories[0]) != os.path.join(root, 'inventory.ini'):
                raise RuntimeError('use the sealed inventory.ini')
            allowed = {'cf_job', 'cf_credentials', 'ansible_user', 'ansible_password', 'ansible_ssh_private_key_file', 'ansible_become_password', 'ansible_python_interpreter'}
            if set(load_extra_vars(DataLoader())) - allowed:
                raise RuntimeError('extra variables cannot replace locked job inputs')
            self.native = NativeController(root, sys.argv[0], dict(ansibleCore=__version__, python='.'.join(str(v) for v in sys.version_info[:3])))
            from ansible.utils.display import Display
            original_display = Display.display
            native = self.native
            def redacted_display(display, message, *args, **kwargs):
                return original_display(display, native.redact(message), *args, **kwargs)
            Display.display = redacted_display
        except Exception as error:
            self._display.error(str(error))
            # Ansible treats callback exceptions as warnings. Startup must not
            # continue with an absent controller or invalid immutable inputs.
            raise SystemExit(2)

    def emit(self, kind, **fields):
        if not self.step_id:
            return
        self.sequence += 1
        payload = dict(kind=kind, stepId=self.step_id, sequence=self.sequence,
                       token=os.environ['CLUSTERFORGE_JOB_TOKEN'], **fields)
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
            connection.settimeout(30)
            connection.connect(os.environ['CLUSTERFORGE_JOB_SOCKET'])
            connection.sendall(json.dumps(payload, default=str).encode() + b'\n')
            result = json.loads(connection.makefile('rb').readline(1048576))
            if not result.get('ok'):
                raise RuntimeError('job event rejected: ' + result.get('error', 'unknown'))

    def v2_playbook_on_play_start(self, play):
        self.step_id = play.get_vars().get('cf_step_id', '')
        if self.step_id:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
                connection.settimeout(30)
                connection.connect(os.environ['CLUSTERFORGE_JOB_SOCKET'])
                connection.sendall(json.dumps(dict(kind='current', stepId=self.step_id, token=os.environ['CLUSTERFORGE_JOB_TOKEN'])).encode() + b'\n')
                response = json.loads(connection.makefile('rb').readline(1048576))
                if not response.get('ok'):
                    raise SystemExit(2)
                self.step_id = response.get('stepId', '') if response.get('run') else ''
        if self.step_id:
            self.emit('play_start')

    def v2_playbook_on_stats(self, stats):
        if self.native:
            incomplete = not self.native.finished
            if incomplete:
                self._display.error(self.native.failure or 'job ended without a durable completion receipt')
            self.native.close()
            if incomplete:
                raise SystemExit(2)

    def v2_playbook_on_task_start(self, task, is_conditional):
        self.emit('task_start', task=task.get_name())

    def v2_playbook_on_handler_task_start(self, task):
        self.emit('handler_start', task=task.get_name())

    def event(self, status, result):
        hidden = result._result.get('_ansible_no_log', False) or result._task.no_log
        data = {'censored': True} if hidden else result._result
        self.emit('result', status=status, host=result._host.get_name(),
                  task='受保护任务' if hidden else result._task.get_name(), changed=bool(result._result.get('changed')),
                  result=data, ownerTask=result._task._role is not None)

    def v2_runner_on_ok(self, result):
        self.event('ok', result)

    def v2_runner_on_failed(self, result, ignore_errors=False):
        self.event('failed', result)

    def v2_runner_on_unreachable(self, result):
        self.event('unreachable', result)

    def v2_runner_on_skipped(self, result):
        self.event('skipped', result)

    def v2_runner_retry(self, result):
        hidden = result._result.get('_ansible_no_log', False) or result._task.no_log
        data = result._result
        observed = '未上报' if hidden else str(data.get('stdout', data.get('msg', data.get('rc', '未上报'))))[:2000]
        self.emit('waiting', host=result._host.get_name(), task='受保护任务' if hidden else result._task.get_name(),
                  result=dict(waiting=dict(object='受保护对象' if hidden else result._task.get_name(),
                              expected='已隐藏' if hidden else str(result._task.until or '任务成功')[:1000],
                              observed=observed, attempt=int(data.get('attempts', 0)))))
