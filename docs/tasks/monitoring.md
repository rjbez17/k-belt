---
title: Monitoring
parent: Tasks
nav_order: 3
---

# Monitoring
{: .no_toc }

1. TOC
{:toc}

## Status

```bash
kubectl get bestbefores
```

```text
NAME           MAX AGE   MATCHED   STALE   DRIFTED   AGE
default-pool   504h      42        6       6         2m11s
spot-pool      168h      18        0       0         2m11s
```

| Column | Meaning |
| --- | --- |
| `MATCHED` | NodeClaims the selector matches |
| `STALE` | Matched NodeClaims older than `maxAge` |
| `DRIFTED` | NodeClaims this policy has marked that Karpenter hasn't replaced yet |

In a healthy rotation `DRIFTED` tracks `STALE` and both fall to zero. If the policy sets
`maxConcurrent`, `DRIFTED` will stay at that limit until the rotation completes. `STALE` well above `DRIFTED`
means k-belt could not mark some nodes: they are paused, another policy owns them, or they are
missing Karpenter's hash annotations.

The `Ready` condition carries configuration problems, such as a selector the API server accepted but
k-belt cannot parse:

```bash
kubectl get bestbefore default-pool -o jsonpath='{.status.conditions[?(@.type=="Ready")]}'
```

## Events

k-belt writes events on the NodeClaim, not on the policy:

```bash
kubectl get events --field-selector involvedObject.kind=NodeClaim | grep BestBefore
```

```text
4m   Normal   BestBeforeExceeded   nodeclaim/default-8vbh7   NodeClaim is older than BestBefore default-pool maxAge 504h; marked drifted for graceful replacement
1m   Normal   BestBeforeRestored   nodeclaim/default-gjpbg   No BestBefore considers the NodeClaim stale any more (previously drifted by default-pool); restored its nodepool hash
```

Karpenter's own events on the Node explain why a marked node has not been replaced yet. Look for
`DisruptionBlocked`.

## Metrics

The controller serves these on its metrics endpoint, labelled by policy:

| Metric | Meaning |
| --- | --- |
| `kbelt_bestbefore_matched_nodeclaims` | NodeClaims the selector matches |
| `kbelt_bestbefore_stale_nodeclaims` | Matched NodeClaims older than `maxAge` |
| `kbelt_bestbefore_drifted_nodeclaims` | NodeClaims marked and not yet replaced |
| `kbelt_bestbefore_oldest_drift_timestamp_seconds` | When the longest-standing drift started, `0` if none |

## The alert worth having

A node that stays marked for a long time is a rotation that has stalled, and it will eventually be
force-expired instead. Alert on the age of the oldest outstanding drift:

```yaml
- alert: BestBeforeRotationStalled
  expr: |
    kbelt_bestbefore_drifted_nodeclaims > 0
      and time() - kbelt_bestbefore_oldest_drift_timestamp_seconds > 86400
  for: 15m
  annotations:
    summary: "BestBefore {{ $labels.bestbefore }} has had a node waiting to rotate for over a day"
```

Pick a threshold from your own numbers: comfortably longer than a normal rollout, comfortably shorter
than `expireAfter - maxAge`.

The chart protects the metrics endpoint with authn/authz by default. To scrape it, bind your
scraper's ServiceAccount to the `k-belt-metrics-reader` ClusterRole, or set
`metrics.serviceMonitor.enabled` if you run the Prometheus Operator.
