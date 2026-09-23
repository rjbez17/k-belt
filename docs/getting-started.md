---
title: Getting Started
nav_order: 2
---

# Getting Started
{: .no_toc }

1. TOC
{:toc}

## Requirements

* A Kubernetes cluster running Karpenter 1.x, with at least one NodePool provisioning nodes
* `kubectl` and `helm` 3.8 or later
* Permission to install a CRD and a ClusterRole

k-belt reads Karpenter's NodeClaims and NodePools and writes annotations to NodeClaims. It never
creates or deletes nodes itself.

## Install

```bash
helm install k-belt oci://ghcr.io/rjbez17/charts/k-belt \
  --namespace k-belt-system --create-namespace
```

Confirm the controller is up:

```bash
kubectl -n k-belt-system get pods
```

```text
NAME                      READY   STATUS    RESTARTS   AGE
k-belt-7d9f6c9b4c-7tqsn   1/1     Running   0          32s
```

## Rotate your first nodes

Start with a `maxAge` slightly below the age of your oldest nodes, so the first rollout is small.
Check how old they are:

```bash
kubectl get nodeclaims -L karpenter.sh/nodepool --sort-by=.metadata.creationTimestamp
```

Then create a policy for one NodePool:

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

`maxAge` is a Go duration, so there is no `d` unit: use `504h` for 21 days.

## Watch what happens

```bash
kubectl get bestbefores
```

```text
NAME           MAX AGE   MATCHED   STALE   DRIFTED   AGE
default-pool   504h      42        6       6         2m11s
```

`STALE` counts NodeClaims past `maxAge`. `DRIFTED` counts the ones k-belt has marked and Karpenter
hasn't replaced yet. The two numbers should track each other. A `STALE` count that stays well above
`DRIFTED` means something is stopping the rotation; see [Troubleshooting]({{ site.baseurl }}/troubleshooting/).

Karpenter reports the drift it sees on the NodeClaim:

```bash
kubectl get nodeclaim default-8vbh7 -o jsonpath='{.status.conditions[?(@.type=="Drifted")]}'
```

```json
{"type":"Drifted","status":"True","reason":"NodePoolDrifted","message":"NodePoolDrifted"}
```

From here Karpenter owns the replacement: it launches a new node, waits for it to be ready, drains
the old one, then terminates it. Disruption budgets decide how many of those happen at once.

## Set the pace

Rotation speed is Karpenter's `Drifted` disruption budget, not a k-belt setting. To roll one node at
a time:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: default
spec:
  disruption:
    budgets:
      - nodes: "1"
        reasons: [Drifted]
```

The default budget is `10%` of the NodePool, for all disruption reasons.

## Uninstall

```bash
helm uninstall k-belt --namespace k-belt-system
```

Uninstalling stops k-belt from marking new nodes, but it does not un-drift the nodes it already
marked: Karpenter will still replace them. To undo those first, delete your BestBefore policies and
wait for the `DRIFTED` count to reach zero.
