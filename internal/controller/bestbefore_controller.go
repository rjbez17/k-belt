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
	"context"
	"fmt"
	"time"

	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	bestbeforev1alpha1 "github.com/rjbez17/k-belt/api/v1alpha1"
)

// ConditionTypeReady reports whether the policy is valid and its status is current.
const ConditionTypeReady = "Ready"

// BestBeforeReconciler maintains BestBefore status. It never changes NodeClaims; NodeClaimReconciler does.
type BestBeforeReconciler struct {
	client.Client
	Clock clock.PassiveClock
}

// +kubebuilder:rbac:groups=bestbefore.k-belt.io,resources=bestbefores,verbs=get;list;watch
// +kubebuilder:rbac:groups=bestbefore.k-belt.io,resources=bestbefores/status,verbs=get;patch
// +kubebuilder:rbac:groups=karpenter.sh,resources=nodeclaims,verbs=get;list;watch

// Reconcile counts the NodeClaims a policy matches, considers stale and has drifted, and requeues
// for when the next matched NodeClaim goes stale.
func (r *BestBeforeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	bestBefore := &bestbeforev1alpha1.BestBefore{}
	if err := r.Get(ctx, req.NamespacedName, bestBefore); err != nil {
		if apierrors.IsNotFound(err) {
			deleteBestBeforeMetrics(req.Name)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !bestBefore.DeletionTimestamp.IsZero() {
		deleteBestBeforeMetrics(bestBefore.Name)
		return ctrl.Result{}, nil
	}
	stored := bestBefore.DeepCopy()

	selector, err := metav1.LabelSelectorAsSelector(&bestBefore.Spec.NodeClaimSelector)
	if err != nil {
		// An invalid selector won't fix itself; report it and wait for a spec change.
		deleteBestBeforeMetrics(bestBefore.Name)
		bestBefore.Status.ObservedGeneration = bestBefore.Generation
		r.setReady(bestBefore, metav1.ConditionFalse, "InvalidSelector", err.Error())
		return ctrl.Result{}, r.patchStatus(ctx, stored, bestBefore)
	}

	nodeClaims := &karpv1.NodeClaimList{}
	if err := r.List(ctx, nodeClaims, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing nodeclaims: %w", err)
	}

	now := r.Clock.Now()
	var stale, drifted int32
	var nextStale time.Duration
	var oldestDrift time.Time
	for i := range nodeClaims.Items {
		nodeClaim := &nodeClaims.Items[i]
		if driftedBy(nodeClaim, bestBefore.Name) {
			drifted++
			if driftedAt, err := time.Parse(time.RFC3339, nodeClaim.Annotations[bestbeforev1alpha1.DriftedAtAnnotationKey]); err == nil &&
				(oldestDrift.IsZero() || driftedAt.Before(oldestDrift)) {
				oldestDrift = driftedAt
			}
		}
		if deadline := staleAt(nodeClaim, bestBefore); now.Before(deadline) {
			nextStale = soonest(nextStale, deadline.Sub(now))
			continue
		}
		stale++
	}

	bestBefore.Status.ObservedGeneration = bestBefore.Generation
	bestBefore.Status.MatchedNodeClaims = int32(len(nodeClaims.Items))
	bestBefore.Status.StaleNodeClaims = stale
	bestBefore.Status.DriftedNodeClaims = drifted
	matchedNodeClaimsGauge.WithLabelValues(bestBefore.Name).Set(float64(len(nodeClaims.Items)))
	staleNodeClaimsGauge.WithLabelValues(bestBefore.Name).Set(float64(stale))
	driftedNodeClaimsGauge.WithLabelValues(bestBefore.Name).Set(float64(drifted))
	oldestDriftTimestampGauge.WithLabelValues(bestBefore.Name).Set(lo.Ternary(oldestDrift.IsZero(), 0, float64(oldestDrift.Unix())))
	r.setReady(bestBefore, metav1.ConditionTrue, "Reconciled", "")
	if err := r.patchStatus(ctx, stored, bestBefore); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: nextStale}, nil
}

func (r *BestBeforeReconciler) setReady(bestBefore *bestbeforev1alpha1.BestBefore, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&bestBefore.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: bestBefore.Generation,
	})
}

func (r *BestBeforeReconciler) patchStatus(ctx context.Context, stored, bestBefore *bestbeforev1alpha1.BestBefore) error {
	if equality.Semantic.DeepEqual(stored.Status, bestBefore.Status) {
		return nil
	}
	if err := r.Status().Patch(ctx, bestBefore, client.MergeFrom(stored)); err != nil {
		return client.IgnoreNotFound(fmt.Errorf("patching bestbefore status: %w", err))
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BestBeforeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&bestbeforev1alpha1.BestBefore{}).
		// NodeClaims coming, going or being drifted change every policy's counts; Karpenter's
		// status writes don't.
		Watches(&karpv1.NodeClaim{}, handler.EnqueueRequestsFromMapFunc(r.allBestBefores),
			builder.WithPredicates(metadataChanged())).
		Named("bestbefore").
		Complete(r)
}

func (r *BestBeforeReconciler) allBestBefores(ctx context.Context, _ client.Object) []reconcile.Request {
	list := &bestbeforev1alpha1.BestBeforeList{}
	if err := r.List(ctx, list); err != nil {
		logf.FromContext(ctx).Error(err, "listing bestbefores")
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
	}
	return requests
}
