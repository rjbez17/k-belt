---
title: Disruption
parent: Concepts
nav_order: 2
---

# Disruption
{: .no_toc }

1. TOC
{:toc}

Everything after a node is marked belongs to Karpenter. This page covers what that means in
practice, because it decides how fast a rollout goes and when it stops.

## What Karpenter does with a drifted node

For each drifted node, Karpenter checks the NodePool's disruption budget, simulates where the node's
pods would go, launches any replacement capacity it needs, waits for it to be ready, then taints and
drains the old node. Empty nodes go first, and the oldest drift goes before the newest.

Because replacements are pre-spun, a rotation costs extra capacity while it runs, up to your
budget's worth of nodes on top of what you normally run.

## Budgets set the pace

Budgets are a NodePool field, and they are the only throttle:

```yaml
spec:
  disruption:
    budgets:
      - nodes: "10%"
        reasons: [Underutilized, Empty]
      - nodes: "5%"
        reasons: [Drifted]
      - nodes: "0"              # freeze rotations during business hours (UTC)
        reasons: [Drifted]
        schedule: "0 8 * * mon-fri"
        duration: 10h
```

Budgets catch people out in two ways:

* A budget without `reasons` applies to every reason, and the nodes it counts include ones being
  disrupted for any reason. A large rotation can therefore hold up consolidation.
* Karpenter acts on one disruption method per pass, and drift runs before consolidation. While a
  backlog of drifted nodes has budget to work with, consolidation rarely gets a turn, so cluster
  cost can drift up during a long rollout.

## When a rotation stalls

Marking a node does not guarantee it will be replaced. Karpenter skips a drifted node that:

* has `karpenter.sh/do-not-disrupt: "true"` on the Node
* isn't initialized yet, or was just nominated for a pending pod
* runs a pod with `karpenter.sh/do-not-disrupt`, or a pod whose PodDisruptionBudget allows no
  evictions
* runs pods its scheduling simulation cannot place anywhere else
* belongs to a NodePool whose budget is currently zero

Karpenter emits a `DisruptionBlocked` event on the node with the reason. k-belt keeps the node
marked, so it will be replaced as soon as whatever is blocking it clears. See
[Monitoring]({{ site.baseurl }}/tasks/monitoring/) for alerting on this.

## terminationGracePeriod changes the contract

Karpenter treats drift as *eventual* disruption. Whether a blocking pod can hold it up depends on
the NodePool's `terminationGracePeriod`:

| `terminationGracePeriod` | Drifted node with a blocking PDB or `do-not-disrupt` pod |
| --- | --- |
| Not set | Never disrupted. It waits until `expireAfter` force-deletes it. |
| Set | Disrupted within the budget once its replacement is ready, drained honouring PDBs, then forcibly once the grace period runs out. |

Set it if you want rotations to finish on a schedule. Leave it unset if a pod that opts out of
disruption must always win, and accept that `expireAfter` becomes the only limit on node age.

## Pods can move twice

While a rotation runs, other nodes that are also too old are still valid destinations for evicted
pods. Karpenter's simulation and the kube-scheduler both treat them as ordinary nodes, so a pod can
be moved onto a node that is itself replaced a few minutes later.

Karpenter's own drift behaves the same way after an AMI or NodeClass change. A larger budget
shortens the window.

If this matters for your workloads, set `taintDriftedNodes` together with `maxConcurrent`. The
taint stops the scheduler placing pods on nodes k-belt has marked, and `maxConcurrent` limits how
many nodes are marked, and therefore tainted, at one time. Without a limit every stale node in the
pool is tainted at once, and Karpenter's scheduling simulation treats a `PreferNoSchedule` taint on
an existing node as a hard constraint, so it will provision capacity for pods that would otherwise
have fit on those nodes. For example:

```yaml
spec:
  maxAge: 504h
  maxConcurrent: "3"
  taintDriftedNodes: true
```

See [taintDriftedNodes]({{ site.baseurl }}/reference/api/#taintdriftednodes) for the full caveat.
