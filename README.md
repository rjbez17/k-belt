# k-belt

A toolbelt of controllers for clusters running [Karpenter](https://karpenter.sh).

Documentation: **[k-belt.io](https://k-belt.io)**

## BestBefore

Karpenter's `expireAfter` deletes a node the moment it is too old, ignoring the NodePool's
disruption budgets and without pre-spinning a replacement. On a cluster where hundreds of nodes were
created in the same few minutes, they expire in the same few minutes too.

`BestBefore` rotates those nodes through Karpenter's drift instead, which budgets rate limit and
which launches replacements first:

```yaml
apiVersion: bestbefore.k-belt.io/v1alpha1
kind: BestBefore
metadata:
  name: default-pool
spec:
  nodeClaimSelector:
    matchLabels:
      karpenter.sh/nodepool: default
  maxAge: 504h   # 21 days
```

Keep `expireAfter` as the hard backstop, and leave enough room under it for a rollout to finish.

```sh
helm install k-belt oci://ghcr.io/rjbez17/charts/k-belt \
  --namespace k-belt-system --create-namespace
```

See [Getting Started](https://k-belt.io/getting-started/) for the rest, and
[Sizing maxAge](https://k-belt.io/tasks/sizing/) before pointing one at a production cluster.

## Development

Everything runs in Docker; the Makefile forwards to the `dev` compose service.

```sh
make test        # codegen, vet, envtest suites (incl. Karpenter contract tests)
make lint        # golangci-lint
make helm-lint   # lint and render the Helm chart
make build       # controller image k-belt:dev
make test-e2e    # kind + KWOK + Karpenter + k-belt, end to end
make clean
```

`make test-e2e` builds Karpenter's KWOK provider from a checkout at `../karpenter` (override with
`KARPENTER_SRC`), since it has no published image. `KEEP=1` leaves the cluster running.

Kubebuilder's original targets live in `hack/container.mk`. The chart's CRD and ClusterRole rules
are generated from `config/` by `hack/sync-chart.sh`, which `make manifests` runs; CI fails if they
drift.

More in [Contributing](https://k-belt.io/contributing/).

## License

Apache 2.0. See [LICENSE](LICENSE).
