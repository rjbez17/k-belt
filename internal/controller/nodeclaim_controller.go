/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"cmp"
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	bestbeforev1alpha1 "github.com/rjbez17/k-belt/api/v1alpha1"
)

const (
	// EventReasonDrifted is emitted on a NodeClaim when k-belt marks it drifted.
	EventReasonDrifted = "BestBeforeExceeded"
	// EventReasonRestored is emitted on a NodeClaim when k-belt undoes its drift.
	EventReasonRestored = "BestBeforeRestored"

	// restoreRetryInterval is how long to wait when a restore can't pick a safe hash yet,
	// e.g. while Karpenter is migrating hashes to a new hash version.
	restoreRetryInterval = 30 * time.Second

	// DefaultResyncPeriod caps how long a NodeClaim can go unreconciled. Deadlines are still
	// scheduled exactly; the cap only bounds how late a missed or mis-computed deadline can be.
	DefaultResyncPeriod = 5 * time.Minute
	// MinResyncPeriod is the smallest resync period worth allowing: below this the periodic
	// reconciles cost more than the lateness they save.
	MinResyncPeriod = 10 * time.Second
	// resyncJitterFraction spreads resyncs out so NodeClaims reconciled together (at startup or
	// after a leader change) don't stay in lockstep.
	resyncJitterFraction = 10
)

// driftedByBestBefore reports whether k-belt replaced the NodeClaim's nodepool hash.
func driftedByBestBefore(nodeClaim *karpv1.NodeClaim) bool {
	return nodeClaim.Annotations[karpv1.NodePoolHashAnnotationKey] == bestbeforev1alpha1.DriftedHashValue
}

// driftedBy reports whether the named policy owns the NodeClaim's drift.
func driftedBy(nodeClaim *karpv1.NodeClaim, policyName string) bool {
	return driftedByBestBefore(nodeClaim) && nodeClaim.Annotations[bestbeforev1alpha1.PolicyAnnotationKey] == policyName
}

// staleAt is when the policy considers the NodeClaim too old. Both controllers use it so status
// can't disagree with what the NodeClaim controller does.
func staleAt(nodeClaim *karpv1.NodeClaim, policy *bestbeforev1alpha1.BestBefore) time.Time {
	return nodeClaim.CreationTimestamp.Add(policy.Spec.MaxAge.Duration)
}

// soonest returns the smallest positive duration, or 0 if there is none.
func soonest(durations ...time.Duration) time.Duration {
	var min time.Duration
	for _, d := range durations {
		if d > 0 && (min == 0 || d < min) {
			min = d
		}
	}
	return min
}

// NodeClaimReconciler owns every change k-belt makes to NodeClaims. For each NodeClaim it
// evaluates all BestBefore policies and either drifts it (some policy considers it stale) or
// restores its original hash (k-belt drifted it but no policy considers it stale any more).
type NodeClaimReconciler struct {
	client.Client
	Recorder events.EventRecorder
	Clock    clock.PassiveClock
	// ResyncPeriod caps RequeueAfter; DefaultResyncPeriod is used when unset.
	ResyncPeriod time.Duration
}

// +kubebuilder:rbac:groups=bestbefore.k-belt.sh,resources=bestbefores,verbs=get;list;watch
// +kubebuilder:rbac:groups=karpenter.sh,resources=nodeclaims,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=karpenter.sh,resources=nodepools,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile drifts or restores a single NodeClaim.
func (r *NodeClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	nodeClaim := &karpv1.NodeClaim{}
	if err := r.Get(ctx, req.NamespacedName, nodeClaim); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !nodeClaim.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	if _, revert := nodeClaim.Annotations[bestbeforev1alpha1.RevertAnnotationKey]; revert {
		return r.revert(ctx, nodeClaim)
	}
	if _, paused := nodeClaim.Annotations[bestbeforev1alpha1.PausedAnnotationKey]; paused {
		return r.requeue(), nil
	}

	policies := &bestbeforev1alpha1.BestBeforeList{}
	if err := r.List(ctx, policies); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing bestbefores: %w", err)
	}
	owner, nextStale := evaluate(nodeClaim, policies.Items, r.Clock.Now())

	var retryAfter time.Duration
	var err error
	switch {
	case owner != nil && !driftedByBestBefore(nodeClaim):
		err = r.drift(ctx, owner, nodeClaim)
	case owner != nil && nodeClaim.Annotations[bestbeforev1alpha1.PolicyAnnotationKey] != owner.Name:
		err = r.reattribute(ctx, owner, nodeClaim)
	case owner == nil && driftedByBestBefore(nodeClaim):
		retryAfter, err = r.restore(ctx, nodeClaim, "No BestBefore considers the NodeClaim stale any more")
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	// drift and restore update nodeClaim in place, so this reflects the state just written.
	wantTaint := owner != nil && owner.Spec.TaintDriftedNodes && driftedByBestBefore(nodeClaim)
	if err := r.syncTaint(ctx, nodeClaim.Status.NodeName, wantTaint); err != nil {
		return ctrl.Result{}, err
	}
	return r.requeue(nextStale, retryAfter), nil
}

