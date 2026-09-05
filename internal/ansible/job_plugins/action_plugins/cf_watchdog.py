"""Stop orphaned Ansible groups when the durable controller disappears."""
import json
import os
import signal
import socket
import sys
import time


def supervise(group):
    ready = False
    while True:
        try:
            os.killpg(group, 0)
        except ProcessLookupError:
            return
        try:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
                connection.settimeout(3)
                connection.connect(os.environ['CLUSTERFORGE_JOB_SOCKET'])
                connection.sendall(json.dumps({'kind': 'heartbeat',
                                   'token': os.environ['CLUSTERFORGE_JOB_TOKEN']}).encode() + b'\n')
                response = json.loads(connection.makefile('rb').readline(65536))
                if not response.get('ok'):
                    raise RuntimeError('controller unavailable')
                if response.get('ansibleGroup') != group:
                    raise RuntimeError('controller process identity changed')
                if response.get('done'):
                    return
                if not ready:
                    sys.stdout.write('ready\n')
                    sys.stdout.flush()
                    sys.stdout.close()
                    ready = True
        except Exception:
            try:
                # TERM lets Ansible propagate cancellation to detached
                # workers; KILL bounds a controller that cannot respond.
                os.killpg(group, signal.SIGTERM)
                time.sleep(1)
                os.killpg(group, signal.SIGKILL)
            except ProcessLookupError:
                pass
            return
        time.sleep(0.5)


if __name__ == '__main__':
    supervise(int(sys.argv[1]))
