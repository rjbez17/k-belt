# Agent Development Guide

A file for [guiding coding agents](https://agents.md/).

## Commands

Everything runs in Docker; the top-level Makefile forwards each target into the `dev` compose
service, and kubebuilder's own targets live in `hack/container.mk`.

- **Test:** `make test` runs codegen, `go vet`, then the envtest suites. Target one package with
  `docker compose run --rm dev go test ./internal/controller/ -run TestControllers`, or focus a
  spec with `-ginkgo.focus="<text>"`.
- **Lint:** `make lint` (golangci-lint with the logcheck plugin).
- **Codegen:** `make manifests generate` writes `api/v1alpha1/zz_generated.deepcopy.go`, the
  chart's CRD under `charts/k-belt/crds/`, and `charts/k-belt/rbac-rules.yaml`. CI fails if the
  committed copies drift, so run it after touching API types or `+kubebuilder` markers.
- **Chart:** `make helm-lint` lints and renders it.
- **End to end:** `make test-e2e` runs kind + KWOK + Karpenter + k-belt, about five minutes.
  `KEEP=1` leaves the cluster up. Needs a Karpenter checkout at `../karpenter` (`KARPENTER_SRC`).

## Layout

- API types and every annotation, taint and hash key: `api/v1alpha1/`
- Controllers: `internal/controller/`, `nodeclaim_controller.go` owns all NodeClaim changes,
  `bestbefore_controller.go` only maintains status
- Helm chart, the only packaging: `charts/k-belt/`
- End-to-end scripts: `hack/e2e/`
- Documentation site (GitHub Pages): `docs/`

There is no kustomize `config/` directory. The chart is generated from the markers; don't hand-edit
`charts/k-belt/crds/` or `charts/k-belt/rbac-rules.yaml`.

## Karpenter coupling

BestBefore works by rewriting a NodeClaim's `karpenter.sh/nodepool-hash` so Karpenter's own drift
check replaces the node. That dependency is deliberately concentrated:

- Keys live in `api/v1alpha1/labels.go`.
- The logic lives in `internal/controller/nodeclaim_controller.go`.
- `internal/controller/karpenter_contract_test.go` runs Karpenter's real drift and hash controllers
  against the NodeClaims k-belt modifies.

Keep new Karpenter-facing behaviour in those places, and add a contract test for it. If a Karpenter
upgrade breaks the mechanism, that suite should fail rather than a cluster.

## Conventions

- Logging and errors follow Karpenter's style: lowercase messages, Kind-cased structured keys
  (`"NodeClaim"`, `"BestBefore"`), `klog.KObj`/`klog.KRef` for object references.
- Every API group, annotation, label and taint uses the owning controller's subdomain of
  `k-belt.io`, e.g. `bestbefore.k-belt.io/paused`. Prometheus metrics use `kbelt_<controller>_`.
- Conventional commits. Generated code goes in its own commit.
