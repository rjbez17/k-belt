---
title: Troubleshooting
nav_order: 6
---

# Troubleshooting
{: .no_toc }

1. TOC
{:toc}

## Nodes are stale but nothing is marked

`STALE` is climbing and `DRIFTED` stays at zero.

Check the policy is healthy and the selector matches what you think:

```bash
kubectl get bestbefore default-pool -o yaml | yq '.status'
kubectl get nodeclaims -l karpenter.sh/nodepool=default
```

If `Ready` is `False` with `InvalidSelector`, the selector parses on the API server but not in
k-belt — usually an operator like `In` with no values. Fix the selector.

If the counts look right, look at the NodeClaims themselves. k-belt skips any NodeClaim without both
`karpenter.sh/nodepool-hash` and `karpenter.sh/nodepool-hash-version`, because Karpenter's drift
check ignores those too:

```bash
kubectl get nodeclaim <name> -o jsonpath='{.metadata.annotations}'
```

NodeClaims carrying `bestbefore.k-belt.io/paused` are skipped, which is the point of that
annotation.

## Nodes are marked but never replaced

`DRIFTED` sits at the same number for hours. k-belt has done its part, so the answer is on
Karpenter's side:

```bash
kubectl describe node <node> | grep -A5 Events
```

`DisruptionBlocked` gives the reason. The common ones are a PodDisruptionBudget that allows no
evictions, a pod with `karpenter.sh/do-not-disrupt`, pods that cannot be scheduled anywhere else, and
a `Drifted` budget of zero. [Disruption]({{ site.baseurl }}/concepts/disruption/) lists the full set.

Worth checking your own budget first, since it is the easiest to get wrong:

```bash
kubectl get nodepool default -o jsonpath='{.spec.disruption.budgets}'
```

## The cluster is rotating nodes constantly

Every replacement node is marked again shortly after it arrives. That means `maxAge` is shorter than
the time a full rotation takes, so the pool never gets ahead of itself. Raise `maxAge` or raise the
budget; [Sizing maxAge]({{ site.baseurl }}/tasks/sizing/) has the arithmetic.

## Nodes were expired instead of rotated

Karpenter deleted nodes in a burst even though BestBefore was running. Compare `expireAfter` with
`maxAge`:

```bash
kubectl get nodepool default -o jsonpath='{.spec.template.spec.expireAfter}'
```

If the gap between them is smaller than a full rollout, nodes reach `expireAfter` while they are
still queued behind the budget, and expiration deletes them regardless. Widen the gap or speed the
rollout up.

## A node I reverted went away anyway

Reverting only helps before Karpenter commits. If the NodeClaim already had the `DisruptionReason`
condition, or the Node already had the `karpenter.sh/disrupted` taint, the replacement was already
under way.

## Everything stopped after a Karpenter upgrade

Karpenter occasionally changes how it hashes NodePools, and bumps `karpenter.sh/nodepool-hash-version`
with it. k-belt handles the migration in both directions, but only while it is running. If k-belt
was down during the upgrade, it catches up on start, so check the controller is healthy:

```bash
kubectl -n k-belt-system logs deploy/k-belt --tail=50
```

If rotations are still not happening after that, the contract k-belt relies on may genuinely have
changed. Please [open an issue](https://github.com/rjbez17/k-belt/issues) with your Karpenter
version.
