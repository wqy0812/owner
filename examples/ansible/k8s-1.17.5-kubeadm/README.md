# Kubernetes 1.17.5 kubeadm sample

This is a fresh, self-contained sample for the manual platform workflow. It
does not import or depend on any of the repository's seeded demo jobs.

Inventory groups:

- `k8s_cluster`: every Kubernetes node
- `primary_control_plane`: exactly one first control-plane node
- `secondary_control_plane`: the remaining control-plane nodes
- `worker_nodes`: worker nodes

The four frontend components should point at these playbooks in order:

1. `preflight.yml` (`preflight`, `k8s_cluster`)
2. `prepare.yml` (`install`, `k8s_cluster`)
3. `bootstrap.yml` (`install`, `k8s_cluster`)
4. `verify.yml` (`verify`, `primary_control_plane`)

Required environment parameters are `media_archive_url` and
`media_archive_sha256`. The archive is published separately on the FSS node
and must contain `bin/kubeadm`, `bin/kubelet`, `bin/kubectl`, `cni/*`, and
`images/k8s-1.17.5-images.tar`.

Optional parameters have LAN defaults: `control_plane_endpoint`,
`pod_network_cidr`, `service_cidr`, `image_repository`, and `deploy_registry`.