// requeue schedules the soonest of the given deadlines, never later than the resync period.
// Capping every return path means a deadline this controller fails to compute delays a NodeClaim
// by one resync instead of indefinitely.
func (r *NodeClaimReconciler) requeue(deadlines ...time.Duration) ctrl.Result {
	return ctrl.Result{RequeueAfter: soonest(append(deadlines, r.jitteredResyncPeriod())...)}
}

func (r *NodeClaimReconciler) jitteredResyncPeriod() time.Duration {
	period := lo.Ternary(r.ResyncPeriod > 0, r.ResyncPeriod, DefaultResyncPeriod)
	jitter := period / resyncJitterFraction
	return period - jitter + time.Duration(rand.Int64N(int64(2*jitter)))
}

// revert restores a NodeClaim k-belt drifted and removes its taint, then leaves the NodeClaim
// alone for as long as the revert annotation stays.
func (r *NodeClaimReconciler) revert(ctx context.Context, nodeClaim *karpv1.NodeClaim) (ctrl.Result, error) {
	var retryAfter time.Duration
	if driftedByBestBefore(nodeClaim) {
		var err error
		if retryAfter, err = r.restore(ctx, nodeClaim, "The "+bestbeforev1alpha1.RevertAnnotationKey+" annotation is set"); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := r.syncTaint(ctx, nodeClaim.Status.NodeName, false); err != nil {
		return ctrl.Result{}, err
	}
	return r.requeue(retryAfter), nil
}

// syncTaint adds or removes the drifted taint on the NodeClaim's Node. NodeClaims without a
// registered Node are skipped; the NodeClaim update that records the Node name triggers a retry.
func (r *NodeClaimReconciler) syncTaint(ctx context.Context, nodeName string, want bool) error {
	if nodeName == "" {
		return nil
	}
	node := &corev1.Node{}
	if err := r.Get(ctx, types.NamespacedName{Name: nodeName}, node); err != nil {
		return client.IgnoreNotFound(err)
	}
	taint := corev1.Taint{Key: bestbeforev1alpha1.DriftedTaintKey, Effect: corev1.TaintEffectPreferNoSchedule}
	has := lo.ContainsBy(node.Spec.Taints, func(t corev1.Taint) bool { return t.MatchTaint(&taint) })
	if has == want {
		return nil
	}
	stored := node.DeepCopy()
	if want {
		node.Spec.Taints = append(node.Spec.Taints, taint)
	} else {
		node.Spec.Taints = lo.Reject(node.Spec.Taints, func(t corev1.Taint, _ int) bool { return t.MatchTaint(&taint) })
	}
	// Merge patches replace the whole taints list, so lock against concurrent taint changes.
	if err := r.Patch(ctx, node, client.MergeFromWithOptions(stored, client.MergeFromWithOptimisticLock{})); err != nil {
		return client.IgnoreNotFound(fmt.Errorf("patching taints on node %s: %w", nodeName, err))
	}
	logf.FromContext(ctx).V(1).Info("updated drifted taint", "Node", nodeName, "tainted", want)
	return nil
}

// evaluate returns the policy that should own a stale NodeClaim (nil if no policy considers it
// stale) and how long until the next matching policy will consider it stale (0 if none).
// The current owner keeps ownership while it still applies; otherwise the policy with the
// shortest maxAge wins, ties broken by name.
func evaluate(nodeClaim *karpv1.NodeClaim, policies []bestbeforev1alpha1.BestBefore, now time.Time) (*bestbeforev1alpha1.BestBefore, time.Duration) {
	var stale []*bestbeforev1alpha1.BestBefore
	var nextStale time.Duration
	for i := range policies {
		policy := &policies[i]
		if !policy.DeletionTimestamp.IsZero() || !selects(policy, nodeClaim) {
			continue
		}
		if deadline := staleAt(nodeClaim, policy); now.Before(deadline) {
			nextStale = soonest(nextStale, deadline.Sub(now))
			continue
		}
		stale = append(stale, policy)
	}
	if len(stale) == 0 {
		return nil, nextStale
	}
	if current := nodeClaim.Annotations[bestbeforev1alpha1.PolicyAnnotationKey]; current != "" {
		if owner, found := lo.Find(stale, func(p *bestbeforev1alpha1.BestBefore) bool { return p.Name == current }); found {
			return owner, nextStale
		}
	}
	return slices.MinFunc(stale, func(a, b *bestbeforev1alpha1.BestBefore) int {
		return cmp.Or(cmp.Compare(a.Spec.MaxAge.Duration, b.Spec.MaxAge.Duration), cmp.Compare(a.Name, b.Name))
	}), nextStale
}

// selects reports whether a policy's selector matches the NodeClaim. Invalid selectors match nothing.
func selects(policy *bestbeforev1alpha1.BestBefore, nodeClaim *karpv1.NodeClaim) bool {
	selector, err := metav1.LabelSelectorAsSelector(&policy.Spec.NodeClaimSelector)
	if err != nil {
		return false
	}
	return selector.Matches(labels.Set(nodeClaim.Labels))
}

// drift makes Karpenter consider the NodeClaim drifted, saving the hash it replaces. NodeClaims
// without both hash annotations are skipped: Karpenter's static drift check ignores them.
func (r *NodeClaimReconciler) drift(ctx context.Context, owner *bestbeforev1alpha1.BestBefore, nodeClaim *karpv1.NodeClaim) error {
	hash, hasHash := nodeClaim.Annotations[karpv1.NodePoolHashAnnotationKey]
	version, hasVersion := nodeClaim.Annotations[karpv1.NodePoolHashVersionAnnotationKey]
	if !hasHash || !hasVersion {
		return nil
	}
	stored := nodeClaim.DeepCopy()
	nodeClaim.Annotations[karpv1.NodePoolHashAnnotationKey] = bestbeforev1alpha1.DriftedHashValue
	nodeClaim.Annotations[bestbeforev1alpha1.PolicyAnnotationKey] = owner.Name
	nodeClaim.Annotations[bestbeforev1alpha1.OriginalHashAnnotationKey] = hash
	nodeClaim.Annotations[bestbeforev1alpha1.OriginalHashVersionAnnotationKey] = version
	nodeClaim.Annotations[bestbeforev1alpha1.DriftedAtAnnotationKey] = r.Clock.Now().UTC().Format(time.RFC3339)
	// A merge patch only touches these annotation keys, so no optimistic lock: Karpenter's frequent
	// status writes would otherwise make this conflict routinely.
	if err := r.Patch(ctx, nodeClaim, client.MergeFrom(stored)); err != nil {
		return client.IgnoreNotFound(fmt.Errorf("patching nodeclaim %s: %w", nodeClaim.Name, err))
	}
	logf.FromContext(ctx).Info("marked nodeclaim drifted", "BestBefore", owner.Name,
		"age", r.Clock.Since(nodeClaim.CreationTimestamp.Time).Round(time.Second))
	r.event(nodeClaim, owner, EventReasonDrifted, "Drift",
		"NodeClaim is older than BestBefore %s maxAge %s; marked drifted for graceful replacement",
		owner.Name, owner.Spec.MaxAge.Duration)
	return nil
}

// reattribute hands a drifted NodeClaim to another stale policy, e.g. after its owner was deleted.
func (r *NodeClaimReconciler) reattribute(ctx context.Context, owner *bestbeforev1alpha1.BestBefore, nodeClaim *karpv1.NodeClaim) error {
	stored := nodeClaim.DeepCopy()
	nodeClaim.Annotations[bestbeforev1alpha1.PolicyAnnotationKey] = owner.Name
	if err := r.Patch(ctx, nodeClaim, client.MergeFrom(stored)); err != nil {
		return client.IgnoreNotFound(fmt.Errorf("patching nodeclaim %s: %w", nodeClaim.Name, err))
	}
	return nil
}

// restore undoes k-belt's drift once no policy considers the NodeClaim stale, reporting reason in
// the emitted event. NodeClaims Karpenter has already started disrupting are left alone: restoring
// their hash can't stop the replacement. A non-zero retryAfter asks for another attempt later.
func (r *NodeClaimReconciler) restore(ctx context.Context, nodeClaim *karpv1.NodeClaim, reason string) (retryAfter time.Duration, err error) {
	if nodeClaim.StatusConditions().Get(karpv1.ConditionTypeDisruptionReason).IsTrue() {
		logf.FromContext(ctx).V(1).Info("not restoring nodeclaim Karpenter is already disrupting")
		return 0, nil
	}
	hash, ok, err := r.restoreHash(ctx, nodeClaim)
	if err != nil {
		return 0, err
	}
	if !ok {
		// Karpenter is mid-way through a hash-version migration; its own hashes settle shortly.
		logf.FromContext(ctx).V(1).Info("no safe nodepool hash to restore yet")
		return restoreRetryInterval, nil
	}

	stored := nodeClaim.DeepCopy()
	owner := nodeClaim.Annotations[bestbeforev1alpha1.PolicyAnnotationKey]
	nodeClaim.Annotations[karpv1.NodePoolHashAnnotationKey] = hash
	for _, key := range []string{bestbeforev1alpha1.PolicyAnnotationKey, bestbeforev1alpha1.OriginalHashAnnotationKey, bestbeforev1alpha1.OriginalHashVersionAnnotationKey, bestbeforev1alpha1.DriftedAtAnnotationKey} {
		delete(nodeClaim.Annotations, key)
	}
	// Optimistic lock here: if Karpenter rewrote the hash since our read, re-evaluate instead of
	// overwriting it with a hash that may no longer be valid.
	if err := r.Patch(ctx, nodeClaim, client.MergeFromWithOptions(stored, client.MergeFromWithOptimisticLock{})); err != nil {
		return 0, client.IgnoreNotFound(fmt.Errorf("restoring nodeclaim %s: %w", nodeClaim.Name, err))
	}
	logf.FromContext(ctx).Info("restored nodeclaim hash", "BestBefore", owner)
	r.event(nodeClaim, nil, EventReasonRestored, "Restore",
		"%s (previously drifted by %s); restored its nodepool hash", reason, owner)
	return 0, nil
}

// restoreHash picks the hash to put back. The saved original is only valid if Karpenter hasn't
// since changed its hash version; otherwise use the NodePool's current hash, as Karpenter's own
// hash-version migration does. ok is false when neither is safe to use yet.
func (r *NodeClaimReconciler) restoreHash(ctx context.Context, nodeClaim *karpv1.NodeClaim) (hash string, ok bool, err error) {
	version := nodeClaim.Annotations[karpv1.NodePoolHashVersionAnnotationKey]
	if original, found := nodeClaim.Annotations[bestbeforev1alpha1.OriginalHashAnnotationKey]; found &&
		nodeClaim.Annotations[bestbeforev1alpha1.OriginalHashVersionAnnotationKey] == version {
		return original, true, nil
	}
	nodePool := &karpv1.NodePool{}
	if err := r.Get(ctx, types.NamespacedName{Name: nodeClaim.Labels[karpv1.NodePoolLabelKey]}, nodePool); err != nil {
		return "", false, client.IgnoreNotFound(err)
	}
	current, found := nodePool.Annotations[karpv1.NodePoolHashAnnotationKey]
	if !found || nodePool.Annotations[karpv1.NodePoolHashVersionAnnotationKey] != version {
		return "", false, nil
	}
	return current, true, nil
}

func (r *NodeClaimReconciler) event(nodeClaim *karpv1.NodeClaim, related *bestbeforev1alpha1.BestBefore, reason, action, note string, args ...any) {
	if r.Recorder == nil {
		return
	}
	var relatedObj client.Object
	if related != nil {
		relatedObj = related
	}
	r.Recorder.Eventf(nodeClaim, relatedObj, corev1.EventTypeNormal, reason, action, note, args...)
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeClaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Karpenter writes NodeClaim status constantly; only metadata, the Node name and the
		// disruption condition change what this controller does. The resync period bounds the cost
		// of anything this filter turns out to miss.
		For(&karpv1.NodeClaim{}, builder.WithPredicates(nodeClaimChanged())).
		// Any spec change, creation or deletion of a policy can drift or restore any NodeClaim.
		Watches(&bestbeforev1alpha1.BestBefore{},
			handler.EnqueueRequestsFromMapFunc(r.allNodeClaims),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("bestbefore-nodeclaim").
		Complete(r)
}

func (r *NodeClaimReconciler) allNodeClaims(ctx context.Context, _ client.Object) []reconcile.Request {
	list := &karpv1.NodeClaimList{}
	if err := r.List(ctx, list); err != nil {
		logf.FromContext(ctx).Error(err, "listing nodeclaims")
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
	}
	return requests
}
