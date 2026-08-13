#!/bin/sh
set -eu

if ! command -v ansible-playbook >/dev/null 2>&1; then
  echo "ansible-playbook is not installed; skipping Kubernetes 1.17.5 component gates"
  exit 0
fi

snapshot_root="examples/ansible/k8s-1.17.5-cluster"
inventory="$snapshot_root/testdata/inventory.ini"
roles="$snapshot_root/roles"

find "$snapshot_root/components" -type f -name '*.yml' -print | xargs -I '{}' -P 4 sh -c '
  playbook=$1
  roles=$2
  inventory=$3
  ANSIBLE_ROLES_PATH="$roles" ansible-playbook -i "$inventory" --syntax-check "$playbook" >/dev/null
  ANSIBLE_ROLES_PATH="$roles" ansible-playbook -i "$inventory" --list-tasks "$playbook" >/dev/null
' sh '{}' "$roles" "$inventory"

if ANSIBLE_ROLES_PATH="$roles" ansible-playbook -i "$inventory" \
  "$snapshot_root/components/docker.yml" \
  -e K8S_VERSION=v1.17.5 \
  -e K8S1175_DOCKER_VERSION=18.09.7 \
  -e K8S1175_ARTIFACTS_VERIFIED=false \
  -e K8S1175_DOCKER_RUNTIME_VERIFIED=false >/dev/null 2>&1; then
  echo "Docker component did not fail closed without verification inputs" >&2
  exit 1
fi

if rg -n "recovery|housekeeping|uninstall|component-(amc|metrics|node-exporter)" \
  "$snapshot_root/components/host-preflight.yml" \
  "$snapshot_root/components/cluster-pki.yml" \
  "$snapshot_root/components/etcd.yml" \
  "$snapshot_root/components/kubernetes-distribution.yml" \
  "$snapshot_root/components/kube-apiserver.yml" \
  "$snapshot_root/components/kubelet.yml" >/dev/null; then
  echo "Core entrypoints reference recovery, housekeeping, uninstall, or optional capabilities" >&2
  exit 1
fi

echo "Kubernetes 1.17.5 component syntax, task listing, and fail-closed gates passed"
