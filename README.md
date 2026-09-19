# k-belt

A toolbelt of controllers that fill operational gaps in [Karpenter](https://karpenter.sh).

## Install

```sh
helm install k-belt oci://ghcr.io/rjbez17/charts/k-belt \
  --namespace k-belt-system --create-namespace
```

Karpenter must already be installed: k-belt watches its NodeClaims and NodePools. Chart values are
documented in [charts/k-belt/values.yaml](charts/k-belt/values.yaml); the common ones are
`image.tag`, `controller.resyncPeriod`, `replicaCount` and `metrics.*`.

Helm never upgrades CRDs, so after `helm upgrade` apply the CRD yourself:

```sh
kubectl apply -f https://raw.githubusercontent.com/rjbez17/k-belt/main/charts/k-belt/crds/bestbefore.k-belt.sh_bestbefores.yaml
```

Images are published to `ghcr.io/rjbez17/k-belt` for linux/amd64 and linux/arm64: `vX.Y.Z` tags for
releases, plus `main` and `sha-<commit>` for builds off the default branch.

## BestBefore

Karpenter's `expireAfter` deletes a node as soon as it's too old, **ignoring the NodePool's
disruption budgets**. If many nodes were created at once, they all expire at once.

A `BestBefore` selects NodeClaims by label and sets a `maxAge`. When a NodeClaim is older than
`maxAge`, k-belt changes its `karpenter.sh/nodepool-hash` annotation. Karpenter then sees the
NodeClaim as `Drifted` (reason `NodePoolDrifted`) and replaces it the way it handles any drift:
within the NodePool's disruption budgets, with replacement capacity launched first.

```yaml
apiVersion: bestbefore.k-belt.sh/v1alpha1
kind: BestBefore
metadata:
  name: default-pool
spec:
  nodeClaimSelector:
    matchLabels:
      karpenter.sh/nodepool: default
  maxAge: 504h   # 21 days
```

If a policy stops applying (deleted, `maxAge` raised, selector changed), k-belt restores the
original hash on NodeClaims Karpenter hasn't started replacing yet. Individual NodeClaims can be
frozen with the `bestbefore.k-belt.sh/paused` annotation, or reverted and frozen with `bestbefore.k-belt.sh/revert`.

Keep a longer `expireAfter` on the NodePool as a hard backstop, with enough headroom for a full
rollout. Read **[docs/bestbefore.md](docs/bestbefore.md)** before using it in production: it covers
sizing, stalled drift, `terminationGracePeriod`, pod churn, budgets and first installs.

## Development

Everything runs in Docker via `docker compose`; the Makefile forwards to the `dev` container.

```sh
make test        # codegen, vet, envtest suites (incl. Karpenter contract tests)
make helm-lint   # lint and render the Helm chart
make lint        # golangci-lint
make build       # controller image k-belt:dev
make run         # run the image against ~/.kube/config
make clean
```

Kubebuilder's original targets (install, deploy, build-installer, ...) live in `hack/container.mk`.

### End-to-end tests

`make test-e2e` runs BestBefore against a real Karpenter in a throwaway kind cluster (about five
minutes). It creates the cluster, installs [KWOK](https://kwok.sigs.k8s.io) so fake nodes cost
nothing, builds and installs Karpenter's KWOK provider, installs k-belt **with the Helm chart**,
then asserts that a rollout
respects the NodePool's disruption budget, that deleting a policy restores the NodeClaims, and that
the paused and revert annotations are honoured. `KEEP=1 make test-e2e` leaves the cluster up.

Karpenter's KWOK provider has no published image, so it is built from the checkout at
`../karpenter` (override with `KARPENTER_SRC`). The scripts live in `hack/e2e/`.

The chart's CRD and ClusterRole rules are generated from `config/` by `hack/sync-chart.sh`, which
`make manifests` runs; CI fails if they drift.

## Getting Started

### Prerequisites
- go version v1.24.6+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/k-belt:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/k-belt:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/k-belt:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/k-belt/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v2-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

