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
string, so `504h` rather than `21d`, and it must be positive. The API server rejects `0s`, negative
values, and anything it cannot parse.

Reaching `maxAge` marks the NodeClaim as drifted. Karpenter decides when it is actually replaced,
which is why `maxAge` is not a guaranteed maximum node lifetime. Keep `expireAfter` for that, and
see [Sizing maxAge]({{ site.baseurl }}/tasks/sizing/).

### maxConcurrent

Optional. Limits the number of NodeClaims this policy can have drifted at one time, either as a
count (`"3"`) or as a percentage of the NodeClaims its selector matches (`"10%"`). If undefined,
k-belt marks every stale NodeClaim at once, and the NodePool's disruption budgets alone pace the
rollout.

If the value is a percentage, k-belt calculates the number it may mark as
`allowed = roundup(matched * percentage) - already_drifted`. If the value is a count, k-belt uses
that number as a static ceiling, `max_concurrent - already_drifted`. Percentages are calculated
from every NodeClaim the selector matches, not only the stale ones. For instance, a policy matching
6 NodeClaims with `maxConcurrent: "10%"` may have 1 NodeClaim drifted at a time, rounding up from
`6 * .1 = 0.6`.

A NodeClaim counts against the limit from the time k-belt marks it until the NodeClaim is removed,
including while Karpenter drains it. Once the limit is reached, k-belt re-checks the remaining
stale NodeClaims every 30 seconds and marks the oldest of them as slots free up. Karpenter
otherwise replaces drifted nodes in the order it observed the drift, so a limit is also how you get
the oldest nodes replaced first.

```yaml
spec:
  maxAge: 504h
  maxConcurrent: "10%"
```

Use `maxConcurrent` with [`taintDriftedNodes`](#taintdriftednodes) to limit how many nodes carry
the taint at one time.

{: .note }
> k-belt counts drifted NodeClaims from its cache, so if two NodeClaims are marked at nearly the
> same time a policy can briefly exceed its limit. Karpenter's disruption budgets still apply.

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

Use this with [`maxConcurrent`](#maxconcurrent), which limits how many NodeClaims the policy marks
at one time and therefore how many nodes are tainted. With `maxConcurrent: "3"`, at most three
nodes are tainted, however many are stale.

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
