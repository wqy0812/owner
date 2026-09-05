"""Structured observations; cf_gate, not this callback, controls progression."""
import json
import os
import socket
from ansible.plugins.callback import CallbackBase


class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = 'notification'
    CALLBACK_NAME = 'cf_events'
    CALLBACK_NEEDS_ENABLED = True

    def __init__(self):
        super().__init__()
        self.step_id = ''
        self.sequence = 0

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
            self.emit('play_start')

    def v2_playbook_on_task_start(self, task, is_conditional):
        self.emit('task_start', task=task.get_name())

    def v2_playbook_on_handler_task_start(self, task):
        self.emit('handler_start', task=task.get_name())

    def event(self, status, result):
        hidden = result._result.get('_ansible_no_log', False) or result._task.no_log
        data = {'censored': True} if hidden else result._result
        self.emit('result', status=status, host=result._host.get_name(),
                  task=result._task.get_name(), changed=bool(result._result.get('changed')),
                  result=data, ownerTask=result._task._role is not None)

    def v2_runner_on_ok(self, result):
        self.event('ok', result)

    def v2_runner_on_failed(self, result, ignore_errors=False):
        self.event('failed', result)

    def v2_runner_on_unreachable(self, result):
        self.event('unreachable', result)

    def v2_runner_on_skipped(self, result):
        self.event('skipped', result)
