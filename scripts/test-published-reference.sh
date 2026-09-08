#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
: "${ANSIBLE_PLAYBOOK:?Set ANSIBLE_PLAYBOOK to the pinned Ansible 2.8.8 executable}"
test -x "$ANSIBLE_PLAYBOOK"
ansible_python="${ANSIBLE_PYTHON:-$(dirname "$ANSIBLE_PLAYBOOK")/python3}"
exec "$ansible_python" scripts/test-published-reference.py
