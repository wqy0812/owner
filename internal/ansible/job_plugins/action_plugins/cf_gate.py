"""Synchronous controller gate. A failed acknowledgement fails the play."""
import json
import os
import select
import socket
import subprocess
import sys
from ansible.plugins.action import ActionBase


def exchange(payload):
    payload['token'] = os.environ['CLUSTERFORGE_JOB_TOKEN']
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
        connection.settimeout(30)
        connection.connect(os.environ['CLUSTERFORGE_JOB_SOCKET'])
        connection.sendall(json.dumps(payload).encode() + b'\n')
        # The controller bounds preparation and action time, and cancellation
        # terminates this process. Media preparation may outlast 30 seconds.
        connection.settimeout(None)
        response = json.loads(connection.makefile('rb').readline(1048576))
    if not response.get('ok'):
        raise RuntimeError(response.get('error', 'job controller rejected the event'))
    return response


class ActionModule(ActionBase):
    TRANSFERS_FILES = False

    def run(self, tmp=None, task_vars=None):
        result = super().run(tmp, task_vars)
        try:
            if not os.environ.get('CLUSTERFORGE_JOB_SOCKET') or not os.environ.get('CLUSTERFORGE_JOB_TOKEN'):
                raise RuntimeError('Run this bundle through clusterforge-job')
            response = exchange({'kind': self._task.args['kind'], 'stepId': self._task.args['step_id']})
            if self._task.args.get('watchdog'):
                # A separate, credential-free supervisor survives a hard
                # controller crash and stops the whole Ansible process group.
                # Workers call setsid(), so their own group is not the main
                # Ansible group. Only the launching controller knows that ID.
                group = int(response.get('ansibleGroup', 0))
                if group <= 1:
                    raise RuntimeError('Ansible process group is unavailable')
                supervisor = subprocess.Popen(
                    [sys.executable, os.path.join(os.path.dirname(__file__),
                     'cf_watchdog.py'), str(group)], stdin=subprocess.DEVNULL,
                    stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                    start_new_session=True)
                ready, _, _ = select.select([supervisor.stdout], [], [], 5)
                if not ready or supervisor.stdout.readline() != b'ready\n':
                    supervisor.kill()
                    supervisor.wait()
                    raise RuntimeError('Job supervisor did not start')
                supervisor.stdout.close()
            result.update(changed=False)
        except Exception as error:
            result.update(failed=True, changed=False, msg='Job boundary rejected: ' + str(error))
        return result
