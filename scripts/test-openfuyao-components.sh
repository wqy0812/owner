#!/bin/sh
set -eu

if ! command -v ansible-playbook >/dev/null 2>&1; then
  echo "ansible-playbook is not installed; skipping OpenFuyao component gates"
  exit 0
fi

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
  ansible-playbook -i "$inventory" -e "@$variables" --syntax-check "$playbook" >/dev/null
  ansible-playbook -i "$inventory" -e "@$variables" --list-tasks "$playbook" >/dev/null
done

if ansible-playbook -i "$inventory" \
  -e "@$variables" \
  -e target_host_group=management_cluster_k8smaster \
  -e cluster_role= \
  "$contract" >/dev/null 2>&1; then
  echo "OpenFuyao contract did not fail closed without cluster_role" >&2
  exit 1
fi

if ansible-playbook -i "$inventory" \
  -e "@$variables" \
  -e target_host_group=work_cluster_k8smaster \
  -e cluster_role=manager "$contract" >/dev/null 2>&1; then
  echo "OpenFuyao contract accepted a manager role on a work-cluster host group" >&2
  exit 1
fi

if ansible-playbook -i "$inventory" \
  -e "@$variables" \
  -e target_host_group=work_cluster_k8smaster \
  -e cluster_role=work \
  -e ENV_FILESTATION_URL= "$contract" >/dev/null 2>&1; then
  echo "OpenFuyao contract did not fail closed without a branch variable" >&2
  exit 1
fi

if ansible-playbook -i "$inventory" \
  -e "@$variables" \
  -e target_host_group=work_cluster_k8smaster \
  -e cluster_role=work \
  -e ENV_DOCKER_SECRET_PASSWORD= "$contract" >/dev/null 2>&1; then
  echo "OpenFuyao contract did not fail closed without a credential variable" >&2
  exit 1
fi

ansible-playbook -i "$inventory" \
  -e "@$variables" \
  -e target_host_group=management_cluster_k8smaster \
  -e cluster_role=manager "$contract" >/dev/null
ansible-playbook -i "$inventory" \
  -e "@$variables" \
  -e target_host_group=work_cluster_k8smaster \
  -e cluster_role=work "$contract" >/dev/null

echo "OpenFuyao adapter syntax, task listing, and manager/work fail-closed gates passed"
