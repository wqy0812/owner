#!/bin/sh
set -eu

if [ -z "${ANSIBLE_PLAYBOOK:-}" ] || ! command -v "$ANSIBLE_PLAYBOOK" >/dev/null 2>&1; then
  echo "Set ANSIBLE_PLAYBOOK to the required Ansible executable" >&2
  exit 1
fi
export ANSIBLE_PLAYBOOK

snapshot_root="examples/ansible/openfuyao"
inventory="$snapshot_root/component-bke-contract-test.inventory.ini"
variables="$snapshot_root/component-bke-contract-test-vars.yml"
contract="$snapshot_root/component-bke-contract-test.platform.yml"
ansible_tmp="$(mktemp -d /tmp/newplatform-openfuyao-ansible.XXXXXX)"
trap 'rm -rf "$ansible_tmp"' EXIT INT TERM

export ANSIBLE_LOCAL_TEMP="$ansible_tmp/local"
export ANSIBLE_REMOTE_TEMP="$ansible_tmp/remote"
export ANSIBLE_ROLES_PATH="$snapshot_root/roles"

for playbook in "$snapshot_root"/component-bke-*.platform.yml; do
  case "$playbook" in
    *contract-test.platform.yml) continue ;;
  esac
  "$ANSIBLE_PLAYBOOK" -i "$inventory" -e "@$variables" --syntax-check "$playbook" >/dev/null
  "$ANSIBLE_PLAYBOOK" -i "$inventory" -e "@$variables" --list-tasks "$playbook" >/dev/null
done

if "$ANSIBLE_PLAYBOOK" -i "$inventory" \
  -e "@$variables" \
  -e target_host_group=management_cluster_k8smaster \
  -e cluster_role= \
  "$contract" >/dev/null 2>&1; then
  echo "OpenFuyao contract did not fail closed without cluster_role" >&2
  exit 1
fi

if "$ANSIBLE_PLAYBOOK" -i "$inventory" \
  -e "@$variables" \
  -e target_host_group=work_cluster_k8smaster \
  -e cluster_role=manager "$contract" >/dev/null 2>&1; then
  echo "OpenFuyao contract accepted a manager role on a work-cluster host group" >&2
  exit 1
fi

if "$ANSIBLE_PLAYBOOK" -i "$inventory" \
  -e "@$variables" \
  -e target_host_group=work_cluster_k8smaster \
  -e cluster_role=work \
  -e ENV_FILESTATION_URL= "$contract" >/dev/null 2>&1; then
  echo "OpenFuyao contract did not fail closed without a branch variable" >&2
  exit 1
fi

if "$ANSIBLE_PLAYBOOK" -i "$inventory" \
  -e "@$variables" \
  -e target_host_group=work_cluster_k8smaster \
  -e cluster_role=work \
  -e ENV_DOCKER_SECRET_PASSWORD= "$contract" >/dev/null 2>&1; then
  echo "OpenFuyao contract did not fail closed without a credential variable" >&2
  exit 1
fi

"$ANSIBLE_PLAYBOOK" -i "$inventory" \
  -e "@$variables" \
  -e target_host_group=management_cluster_k8smaster \
  -e cluster_role=manager "$contract" >/dev/null
"$ANSIBLE_PLAYBOOK" -i "$inventory" \
  -e "@$variables" \
  -e target_host_group=work_cluster_k8smaster \
  -e cluster_role=work "$contract" >/dev/null

echo "OpenFuyao adapter syntax, task listing, and manager/work fail-closed gates passed"
