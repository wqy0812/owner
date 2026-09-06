SHELL := /bin/sh

GO ?= go
PNPM ?= pnpm
APP := bin/newplatform
BACKUP_APP := bin/clusterforge-backup
JOB_APP := bin/clusterforge-job
EMBED_DIR := internal/ui/dist

.PHONY: bootstrap dev dev-api dev-web seed reset-demo test check-docs test-deploy-script test-fixture-boundary test-ansible test-role-job test-reference-playbooks test-e2e test-e2e-live build build-web

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

test:
	$(MAKE) test-deploy-script
	$(MAKE) test-fixture-boundary
	$(GO) test ./...
	$(PNPM) --dir web test
	$(MAKE) test-ansible

test-deploy-script:
	./scripts/test-deploy-test-88-55.sh

test-fixture-boundary:
	./scripts/check-test-fixture-boundary.sh

test-role-job:
	@test -n "$(ANSIBLE_PLAYBOOK)" || { echo "Set ANSIBLE_PLAYBOOK to the required Ansible executable"; exit 1; }
	$(GO) build -o $(JOB_APP) ./cmd/clusterforge-job
	CLUSTERFORGE_JOB_CLI="$(CURDIR)/$(JOB_APP)" CLUSTERFORGE_JOB_TEST_ANSIBLE="$(ANSIBLE_PLAYBOOK)" $(GO) test ./internal/ansible ./internal/service ./internal/jobcli -run 'TestNativeJobReal|TestRoleJobRealAnsible|TestRoleJobAcceptance|TestScenarioBusinessAcceptanceRealAnsible|TestScenarioAcceptanceCredentialIsolationRealAnsible|TestScenarioRoleJobContinuationRealAnsible|TestStandaloneReal|TestYAMLTwoStepRollbackRealAnsible|TestNativeRollbackResumeAfterProviderRemoval|TestExecutorHealthRealRuntimePlugins' -count=1 -v

test-ansible: test-role-job

# Retained source examples are separate from the native Role execution gate.
test-reference-playbooks:
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
