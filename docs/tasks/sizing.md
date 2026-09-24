---
title: Sizing maxAge
parent: Tasks
nav_order: 1
---

# Sizing maxAge
{: .no_toc }

1. TOC
{:toc}

## Leave room for the rollout

Keep `expireAfter` on the NodePool. It is the hard limit on node age, and BestBefore does not
replace it. What BestBefore needs is enough room underneath it to finish a rotation:

```text
expireAfter - maxAge  >  rollout time

rollout time  ≈  nodes in the pool
                 ÷ nodes the Drifted budget allows at once
                 × time to replace one node
```

Replacing one node means launching it, waiting for it to register and initialize, draining the old
one and terminating it. Usually a few minutes; longer if your pods are slow to drain.

| Pool | Drifted budget | Per node | Rollout | Headroom to leave |
| --- | --- | --- | --- | --- |
| 50 nodes | `10%` (5 at a time) | 10m | ~1h 40m | a few hours |
| 500 nodes | `10%` (50 at a time) | 10m | ~1h 40m | a few hours |
| 500 nodes | `1` | 10m | ~83h | 4 days or more |

Any node still waiting when `expireAfter` arrives is force-deleted, which is the burst you installed
BestBefore to avoid. Budgets that aren't scoped to `Drifted` also count other disruptions, so leave
extra margin.

Two settings that do nothing useful:

* `maxAge` greater than or equal to `expireAfter`: expiration always gets there first.
* `maxAge` shorter than a rollout: replacement nodes are already too old when they arrive, and the
  pool rotates forever. We have watched a test cluster do exactly this with `maxAge: 60s`.

## Capping how much rotates at once

Karpenter's `Drifted` budget already limits how many nodes it replaces at a time, and for most
clusters that is the only throttle you need. `maxConcurrent` limits how many NodeClaims k-belt
marks in the first place:

```yaml
spec:
  maxAge: 504h
  maxConcurrent: "10%"
```

Reach for it when:

* **The budget is shared.** A budget without `reasons` counts every kind of disruption, so a large
  rotation crowds out consolidation. Capping the rotation leaves the budget room for other work.
* **You want the oldest nodes replaced first.** Karpenter orders drifted nodes by when it noticed
  them. Under a cap, k-belt only marks the oldest stale NodeClaims, so age decides the order.
* **You are phasing a policy in** and want a handful of nodes to move before the rest.
* **You want `taintDriftedNodes` on.** The taint stops evicted pods landing on nodes that are next
  to go, and the cap stops it covering the whole pool. The two are much more useful together than
  either is alone.

The rollout is then paced by whichever is tighter. A cap below the budget slows the rollout without
changing how Karpenter replaces each node, so remember to fold it into the headroom arithmetic
above: `maxConcurrent: "1"` on a 500-node pool is the 83-hour row, whatever the budget says.

## Rolling it out to an existing cluster

On a cluster that has been running for months, a sensible `maxAge` makes most of the fleet stale at
once. Karpenter's budgets will pace the work, but the order is by when each node was marked, not by
age, so the oldest nodes are not necessarily replaced first.

Phase it in instead:

1. Create the policy with a `maxAge` just under the age of your oldest nodes:

   ```bash
   kubectl get nodeclaims --sort-by=.metadata.creationTimestamp | tail -5
   ```

2. Check the blast radius before anything moves:

   ```bash
   kubectl get bestbefores
   ```

   ```text
   NAME           MAX AGE   MATCHED   STALE   DRIFTED   AGE
   default-pool   1400h     312       4       4         18s
   ```

3. Let that rollout finish — `DRIFTED` back to `0` — then lower `maxAge` a step and repeat until you
   reach the target.

Each pass resets the creation times of the nodes it replaces, which spreads future expiry out. After
one full rotation the fleet stops arriving at `maxAge` in a single wave.

## One policy or several

A policy per NodePool is the easy default: the selector is
`matchLabels: {karpenter.sh/nodepool: <name>}` and the `maxAge` can follow how disposable that pool
is. Selectors are ordinary label selectors, so you can also target a subset:

```yaml
spec:
  nodeClaimSelector:
    matchExpressions:
      - key: karpenter.sh/capacity-type
        operator: In
        values: ["spot"]
  maxAge: 168h
```

An empty selector (`{}`) matches every NodeClaim in the cluster, including pools you did not intend
to rotate. Be explicit unless that is genuinely what you want.
