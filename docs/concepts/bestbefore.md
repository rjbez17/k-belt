---
title: BestBefore
parent: Concepts
nav_order: 1
---

# BestBefore
{: .no_toc }

1. TOC
{:toc}

## Marking a node as too old

Karpenter stamps every NodeClaim with the hash of the NodePool that created it, in the
`karpenter.sh/nodepool-hash` annotation. When that hash stops matching the NodePool's own hash,
Karpenter considers the NodeClaim drifted and replaces it. That is how a NodePool edit rolls out to
existing nodes.

BestBefore uses the same mechanism for age. When a NodeClaim is older than a matching policy's
`maxAge`, k-belt replaces the hash with a value that can never be a real one, and records what it
took away:

```bash
kubectl get nodeclaim default-8vbh7 -o jsonpath='{.metadata.annotations}' | jq
```

```json
{
  "karpenter.sh/nodepool-hash": "bestbefore.k-belt.io",
  "karpenter.sh/nodepool-hash-version": "v3",
  "bestbefore.k-belt.io/policy": "default-pool",
  "bestbefore.k-belt.io/original-nodepool-hash": "7428780003907761376",
  "bestbefore.k-belt.io/original-nodepool-hash-version": "v3",
  "bestbefore.k-belt.io/drifted-at": "2026-09-18T03:56:11Z"
}
```

Karpenter's drift controller picks it up within seconds and sets the `Drifted` status condition with
reason `NodePoolDrifted`. From that point the node is an ordinary drifted node, and Karpenter's
disruption controller replaces it under the NodePool's budgets.

k-belt also emits an event on the NodeClaim, so `kubectl describe nodeclaim` shows why it happened:

```text
Normal  BestBeforeExceeded  4m  k-belt  NodeClaim is older than BestBefore default-pool maxAge 504h;
                                        marked drifted for graceful replacement
```

## Undoing a drift

A policy is a live object, so the answer to "is this node too old?" can change. Delete the policy,
raise its `maxAge`, or narrow its selector, and k-belt puts the original hash back and removes its
annotations. Karpenter clears the `Drifted` condition on its next pass and the node stays.

k-belt handles two awkward cases here:

* **Karpenter changed its hash format.** Karpenter versions its hashes with
  `karpenter.sh/nodepool-hash-version`. If that version has moved on since the drift, the saved hash
  is stale, so k-belt restores the NodePool's current hash instead, which is what Karpenter's own
  migration does.
* **Karpenter already started replacing the node.** Once the disruption controller has taken the
  node, restoring the hash cannot call that off. k-belt leaves those NodeClaims alone rather than
  writing an annotation that would do nothing.

{: .note }
> Restoring is best effort. With a generous disruption budget, only seconds pass between a node
> being marked and Karpenter acting on it.

## Which policy owns a node

Several policies can match the same NodeClaim. A node is rotated as soon as *any* matching policy
considers it too old, and exactly one policy owns the result, recorded in
`bestbefore.k-belt.io/policy`.

The owner keeps ownership while its own `maxAge` still applies. When it no longer does, usually
because the policy was deleted, another policy that still considers the node stale takes over. If
none does, the node is restored. When there is no incumbent, the policy with the shortest `maxAge`
wins, and ties are broken by name.

A node counts toward `matched` and `stale` on every policy that selects it, so those totals across
policies can add up to more than the number of nodes you have.

## Keeping the mark in place

k-belt is level triggered: every reconcile asks what the NodeClaim should look like now and fixes it
if it doesn't. If something else rewrites the hash while the node is still too old, such as a
Karpenter upgrade that migrates hash versions or an operator running `kubectl annotate`, k-belt
marks it again on the next pass.

Each NodeClaim is also re-checked at least every five minutes, whatever else happens, so a missed
deadline delays a rotation by minutes rather than indefinitely. See
[Settings]({{ site.baseurl }}/reference/settings/) to change that interval.
