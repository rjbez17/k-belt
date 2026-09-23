---
title: Annotations and taints
parent: Reference
nav_order: 2
---

# Annotations and taints

## Set by you

On a NodeClaim, to control k-belt for that node. Only presence matters; the value is ignored.

| Key | Effect |
| --- | --- |
| `bestbefore.k-belt.io/paused` | k-belt leaves the NodeClaim exactly as it is: not marked, not restored, taint untouched |
| `bestbefore.k-belt.io/revert` | As `paused`, and first restores the hash k-belt replaced |

If both are present, `revert` wins. See
[Pausing and reverting nodes]({{ site.baseurl }}/tasks/pause-and-revert/).

## Set by k-belt

On a NodeClaim it has marked. Treat these as internal: they exist so the drift can be undone and so
you can see what happened.

| Key | Value |
| --- | --- |
| `karpenter.sh/nodepool-hash` | Overwritten with `bestbefore.k-belt.io` (Karpenter's own annotation) |
| `bestbefore.k-belt.io/policy` | Name of the BestBefore that owns the drift |
| `bestbefore.k-belt.io/original-nodepool-hash` | The hash that was replaced |
| `bestbefore.k-belt.io/original-nodepool-hash-version` | Karpenter hash version that hash belongs to |
| `bestbefore.k-belt.io/drifted-at` | When the NodeClaim was marked, RFC 3339 |

On the Node, when the owning policy sets `taintDriftedNodes`:

| Taint | Effect |
| --- | --- |
| `bestbefore.k-belt.io/drifted` | `PreferNoSchedule` |

## Karpenter keys that matter here

| Key | Why it matters here |
| --- | --- |
| `karpenter.sh/nodepool` | The label nearly every selector uses |
| `karpenter.sh/do-not-disrupt` | On a Node or pod, stops Karpenter replacing a marked node |
| `karpenter.sh/disrupted` | Taint Karpenter adds once it has committed to replacing the node; past this point a revert changes nothing |
