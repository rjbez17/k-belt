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
helm-lint: ## Lint and render the Helm chart.
	docker run --rm -v "$(PWD)/charts:/charts" alpine/helm:3.19.0 lint /charts/k-belt
	docker run --rm -v "$(PWD)/charts:/charts" alpine/helm:3.19.0 template k-belt /charts/k-belt > /dev/null

.PHONY: test-e2e
test-e2e: ## Run the kind + KWOK end-to-end tests (KEEP=1 keeps the cluster).
	./hack/e2e/run.sh

.PHONY: shell
shell: ## Open a shell in the dev container.
	$(COMPOSE) run --rm dev bash

.PHONY: clean
clean: ## Remove build output, containers and cached Go volumes.
	$(COMPOSE) down --volumes --remove-orphans
	-./hack/e2e/down.sh
	rm -rf bin cover.out

endif
