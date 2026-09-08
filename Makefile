SHELL := /bin/sh

GO ?= go
PNPM ?= pnpm
APP := bin/newplatform
BACKUP_APP := bin/clusterforge-backup
JOB_APP := bin/clusterforge-job
EMBED_DIR := internal/ui/dist
export ANSIBLE_PLAYBOOK

.PHONY: bootstrap dev dev-api dev-web seed reset-demo test test-fast test-local test-go test-evidence check-docs test-deploy-script test-deploy-local-docker deploy-local-docker test-fixture-boundary test-ansible test-role-job test-reference-playbooks test-historical-reference-playbooks test-e2e test-e2e-live build build-web
.PHONY: test-deploy test-deploy-runtime test-deploy-gate

check-docs:
	python3 scripts/check-docs.py

bootstrap:
	$(GO) mod download
	CI=true $(PNPM) --dir web install --frozen-lockfile

dev:
	$(MAKE) -j2 dev-api dev-web

dev-api:
	$(GO) run ./cmd/server

dev-web:
	$(PNPM) --dir web dev

seed:
	$(GO) run ./cmd/server --seed-only

reset-demo:
	$(GO) run ./cmd/server --reset-demo

test-fast:
	$(MAKE) test-deploy-script
	$(MAKE) test-fixture-boundary
	$(MAKE) test-evidence
	$(MAKE) test-go
	$(PNPM) --dir web test

# Shared host checks; the deployment entry also requires the isolated runtime gate.
test-local:
	$(MAKE) test-deploy-script test-fixture-boundary test-evidence check-docs
	$(MAKE) test-go
	$(PNPM) --dir web test:coverage
	$(MAKE) test-e2e-live
	git diff --check

test: test-deploy

# Both deployment scripts use this entry. It never uploads or activates a build.
test-deploy:
	python3 scripts/deployment-gate.py

# Internal container stage, also callable for focused runtime diagnosis.
test-deploy-runtime:
	$(MAKE) test-role-job
	$(MAKE) test-reference-playbooks
	$(ANSIBLE_PLAYBOOK) -i /etc/ansible/inventory.ini deploy/local-docker/smoke.yml

test-deploy-gate:
	python3 scripts/test-deployment-gate.py

test-go:
	$(GO) test ./cmd/... ./internal/...

test-evidence:
	node --test web/scripts/source-manifest.test.mjs

test-deploy-script:
	$(MAKE) test-deploy-gate
	./scripts/test-deploy-test-88-55.sh
	python3 scripts/test-deploy-activation.py
	$(MAKE) test-deploy-local-docker

test-deploy-local-docker:
	python3 scripts/test-deploy-local-docker.py

deploy-local-docker:
	./scripts/deploy-local-docker.sh

test-fixture-boundary:
	./scripts/check-test-fixture-boundary.sh

test-role-job:
	@test -n "$(ANSIBLE_PLAYBOOK)" || { echo "Set ANSIBLE_PLAYBOOK to the required Ansible executable"; exit 1; }
	$(GO) build -o $(JOB_APP) ./cmd/clusterforge-job
	CLUSTERFORGE_JOB_CLI="$(abspath $(JOB_APP))" GO="$(GO)" python3 scripts/test-role-job.py

test-ansible: test-role-job

# Current published-component reference; pinned runtime and offline behavior checks.
test-reference-playbooks:
	@test -n "$(ANSIBLE_PLAYBOOK)" || { echo "Set ANSIBLE_PLAYBOOK to the required Ansible executable"; exit 1; }
	$(GO) test ./internal/ansible -run TestPublishedReferenceRoleEntries -count=1 -v
	./scripts/test-published-reference.sh

# Historical SUSE/kubeadm/OpenFuyao snapshots retain their original checks.
# Their compatibility failures are independent of the current reference gate.
test-historical-reference-playbooks:
	@test -n "$(ANSIBLE_PLAYBOOK)" || { echo "Set ANSIBLE_PLAYBOOK to the required Ansible executable"; exit 1; }
	NEWPLATFORM_ANSIBLE_INTEGRATION=1 $(GO) test ./internal/ansible -run TestRunnerWithTemporaryLocalPlaybook -count=1 -v
	./scripts/test-k8s1175-components.sh
	./scripts/test-flannel-ownership.sh
	./scripts/test-kubeadm-component-ownership.sh
	./scripts/test-openfuyao-components.sh

test-e2e:
	$(PNPM) --dir web test:e2e

test-e2e-live:
	./scripts/test-live-api-e2e.sh

build-web:
	$(PNPM) --dir web build
	rm -rf $(EMBED_DIR)
	mkdir -p $(EMBED_DIR)
	cp -R web/dist/. $(EMBED_DIR)/

build: build-web
	mkdir -p bin
	$(GO) build -tags embed -o $(APP) ./cmd/server
	$(GO) build -o $(BACKUP_APP) ./cmd/backup
	$(GO) build -o $(JOB_APP) ./cmd/clusterforge-job
