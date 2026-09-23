---
title: Home
nav_order: 1
---

# k-belt

k-belt is a set of controllers for clusters that already run [Karpenter](https://karpenter.sh). Each
one fills a gap that shows up once Karpenter is managing production capacity. It currently ships a
single controller, **BestBefore**, which rotates ageing nodes without the mass churn `expireAfter`
can cause.

## Why BestBefore exists

Karpenter's `expireAfter` is the usual way to put a ceiling on node age, and most clusters want one:
it picks up AMI patches and satisfies rules about how long a host may live. Two properties of it are
worth understanding before relying on it alone.

**Expiry is fixed when the node is created.** `expireAfter` lives in the NodeClaim spec, which
Kubernetes rejects any change to, and the expiry is `creationTimestamp + expireAfter`. Editing the
NodePool sets the value for future NodeClaims only.

**Expiry cannot be called off.** When a NodeClaim reaches its expiry, Karpenter deletes it: no
disruption budget is consulted, no replacement is launched first, and no annotation on the node or
its pods prevents it. Draining still respects PodDisruptionBudgets, so the node may take a while to
go, but the decision has been made.

Neither matters much when node ages are spread out. Both matter in two situations that come up on
real clusters.

### Mass expiration

Clusters scale because traffic grew, so nodes arrive in batches. Every node in a batch shares a
creation time within a few minutes, and therefore its possible an expiry within a few minutes. When it arrives,
Karpenter removes them as fast as it finds them.

Traffic is often predictable, which makes this worse: a batch created during yesterday's peak
expires during today's. Perfect PodDisruptionBudgets and warm caches would absorb that. Most
clusters have neither.

### Incidents and cloud outages

During an AZ outage the priority is holding on to the compute that still works. Expiration doesn't
know that. It keeps deleting nodes in healthy zones on schedule, at the moment every other workload
in the region is trying to scale into those same zones, and replacements are slowest to come back
exactly when they matter most.

### A pattern that avoids both

Rotate nodes on a short cycle with BestBefore, weekly for example, and keep `expireAfter` on a much
longer one such as monthly. Rotation then runs through drift, which budgets pace and which pre-spins
replacements, and which can be paused or reverted while an incident is in progress. A node that
reaches `expireAfter` means the graceful path didn't keep up, so expiry becomes a backstop rather
than the normal way nodes are replaced.

## How it works

A BestBefore policy selects NodeClaims by label and gives them a maximum age:

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
> k-belt is pre-release. The API is `v1alpha1` and may change between releases. Keep `expireAfter`
> set on your NodePools as a backstop, and please
> [report what breaks](https://github.com/rjbez17/k-belt/issues).
