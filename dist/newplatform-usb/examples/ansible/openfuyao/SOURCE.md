# OpenFuyao Ansible snapshot

This directory is a read-only demo snapshot of an existing cluster build job.
It is intentionally not wired to a production inventory or credentials.

- Source repository: `/Users/qyw/Downloads/paasinstallationserver`
- Source path: `paas_installation_server/ansible/project/openfuyao`
- Source Git commit: `6909da3eb238b76989403f033941158ab35fdf07`
- Snapshot time: `2026-08-11T00:10:21+08:00`
- Original files: `105`
- Original file bytes: `383055`
- Snapshot additions: `SOURCE.md`, seven executable `component-bke-*.platform.yml`
  adapters, a shared contract task, and static contract-test fixtures
- Original tree manifest SHA-256: `c928b9e5c8b6dd40b0efcf3c998d604ebb65c009bb69bb8097ef2d5d6584898a`

The manifest digest is computed over the sorted output of
`shasum -a 256` for every original regular file, using paths relative to this
directory. `SOURCE.md` and all platform-owned `component-bke-*` integration
files are excluded.

## Safety scan

Before copying, all 105 source files were inspected for inventory/host files,
private-key headers, SSH passwords, literal password/token/secret assignments,
fixed network endpoints, symlinks, executable payloads, and high-entropy token
candidates.

The source contains no inventory file, private key, high-entropy credential, or
hard-coded SSH credential. Credential-related matches are variable references,
empty template fields, Kubernetes Secret resource definitions, and helper code
that reads credentials supplied at runtime. Fixed addresses are loopback,
well-known service addresses, or examples. No source file was excluded.

The external `group_vars/bke_all` file is deliberately **not** part of this
snapshot because it belongs to a concrete deployment. Use the sibling
`environment-parameters.example.yml` template and resolve secret references at
runtime.

The platform-owned adapters and contract-test files are not part of the
immutable 105-file source snapshot or its digest. The install adapters invoke
only the Roles needed by a component node. `component-bke-nodes.platform.yml`
keeps the original `bke-common` then `bke-nodes` order, while
`component-bke-master-verify.platform.yml` only reads the target BKECluster.

Every adapter imports `component-bke-contract.tasks.yml`. It checks required
variable names, types, `cluster_role`, strategy, and `target_host_group` before
any Role executes. The master adapter also recreates the registry facts that
were previously inherited from another Playbook and bridges Inventory
`ansible_user` to the legacy `ansible_ssh_user` variable.

The platform Seed exposes three independent DAGs: management-cluster build,
work-cluster control-plane build, and work-node enrollment. The enrollment DAG
must pass the read-only master verification before running the node adapter.

## Execution warning

`build_manager_cluster.yml` invokes roles whose normal build tags include
recovery (`rcv`) operations. Treat this sample as destructive and require an
environment-owner approval. It also requires private artifacts, registries,
BKE tooling, and suitable target hosts; the demo only performs preflight and
job-shape validation unless those external dependencies are explicitly supplied.
