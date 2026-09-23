---
title: Pausing and reverting nodes
parent: Tasks
nav_order: 2
---

# Pausing and reverting nodes

Sometimes one node should be left alone: it is running a long batch job, or it is the node you are
debugging. Two annotations on the NodeClaim control this. Only their presence matters, so any value
works.

```bash
# Leave this NodeClaim exactly as it is
kubectl annotate nodeclaim default-8vbh7 bestbefore.k-belt.io/paused=true

# Undo k-belt's drift as well, then leave it alone
kubectl annotate nodeclaim default-8vbh7 bestbefore.k-belt.io/revert=true

# Hand it back to k-belt
kubectl annotate nodeclaim default-8vbh7 bestbefore.k-belt.io/revert-
```

`paused` freezes the NodeClaim in whatever state it is in. If k-belt had already marked it, it stays
marked and Karpenter will still replace it. Pause a node *before* it reaches `maxAge` and it is
never marked at all.

`revert` does what `paused` does and first puts the original hash back, so Karpenter clears the
drift. Use it when a node has been marked but you want to keep it.

Removing the annotation hands the NodeClaim back. If it is still older than `maxAge`, k-belt marks
it again within seconds, so remove the annotation only when you are ready for the node to go.

{: .warning }
> Reverting cannot stop a replacement Karpenter has already started. Once the node has the
> `karpenter.sh/disrupted` taint, it is going away. Check with
> `kubectl get nodeclaim <name> -o jsonpath='{.status.conditions[?(@.type=="DisruptionReason")]}'`.

Paused and reverted NodeClaims still count in a policy's `matched` and `stale` totals, which is why
`STALE` can sit above `DRIFTED` indefinitely on a cluster where you have paused a few nodes.

## Pausing a whole pool

There is no pause field on the policy. To stop rotations for a NodePool without deleting the policy,
set its `Drifted` budget to zero:

```yaml
spec:
  disruption:
    budgets:
      - nodes: "0"
        reasons: [Drifted]
```

k-belt keeps marking nodes, Karpenter stops acting on them, and removing the budget resumes the
rollout where it left off. Deleting the BestBefore instead un-marks every node it owns, which is the
right move if you want to abandon the rotation rather than postpone it.
