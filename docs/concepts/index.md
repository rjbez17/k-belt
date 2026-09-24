---
title: Concepts
nav_order: 3
has_children: true
---

# Concepts

k-belt sits beside Karpenter and nudges it. It does not provision, drain or delete anything. Each
page here covers one part of how that works.

* [BestBefore]({{ site.baseurl }}/concepts/bestbefore/): what a policy does to a NodeClaim, and how
  the change is undone.
* [Disruption]({{ site.baseurl }}/concepts/disruption/): what Karpenter does with a drifted node,
  and the cases where it does nothing at all.
