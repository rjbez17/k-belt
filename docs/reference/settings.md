---
title: Settings
parent: Reference
nav_order: 3
---

# Settings

## Controller flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--bestbefore-resync-period` | `5m` | How often every NodeClaim is re-evaluated, on top of its exact `maxAge` deadline. Minimum `10s`. |
| `--metrics-bind-address` | `:8443` | Metrics endpoint. `0` disables it. |
| `--metrics-secure` | `true` | Require authn/authz on the metrics endpoint. |
| `--health-probe-bind-address` | `:8081` | Health and readiness probes. |
| `--leader-elect` | `false` | Leader election. The chart sets it. |

The resync period bounds how late a rotation can start if a deadline is missed; it does not make
k-belt poll the API server, because reconciles read from the controller's cache. At 2,000 NodeClaims
the default works out at roughly seven cached reads a second. Each NodeClaim's resync is jittered by
±10% so they don't all land together after a restart.

## Chart values

The ones most installs touch. Everything else is in
[values.yaml](https://github.com/rjbez17/k-belt/blob/main/charts/k-belt/values.yaml).

| Value | Default | Meaning |
| --- | --- | --- |
| `image.tag` | chart `appVersion` | Controller image tag |
| `replicaCount` | `1` | Extra replicas stand by via leader election |
| `controller.resyncPeriod` | `5m` | Sets `--bestbefore-resync-period` |
| `metrics.enabled` | `true` | Serve metrics and create the Service |
| `metrics.secure` | `true` | Protect the metrics endpoint |
| `metrics.serviceMonitor.enabled` | `false` | Create a Prometheus Operator ServiceMonitor |
| `resources` | 10m/64Mi requests | Controller pod resources |
| `priorityClassName` | `""` | Worth setting so k-belt isn't evicted by the rotations it starts |

## Upgrading

Helm does not upgrade CRDs. After `helm upgrade`, apply the CRD yourself:

```bash
kubectl apply -f https://raw.githubusercontent.com/rjbez17/k-belt/main/charts/k-belt/crds/bestbefore.k-belt.io_bestbefores.yaml
```

## Permissions

k-belt runs with a ClusterRole that can `get`, `list`, `watch` and `patch` NodeClaims and Nodes,
read NodePools and BestBefores, write its own status, and create events. It cannot create or delete
nodes: every replacement is Karpenter's doing.
