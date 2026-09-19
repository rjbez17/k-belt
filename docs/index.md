---
title: Home
nav_order: 1
---

# k-belt

k-belt is a set of controllers for clusters that already run [Karpenter](https://karpenter.sh). Each
one fills a gap that shows up once Karpenter is managing real production capacity. Today there is
one controller, **BestBefore**, which rotates ageing nodes without the mass churn that
`expireAfter` can cause.

## The problem BestBefore solves

Karpenter's `expireAfter` gives you a maximum node lifetime, which most teams need for compliance or
for picking up AMI patches. But expiration is forceful: when a NodeClaim reaches its expiry,
Karpenter deletes it immediately, regardless of the NodePool's disruption budgets and without
waiting for replacement capacity.

With node ages spread out, that works. After a large scale-up or a cluster migration, hundreds of
nodes share a creation time within a few minutes, so they share an expiry too, and Karpenter deletes
them as fast as it finds them.

BestBefore rotates nodes through Karpenter's drift instead, which is rate limited by disruption
budgets and launches replacements before draining anything.

## How it works

You give BestBefore a label selector and a maximum age:

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
```

k-belt then:

* **Watches** the NodeClaims the selector matches
* **Marks** any NodeClaim older than `maxAge` as drifted, recording what it changed
* **Waits** while Karpenter replaces those nodes under the NodePool's disruption budgets
* **Restores** the NodeClaim if the policy stops applying, so nothing is rotated by accident

Karpenter does the disrupting. k-belt only decides which nodes are too old.

## Where to start

* [Getting Started]({{ site.baseurl }}/getting-started/) installs k-belt and rotates your first node.
* [Concepts]({{ site.baseurl }}/concepts/) explains how drift, restore and overlapping policies work.
* [Tasks]({{ site.baseurl }}/tasks/) covers sizing `maxAge`, pausing a node, and alerting on a stalled rollout.
* [Reference]({{ site.baseurl }}/reference/) is the full API, annotations, metrics and flags.

{: .warning }
> k-belt is pre-release. The API group is `v1alpha1` and can change. Keep `expireAfter` set on your
> NodePools as a backstop.
