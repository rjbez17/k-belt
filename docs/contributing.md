---
title: Contributing
nav_order: 7
---

# Contributing
{: .no_toc }

1. TOC
{:toc}

## Development environment

Everything runs in Docker. You need Docker and `make`; the toolchain, linters and test binaries all
live in the dev container.

```bash
make test        # codegen, vet and the envtest suites
make lint        # golangci-lint
make build       # controller image
make helm-lint   # lint and render the chart
make test-e2e    # kind + KWOK + Karpenter + k-belt, end to end
```

`make test-e2e` needs a Karpenter checkout for its KWOK provider, which has no published image. It
looks for `../karpenter` and takes `KARPENTER_SRC` to point somewhere else. `KEEP=1` leaves the
cluster up afterwards.

## Layout

| Path | What's in it |
| --- | --- |
| `api/v1alpha1/` | The BestBefore types, and `labels.go` with every annotation and taint key |
| `internal/controller/` | The two controllers: one owns NodeClaims, one maintains status |
| `charts/k-belt/` | Helm chart, the only way k-belt is packaged; its CRD and RBAC rules are generated |
| `hack/e2e/` | The kind + KWOK end-to-end scripts |
| `docs/` | This site |

Run `make manifests` after touching kubebuilder markers or API types. It writes the CRD and the
chart's RBAC rules, and CI fails if you forget.

## Tests

Three layers, and most changes touch two of them:

* **Envtest suites** in `internal/controller` run against a real API server with Karpenter's CRDs
  loaded. Most behaviour lives here.
* **Contract tests** in `karpenter_contract_test.go` run Karpenter's own drift and hash controllers
  against the NodeClaims k-belt modifies. They exist so that a Karpenter upgrade that changes the
  mechanism fails `make test` instead of failing quietly in someone's cluster. Keep them passing, or
  change them deliberately.
* **End-to-end** in `hack/e2e` runs the real thing on kind and asserts what envtest cannot: that a
  rollout stays inside the NodePool's disruption budget.

## Karpenter internals

k-belt depends on how Karpenter computes and compares NodePool hashes. That dependency is
deliberately concentrated: the keys are in `api/v1alpha1/labels.go`, the logic is in
`internal/controller/nodeclaim_controller.go`, and the contract tests pin the behaviour. If you are
adding something that reaches into Karpenter, keep it in those places and add a contract test.

## Pull requests

* Conventional commit messages (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`).
* Generated code and the chart land in their own commits, so the interesting diff stays readable.
* `make test lint helm-lint` before pushing; `make test-e2e` if you touched the controllers.
