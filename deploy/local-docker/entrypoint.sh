#!/bin/bash
set -euo pipefail
install -d -m 0700 /var/lib/clusterforge-test/ssh /root/.ssh /home/testops/.ssh
install -d -m 0755 /run/sshd /workspace /etc/ansible
if [[ ! -f /var/lib/clusterforge-test/ssh/controller_ed25519 ]]; then
  ssh-keygen -q -t ed25519 -N '' -C clusterforge-docker-test -f /var/lib/clusterforge-test/ssh/controller_ed25519
fi
if [[ ! -f /var/lib/clusterforge-test/ssh/host_ed25519 ]]; then
  ssh-keygen -q -t ed25519 -N '' -C clusterforge-docker-host -f /var/lib/clusterforge-test/ssh/host_ed25519
fi
install -m 0600 /var/lib/clusterforge-test/ssh/controller_ed25519.pub /home/testops/.ssh/authorized_keys
chown -R testops:testops /home/testops/.ssh
awk '{print "127.0.0.1 " $1 " " $2}' /var/lib/clusterforge-test/ssh/host_ed25519.pub > /root/.ssh/known_hosts
chmod 0600 /root/.ssh/known_hosts
install -d -m 0700 /var/lib/clusterforge-test/platform /var/lib/clusterforge-test/platform/playbooks /var/lib/clusterforge-test/platform/runs /var/lib/clusterforge-test/platform/run-archives
ssh_pid=""
platform_pid=""
shutdown() {
  trap - TERM INT
  [[ -z "$platform_pid" ]] || kill -TERM "$platform_pid" 2>/dev/null || true
  [[ -z "$ssh_pid" ]] || kill -TERM "$ssh_pid" 2>/dev/null || true
  wait || true
}
trap 'shutdown; exit 0' TERM INT
/usr/sbin/sshd -D -e -h /var/lib/clusterforge-test/ssh/host_ed25519 &
ssh_pid="$!"
cd /var/lib/clusterforge-test/platform
/opt/clusterforge/platform/newplatform &
platform_pid="$!"
set +e
wait -n "$ssh_pid" "$platform_pid"
status="$?"
shutdown
exit "$status"
