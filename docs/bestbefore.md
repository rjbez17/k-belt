# BestBefore

`BestBefore` rotates old Karpenter nodes **gracefully**: through Karpenter's drift, which honours
NodePool disruption budgets and launches replacement capacity first, instead of through
`expireAfter`, which deletes expired nodes regardless of budgets.

- [How it works](#how-it-works)
- [Spec reference](#spec-reference)
- [Pausing and reverting NodeClaims](#pausing-and-reverting-nodeclaims)
- [Status, events and metrics](#status-events-and-metrics)
- [Controller flags](#controller-flags)
- [Operating BestBefore](#operating-bestbefore)
  - [Sizing maxAge against expireAfter](#sizing-maxage-against-expireafter)
  - [Drift can stall](#drift-can-stall)
  - [terminationGracePeriod makes drift eventually forceful](#terminationgraceperiod-makes-drift-eventually-forceful)
  - [Pods can move more than once](#pods-can-move-more-than-once)
  - [Drift delays consolidation](#drift-delays-consolidation)
  - [Rollout order and first installs](#rollout-order-and-first-installs)
  - [Manual undo](#manual-undo)

## How it works

Karpenter stamps every NodeClaim with its NodePool's hash (`karpenter.sh/nodepool-hash`) and marks
the NodeClaim `Drifted` (reason `NodePoolDrifted`) when the two differ. k-belt uses that check:

1. **Drift.** When a NodeClaim is older than `maxAge` for any `BestBefore` whose selector matches it,
   k-belt replaces its hash with `bestbefore.k-belt.sh` and records:

   | Annotation | Value |
   |---|---|
   | `bestbefore.k-belt.sh/policy` | Policy that owns the drift |
   | `bestbefore.k-belt.sh/original-nodepool-hash` | Hash k-belt replaced |
   | `bestbefore.k-belt.sh/original-nodepool-hash-version` | Karpenter hash version of that hash |
   | `bestbefore.k-belt.sh/drifted-at` | When k-belt drifted it (RFC 3339) |

   Karpenter then replaces the node like any other drifted node.

2. **Restore.** If no policy considers the NodeClaim stale any more (the policy was deleted, its
   `maxAge` raised or its selector changed), k-belt writes the original hash back and removes its
   annotations, and Karpenter clears `Drifted`. If Karpenter has changed hash versions since the
   drift, k-belt restores the NodePool's current hash instead. NodeClaims Karpenter has already
   started disrupting are left alone: restoring can't stop a replacement in progress.

3. **Overlapping policies.** A NodeClaim is drifted as soon as *any* matching policy considers it
   stale. The owning policy keeps it while it still applies; otherwise the stale policy with the
   shortest `maxAge` takes over (ties broken by name).

k-belt re-applies its hash if something rewrites it while the NodeClaim is still stale, such as
Karpenter's hash-version migration during an upgrade, so drift survives it as long as k-belt is
running.

`internal/controller/karpenter_contract_test.go` runs Karpenter's own drift and hash controllers
against these changes, so a Karpenter upgrade that breaks this behaviour fails `make test`, and
`make test-e2e` exercises the whole flow against a real Karpenter on kind + KWOK.

## Spec reference

```yaml
apiVersion: bestbefore.k-belt.sh/v1alpha1
kind: BestBefore
metadata:
  name: default-pool
spec:
  nodeClaimSelector:        # required; standard label selector, {} matches every NodeClaim
    matchLabels:
      karpenter.sh/nodepool: default
  maxAge: 504h              # required; positive Go duration (no "d" unit: 21 days = 504h)
  taintDriftedNodes: false  # optional; see "Pods can move more than once"
```

`BestBefore` is cluster-scoped.

## Pausing and reverting NodeClaims

Two annotations control k-belt per NodeClaim. Only presence matters; any value works, including an
empty string.

| Annotation | Effect |
|---|---|
| `bestbefore.k-belt.sh/paused` | k-belt ignores the NodeClaim: it isn't drifted, restored or tainted, and anything k-belt already did stays as it is. |
| `bestbefore.k-belt.sh/revert` | Everything `paused` does, and first undoes k-belt's drift: restores the original hash (same rules as [restore](#how-it-works)) and removes the drifted taint. |

```sh
kubectl annotate nodeclaim <name> bestbefore.k-belt.sh/paused=true     # freeze
kubectl annotate nodeclaim <name> bestbefore.k-belt.sh/revert=true     # undo and freeze
kubectl annotate nodeclaim <name> bestbefore.k-belt.sh/revert-         # hand back to k-belt
```

If both are set, `revert` wins. Removing the annotation returns the NodeClaim to normal handling, so
a NodeClaim that is still stale is drifted again. As with automatic restore, reverting can't stop a
replacement Karpenter has already started. Paused and reverted NodeClaims still count toward a
policy's `matched` and `stale` numbers.

## Status, events and metrics

`kubectl get bestbefores` shows `Max Age`, `Matched`, `Stale` and `Drifted`:

| Status field | Meaning |
|---|---|
| `matchedNodeClaims` | NodeClaims the selector matches |
| `staleNodeClaims` | Matched NodeClaims older than `maxAge` |
| `driftedNodeClaims` | NodeClaims this policy drifted that Karpenter hasn't removed yet |
| `conditions[Ready]` | `False` with reason `InvalidSelector` if the selector can't be parsed |

A policy whose selector can't be parsed matches nothing, so its drifted NodeClaims are restored
(the safe direction) until the selector is fixed.

Fewer drifted than stale NodeClaims means some couldn't be drifted: they lack Karpenter's hash
annotations, or another policy owns them.

Events on NodeClaims: `BestBeforeExceeded` (drifted) and `BestBeforeRestored`.

Prometheus gauges on the controller metrics endpoint, labelled `bestbefore`:

| Metric | Meaning |
|---|---|
| `kbelt_bestbefore_matched_nodeclaims` | As `matchedNodeClaims` |
| `kbelt_bestbefore_stale_nodeclaims` | As `staleNodeClaims` |
| `kbelt_bestbefore_drifted_nodeclaims` | As `driftedNodeClaims` |
| `kbelt_bestbefore_oldest_drift_timestamp_seconds` | Unix time the longest-standing drift started, `0` if none |

## Controller flags

| Flag | Default | Meaning |
|---|---|---|
| `--bestbefore-resync-period` | `5m` (minimum `10s`) | How often every NodeClaim is re-evaluated on top of its exact `maxAge` deadline. It bounds how late k-belt can be if a deadline is missed; it does not refresh cached data. Reconciles are cache reads, so the cost is small: about 7/second at 2,000 NodeClaims. |

Each NodeClaim's resync is jittered by ±10% so they don't all reconcile in lockstep after a restart
or leader change.

## Operating BestBefore

### Sizing maxAge against expireAfter

Keep `expireAfter` on the NodePool as the hard backstop, and leave enough room between the two for a
full rollout to finish:

```
expireAfter - maxAge  >  rollout time
rollout time          ≈  nodes in pool / nodes the Drifted budget allows at once × time to replace one node
```

Time to replace one node covers launching the replacement, waiting for it to initialize, draining
and terminating: usually a few minutes, longer with slow-draining pods.

| Pool | Drifted budget | Per node | Rollout | Minimum headroom |
|---|---|---|---|---|
| 50 nodes | `10%` (5 at once) | 10m | ~1h 40m | a few hours |
| 500 nodes | `10%` (50 at once) | 10m | ~1h 40m | a few hours |
| 500 nodes | `1` | 10m | ~83h | 4+ days |

Nodes still waiting when `expireAfter` arrives are force-expired, which is the burst BestBefore
exists to avoid. Budgets not scoped to a reason also count nodes disrupted for other reasons, so
leave extra margin. Setting `maxAge >= expireAfter` makes a policy do nothing.

### Drift can stall

Marking a NodeClaim drifted doesn't guarantee Karpenter replaces it. Karpenter skips drifted nodes
that:

- carry `karpenter.sh/do-not-disrupt: "true"` on the node,
- aren't initialized, or were just nominated for a pending pod,
- host `do-not-disrupt` pods or pods whose PDB allows no evictions (unless the NodeClaim has a
  `terminationGracePeriod`, see below),
- host pods Karpenter's scheduling simulation can't place elsewhere, or
- belong to a NodePool whose budget stays at zero.

Karpenter emits `DisruptionBlocked` events on the node explaining why. Stalled nodes sit until
`expireAfter` force-deletes them, so alert on long-standing drift:

```promql
kbelt_bestbefore_drifted_nodeclaims > 0
  and time() - kbelt_bestbefore_oldest_drift_timestamp_seconds > 86400
```

### terminationGracePeriod makes drift eventually forceful

Karpenter treats drift as *eventual* disruption. Whether blocking pods can hold it up depends on the
NodePool's `terminationGracePeriod` (TGP):

| TGP | Drifted node with a blocking PDB or `do-not-disrupt` pod |
|---|---|
| unset | Not disrupted; waits indefinitely (until `expireAfter`) |
| set | Disrupted within the budget after its replacement is ready; drained honouring PDBs, then forcibly after TGP |

With TGP set BestBefore still beats `expireAfter`, because budgets and replacement-first still apply,
but it will not protect pods that opt out of disruption indefinitely. Leave TGP unset if blocking
pods must always win; set it if rollouts need an upper bound.

### Pods can move more than once

When Karpenter plans where a drifted node's pods go, other stale nodes still count as valid
destinations, and the scheduler uses them too. A pod evicted from one stale node can land on another
and be evicted again when that node's turn comes. Karpenter's own drift (for example after an AMI
change) behaves the same way. Larger Drifted budgets shorten the window.

`taintDriftedNodes: true` adds a `bestbefore.k-belt.sh/drifted:PreferNoSchedule` taint to drifted
nodes (removed if the drift is restored) so the scheduler prefers fresh nodes. **Caveat:** Karpenter's
scheduling simulation treats `PreferNoSchedule` taints on existing nodes as hard constraints unless a
NodePool template also carries a `PreferNoSchedule` taint. With tainting on, Karpenter may launch
extra capacity for pods that would fit on tainted nodes and consolidate less effectively until the
rollout completes. Adding any `PreferNoSchedule` taint to the NodePool template makes Karpenter relax
these as preferences.

### Drift delays consolidation

Karpenter's disruption loop acts on one method per pass in a fixed order (emptiness, static drift,
drift, multi-node consolidation, single-node consolidation) and starts over after any action. Nodes
being disrupted for any reason also count against every budget. While a large drift backlog has
budget to proceed, consolidation rarely runs, so costs can rise during a rollout.

Give drift and consolidation separate room with reason-scoped budgets, and confine drift to a window:

```yaml
spec:
  disruption:
    budgets:
      - nodes: "10%"
        reasons: [Underutilized, Empty]
      - nodes: "5%"
        reasons: [Drifted]
      - nodes: "0"              # no drift during business hours (schedules are UTC)
        reasons: [Drifted]
        schedule: "0 8 * * mon-fri"
        duration: 10h
```

These budgets apply to all drift, including AMI and NodeClass changes, not just BestBefore's.

### Rollout order and first installs

Karpenter replaces drifted nodes in the order their `Drifted` condition was set, not by node age.
k-belt drifts every stale NodeClaim as soon as it goes stale, so when many go stale at once the
order among them is effectively arbitrary. If headroom runs short, some of the *oldest* nodes may
reach `expireAfter` while younger ones rotate.

This matters most when adding a policy to a mature cluster, where a large share of nodes may already
be stale. Phase it in:

1. Create the policy with `maxAge` just below the age of your oldest nodes and check
   `kubectl get bestbefores` for the `Stale` count.
2. Watch the first rollout complete (`Drifted` back to `0`).
3. Lower `maxAge` in steps toward the target, letting each rollout finish first.

### Manual undo

To undo k-belt's drift on a single NodeClaim, annotate it with `bestbefore.k-belt.sh/revert` (see
[Pausing and reverting NodeClaims](#pausing-and-reverting-nodeclaims)). If k-belt isn't running,
restore the hash by hand before Karpenter starts replacing the node:

```sh
NC=<nodeclaim>
HASH=$(kubectl get nodeclaim "$NC" -o jsonpath='{.metadata.annotations.bestbefore\.k-belt\.sh/original-nodepool-hash}')
kubectl annotate nodeclaim "$NC" --overwrite karpenter.sh/nodepool-hash="$HASH" bestbefore.k-belt.sh/paused=true \
  bestbefore.k-belt.sh/policy- bestbefore.k-belt.sh/original-nodepool-hash- \
  bestbefore.k-belt.sh/original-nodepool-hash-version- bestbefore.k-belt.sh/drifted-at-
```

Adding `bestbefore.k-belt.sh/paused` stops k-belt drifting it again once it's back. If the NodeClaim's
`karpenter.sh/nodepool-hash-version` no longer matches the saved `original-nodepool-hash-version`,
use the NodePool's current `karpenter.sh/nodepool-hash` instead. Nodes Karpenter has already tainted
`karpenter.sh/disrupted` will be replaced regardless.
