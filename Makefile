SHELL := /bin/sh

GO ?= go
PNPM ?= pnpm
APP := bin/newplatform
EMBED_DIR := internal/ui/dist

.PHONY: bootstrap dev dev-api dev-web seed reset-demo test test-ansible test-e2e build build-web

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
	$(GO) test ./...
	$(PNPM) --dir web test
	$(MAKE) test-ansible

test-ansible:
	NEWPLATFORM_ANSIBLE_INTEGRATION=1 $(GO) test ./internal/ansible -run TestRunnerWithTemporaryLocalPlaybook -count=1 -v
	./scripts/test-k8s1175-components.sh
	./scripts/test-openfuyao-components.sh

test-e2e:
	$(PNPM) --dir web test:e2e

build-web:
	$(PNPM) --dir web build
	rm -rf $(EMBED_DIR)
	mkdir -p $(EMBED_DIR)
	cp -R web/dist/. $(EMBED_DIR)/

build: build-web
	mkdir -p bin
	$(GO) build -tags embed -o $(APP) ./cmd/server
