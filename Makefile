SHELL := /bin/sh

GO ?= go
PNPM ?= pnpm
APP := bin/newplatform
BACKUP_APP := bin/clusterforge-backup
EMBED_DIR := internal/ui/dist

.PHONY: bootstrap dev dev-api dev-web seed reset-demo test test-fixture-boundary test-ansible test-e2e test-e2e-live test-e2e-real-scenarios-preflight test-e2e-real-scenarios build build-web

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
	$(MAKE) test-fixture-boundary
	$(GO) test ./...
	$(PNPM) --dir web test
	$(MAKE) test-ansible

test-fixture-boundary:
	./scripts/check-test-fixture-boundary.sh

test-ansible:
	NEWPLATFORM_ANSIBLE_INTEGRATION=1 $(GO) test ./internal/ansible -run TestRunnerWithTemporaryLocalPlaybook -count=1 -v
	./scripts/test-k8s1175-components.sh
	./scripts/test-openfuyao-components.sh

test-e2e:
	$(PNPM) --dir web test:e2e

test-e2e-live:
	./scripts/test-live-api-e2e.sh

test-e2e-real-scenarios-preflight:
	CLUSTERFORGE_REAL_E2E=1 CLUSTERFORGE_REAL_E2E_PREFLIGHT_ONLY=1 $(GO) test ./automation/scenarios -run '^TestRealScenarioAutomation$$' -count=1 -v -timeout=10m

test-e2e-real-scenarios:
	CLUSTERFORGE_REAL_E2E=1 $(GO) test ./automation/scenarios -run '^TestRealScenarioAutomation$$' -count=1 -v -timeout=3h

build-web:
	$(PNPM) --dir web build
	rm -rf $(EMBED_DIR)
	mkdir -p $(EMBED_DIR)
	cp -R web/dist/. $(EMBED_DIR)/

build: build-web
	mkdir -p bin
	$(GO) build -tags embed -o $(APP) ./cmd/server
	$(GO) build -o $(BACKUP_APP) ./cmd/backup
