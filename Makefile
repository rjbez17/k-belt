# All toolchain targets run inside the `dev` container defined in docker-compose.yml.
# The kubebuilder-generated targets live in hack/container.mk.

ifdef IN_CONTAINER
include hack/container.mk
else

SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec
.DEFAULT_GOAL := help

COMPOSE ?= docker compose
IMG ?= k-belt:dev
RENDERED_CHART ?= /tmp/k-belt-rendered.yaml

# kind runs on the host, since it drives the host's Docker daemon.
KIND_VERSION ?= v0.30.0
KIND ?= $(PWD)/bin/kind
KIND_PLATFORM := $(shell uname -s | tr '[:upper:]' '[:lower:]')-$(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

# Forwarded to the dev container: make <target> == docker compose run dev make <target>
CONTAINER_TARGETS := manifests generate fmt vet test lint lint-fix lint-config karpenter-crds

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
	@printf "\n\033[1mRun in the dev container\033[0m\n  %s\n" "$(CONTAINER_TARGETS)"

##@ Development

.PHONY: $(CONTAINER_TARGETS)
$(CONTAINER_TARGETS):
	$(COMPOSE) run --rm -T -e IMG="$(IMG)" dev make $@

.PHONY: build
build: manifests generate fmt vet ## Build the controller image.
	$(COMPOSE) build app

.PHONY: run
run: build ## Run the controller against the cluster in ~/.kube/config (override with KUBECONFIG_DIR).
	$(COMPOSE) up app

.PHONY: helm-lint
helm-lint: ## Lint the Helm chart and validate what it renders against the Kubernetes schemas.
	docker run --rm -v "$(PWD)/charts:/charts" alpine/helm:3.19.0 lint /charts/k-belt
	docker run --rm -v "$(PWD)/charts:/charts" alpine/helm:3.19.0 template k-belt /charts/k-belt \
		--set metrics.serviceMonitor.enabled=true > "$(RENDERED_CHART)"
	docker run --rm -v "$(RENDERED_CHART):/rendered.yaml" ghcr.io/yannh/kubeconform:latest \
		-summary -strict -ignore-missing-schemas /rendered.yaml

.PHONY: lint-shell
lint-shell: ## Shellcheck the hack scripts.
	docker run --rm -v "$(PWD):/mnt" -w /mnt koalaman/shellcheck:stable -x -S warning \
		hack/sync-chart.sh hack/e2e/*.sh

.PHONY: lint-docs
lint-docs: ## Lint the documentation markdown.
	docker run --rm -v "$(PWD):/w" -w /w davidanson/markdownlint-cli2:latest

.PHONY: docs-build
docs-build: ## Build the documentation site the way GitHub Pages does.
	docker run --rm -v "$(PWD)/docs:/site" -w /site ruby:3.3 bash -c '\
		gem install jekyll jekyll-remote-theme jekyll-seo-tag jekyll-include-cache --no-document -q && \
		jekyll build -d /tmp/site'

$(KIND):
	@mkdir -p $(dir $(KIND))
	curl -fsSLo $(KIND) "https://kind.sigs.k8s.io/dl/$(KIND_VERSION)/kind-$(KIND_PLATFORM)"
	chmod +x $(KIND)

.PHONY: test-e2e
test-e2e: $(KIND) ## Run the kind + KWOK end-to-end tests (KEEP=1 keeps the cluster).
	KIND=$(KIND) ./hack/e2e/run.sh

.PHONY: shell
shell: ## Open a shell in the dev container.
	$(COMPOSE) run --rm dev bash

.PHONY: clean
clean: ## Remove build output, containers and cached Go volumes.
	$(COMPOSE) down --volumes --remove-orphans
	-./hack/e2e/down.sh
	rm -rf bin cover.out

endif
