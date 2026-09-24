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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	clocktesting "k8s.io/utils/clock/testing"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	bestbeforev1alpha1 "github.com/rjbez17/k-belt/api/v1alpha1"
)

var _ = Describe("maxConcurrent", func() {
	var (
		clk        *clocktesting.FakeClock
		reconciler *NodeClaimReconciler
		nodePool   *karpv1.NodePool
	)

	BeforeEach(func() {
		clk = clocktesting.NewFakeClock(time.Now())
		reconciler = &NodeClaimReconciler{Client: k8sClient, Clock: clk, ResyncPeriod: 24 * time.Hour}
		nodePool = nodePoolWithHash()
		ExpectApplied(ctx, k8sClient, nodePool)
	})

	AfterEach(cleanUp)

	// olderNodeClaims creates NodeClaims whose ages differ, oldest first in the returned slice.
	// The API server stamps creationTimestamp, so age is faked by moving the clock instead.
	staleNodeClaims := func(count int) []*karpv1.NodeClaim {
		claims := make([]*karpv1.NodeClaim, count)
		for i := range claims {
			claims[i] = nodeClaimFor(nodePool, nil)
			ExpectApplied(ctx, k8sClient, claims[i])
		}
		clk.Step(2 * testMaxAge)
		return claims
	}

	// splitBySlot reconciles every claim and returns the one that took the single slot, plus one
	// still waiting. Which claim wins is decided by age and then name, not creation order here.
	splitBySlot := func(claims []*karpv1.NodeClaim) (drifted, waiting *karpv1.NodeClaim) {
		GinkgoHelper()
		for _, claim := range claims {
			ExpectReconciled(ctx, reconciler, requestFor(claim))
		}
		for _, claim := range claims {
			if driftedByBestBefore(expectNodeClaim(claim)) {
				drifted = claim
			} else {
				waiting = claim
			}
		}
		Expect(drifted).ToNot(BeNil())
		Expect(waiting).ToNot(BeNil())
		return drifted, waiting
	}

	reconcileAll := func(claims []*karpv1.NodeClaim) int {
		drifted := 0
		for _, claim := range claims {
			ExpectReconciled(ctx, reconciler, requestFor(claim))
			if driftedByBestBefore(expectNodeClaim(claim)) {
				drifted++
			}
		}
		return drifted
	}

	It("drifts everything when maxConcurrent is unset", func() {
		claims := staleNodeClaims(3)
		ExpectApplied(ctx, k8sClient, bestBeforeFor(nil))

		Expect(reconcileAll(claims)).To(Equal(3))
	})

	It("drifts no more than a count", func() {
		claims := staleNodeClaims(4)
		policy := bestBeforeFor(nil)
		policy.Spec.MaxConcurrent = new("2")
		ExpectApplied(ctx, k8sClient, policy)

		Expect(reconcileAll(claims)).To(Equal(2))
	})

	It("drifts no more than a percentage of the matched NodeClaims", func() {
		claims := staleNodeClaims(4)
		policy := bestBeforeFor(nil)
		policy.Spec.MaxConcurrent = new("50%")
		ExpectApplied(ctx, k8sClient, policy)

		Expect(reconcileAll(claims)).To(Equal(2))
	})

	It("rounds percentages up so small pools still move", func() {
		claims := staleNodeClaims(4)
		policy := bestBeforeFor(nil)
		policy.Spec.MaxConcurrent = new("10%") // 0.4 of 4
		ExpectApplied(ctx, k8sClient, policy)

		Expect(reconcileAll(claims)).To(Equal(1))
	})

	It("asks to look again while it waits for a slot", func() {
		claims := staleNodeClaims(2)
		policy := bestBeforeFor(nil)
		policy.Spec.MaxConcurrent = new("1")
		ExpectApplied(ctx, k8sClient, policy)
		_, waiting := splitBySlot(claims)

		result := ExpectReconciled(ctx, reconciler, requestFor(waiting))

		Expect(result.RequeueAfter).To(Equal(concurrencyRetryInterval))
		Expect(driftedByBestBefore(expectNodeClaim(waiting))).To(BeFalse())
	})

	It("frees the slot once Karpenter has replaced the NodeClaim", func() {
		claims := staleNodeClaims(2)
		policy := bestBeforeFor(nil)
		policy.Spec.MaxConcurrent = new("1")
		ExpectApplied(ctx, k8sClient, policy)
		drifted, waiting := splitBySlot(claims)

		// The replacement finishing is the drifted NodeClaim going away.
		ExpectDeleted(ctx, k8sClient, drifted)

		ExpectReconciled(ctx, reconciler, requestFor(waiting))

		Expect(driftedByBestBefore(expectNodeClaim(waiting))).To(BeTrue())
	})

	It("counts a drifted NodeClaim that is still draining against the limit", func() {
		claims := staleNodeClaims(2)
		policy := bestBeforeFor(nil)
		policy.Spec.MaxConcurrent = new("1")
		ExpectApplied(ctx, k8sClient, policy)
		drifted, waiting := splitBySlot(claims)
		ExpectDeletionTimestampSet(ctx, k8sClient, drifted)

		ExpectReconciled(ctx, reconciler, requestFor(waiting))

		Expect(driftedByBestBefore(expectNodeClaim(waiting))).To(BeFalse())
	})

	It("takes the oldest NodeClaim first", func() {
		// creationTimestamp has one-second granularity, so space the two creations apart rather
		// than relying on ordering within the same second.
		oldest := nodeClaimFor(nodePool, nil)
		ExpectApplied(ctx, k8sClient, oldest)
		time.Sleep(1100 * time.Millisecond)
		newest := nodeClaimFor(nodePool, nil)
		ExpectApplied(ctx, k8sClient, newest)
		Expect(expectNodeClaim(newest).CreationTimestamp.Time).To(BeTemporally(">", expectNodeClaim(oldest).CreationTimestamp.Time))

		policy := bestBeforeFor(nil)
		policy.Spec.MaxConcurrent = new("1")
		ExpectApplied(ctx, k8sClient, policy)
		clk.Step(2 * testMaxAge)

		// Reconciled newest first, so age has to be what decides it.
		ExpectReconciled(ctx, reconciler, requestFor(newest))
		ExpectReconciled(ctx, reconciler, requestFor(oldest))

		Expect(driftedByBestBefore(expectNodeClaim(oldest))).To(BeTrue())
		Expect(driftedByBestBefore(expectNodeClaim(newest))).To(BeFalse())
	})

	It("doesn't let another policy's drifted NodeClaim take a slot", func() {
		older := nodeClaimFor(nodePool, nil)
		ExpectApplied(ctx, k8sClient, older)
		time.Sleep(1100 * time.Millisecond)
		newer := nodeClaimFor(nodePool, nil)
		ExpectApplied(ctx, k8sClient, newer)

		// A second policy, matching the same NodeClaims and with the longer maxAge, marks the
		// older one and keeps owning it.
		theirs := withMaxAge(bestBeforeFor(nil), 2*testMaxAge)
		ExpectApplied(ctx, k8sClient, theirs)
		clk.Step(3 * testMaxAge)
		ExpectReconciled(ctx, reconciler, requestFor(older))
		Expect(driftedBy(expectNodeClaim(older), theirs.Name)).To(BeTrue())

		// Ours has the shorter maxAge, so it owns whatever is still unmarked. Its single slot must
		// go to its own candidate, not to the NodeClaim the other policy is already replacing.
		mine := bestBeforeFor(nil)
		mine.Spec.MaxConcurrent = new("1")
		ExpectApplied(ctx, k8sClient, mine)

		ExpectReconciled(ctx, reconciler, requestFor(newer))

		Expect(driftedBy(expectNodeClaim(newer), mine.Name)).To(BeTrue())
	})

	It("skips NodeClaims without Karpenter's hash annotations when filling slots", func() {
		unmarkable := nodeClaimFor(nodePool, nil)
		delete(unmarkable.Annotations, karpv1.NodePoolHashAnnotationKey)
		ExpectApplied(ctx, k8sClient, unmarkable)
		time.Sleep(1100 * time.Millisecond) // older than the one that can be marked
		markable := nodeClaimFor(nodePool, nil)
		ExpectApplied(ctx, k8sClient, markable)
		policy := bestBeforeFor(nil)
		policy.Spec.MaxConcurrent = new("1")
		ExpectApplied(ctx, k8sClient, policy)
		clk.Step(2 * testMaxAge)

		// The oldest NodeClaim can never be marked, so it must not hold the slot.
		ExpectReconciled(ctx, reconciler, requestFor(markable))

		Expect(driftedByBestBefore(expectNodeClaim(markable))).To(BeTrue())
	})

	It("ignores paused NodeClaims when counting slots", func() {
		claims := staleNodeClaims(2)
		patchAnnotations(claims[0], map[string]string{bestbeforev1alpha1.PausedAnnotationKey: annotationSet})
		policy := bestBeforeFor(nil)
		policy.Spec.MaxConcurrent = new("1")
		ExpectApplied(ctx, k8sClient, policy)

		// The paused one is skipped, and it must not consume the single slot either.
		Expect(reconcileAll(claims)).To(Equal(1))
		Expect(driftedByBestBefore(expectNodeClaim(claims[1]))).To(BeTrue())
	})
})
