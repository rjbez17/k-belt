---
title: API
parent: Reference
nav_order: 1
---

# API
{: .no_toc }

1. TOC
{:toc}

`BestBefore` is cluster scoped, in API group `bestbefore.k-belt.io/v1alpha1`.

```yaml
apiVersion: bestbefore.k-belt.io/v1alpha1
kind: BestBefore
metadata:
  name: default-pool
spec:
  nodeClaimSelector:
    matchLabels:
      karpenter.sh/nodepool: default
  maxAge: 504h
  maxConcurrent: "10%"
  taintDriftedNodes: false
```

## spec

### nodeClaimSelector

Required. A standard Kubernetes label selector, matched against NodeClaim labels. Karpenter labels
every NodeClaim with `karpenter.sh/nodepool`, `karpenter.sh/capacity-type`,
`node.kubernetes.io/instance-type` and the well-known topology labels, so those are usually what you
select on.

An empty selector (`{}`) matches every NodeClaim in the cluster.

### maxAge

Required. How long a NodeClaim may live, measured from its `creationTimestamp`. A Go duration
string, so `504h` rather than `21d`, and it must be positive — the API server rejects `0s`, negative
values and anything it cannot parse.

Reaching `maxAge` marks the NodeClaim as drifted. Karpenter decides when it is actually replaced,
which is why `maxAge` is not a guaranteed maximum node lifetime. Keep `expireAfter` for that, and
see [Sizing maxAge]({{ site.baseurl }}/tasks/sizing/).

### maxConcurrent

Optional. Caps how many of this policy's NodeClaims may be drifted and awaiting replacement at
once, as a count (`"3"`) or a percentage of the NodeClaims the selector matches (`"10%"`). Both
forms are strings.

Unset means no cap: every stale NodeClaim is marked at once and the NodePool's disruption budgets
alone pace the rollout. Setting it adds a second, policy-level throttle on top of those budgets —
whichever is tighter wins.

Percentages are of everything the selector matches, not just the stale ones, and round up, so a
small pool still makes progress: `10%` of 6 NodeClaims allows 1.

A slot is occupied from the moment k-belt marks a NodeClaim until that NodeClaim is gone, including
while Karpenter drains it. When slots are full, the remaining stale NodeClaims are re-checked every
30 seconds, and the **oldest** of them goes next — which is worth knowing, because without a cap
Karpenter replaces drifted nodes in the order it noticed them rather than by age.

```yaml
spec:
  maxAge: 504h
  maxConcurrent: "10%"
```

The count comes from the controller's cache, so a policy can briefly exceed its cap if two
NodeClaims are marked within the same moment. Karpenter's budgets still bound what that disrupts.

### taintDriftedNodes

Optional, defaults to `false`. Adds `bestbefore.k-belt.io/drifted:PreferNoSchedule` to the Node of
every NodeClaim this policy marks, so the scheduler prefers nodes that aren't about to be replaced.
The taint is removed if the drift is undone.

{: .warning }
> Karpenter's scheduling simulation treats a `PreferNoSchedule` taint on an existing node as a hard
> constraint, unless one of your NodePool templates also carries a `PreferNoSchedule` taint. With
> this on, Karpenter may launch capacity for pods that would have fitted on tainted nodes, and
> consolidate less while a rotation runs. Adding any `PreferNoSchedule` taint to the NodePool
> template makes Karpenter relax these back into preferences.

## status

| Field | Meaning |
| --- | --- |
| `matchedNodeClaims` | NodeClaims the selector matches |
| `staleNodeClaims` | Matched NodeClaims older than `maxAge` |
| `driftedNodeClaims` | NodeClaims this policy has marked that Karpenter hasn't replaced yet |
| `observedGeneration` | Spec generation the status was computed from |
| `conditions` | `Ready` is `False` with reason `InvalidSelector` when the selector cannot be parsed |

A policy with an unparseable selector matches nothing, so any nodes it had marked are restored until
you fix it.
