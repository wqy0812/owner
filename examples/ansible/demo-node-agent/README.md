# demo-node-agent

These localhost-safe playbooks demonstrate a real component lifecycle without
pretending to be a Kubernetes upgrade. By default they only touch:

`/tmp/newplatform-demo-agent`

The lifecycle is:

1. `install-v1.0.yml`
2. `upgrade-v1.1.yml`
3. `verify.yml` with `expected_version=1.1.0`
4. `rollback-v1.0.yml`
5. `verify.yml` with `expected_version=1.0.0`
6. `cleanup.yml`

The upgrade stores a checkpoint in `.previous-version`; rollback requires and
consumes that checkpoint. Tests override `agent_root` with an isolated temporary
directory and always run cleanup.
