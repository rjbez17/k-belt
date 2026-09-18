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
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	clocktesting "k8s.io/utils/clock/testing"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	bestbeforev1alpha1 "github.com/rjbez17/k-belt/api/v1alpha1"
)

var _ = Describe("BestBefore controller", func() {
	var (
		clk        *clocktesting.FakeClock
		reconciler *BestBeforeReconciler
		nodePool   *karpv1.NodePool
	)

	BeforeEach(func() {
		clk = clocktesting.NewFakeClock(time.Now())
		reconciler = &BestBeforeReconciler{Client: k8sClient, Clock: clk}
		nodePool = nodePoolWithHash()
	})

	AfterEach(cleanUp)

	It("rejects maxAge values that aren't a positive duration", func() {
		for _, maxAge := range []string{"0s", "-1h", "banana"} {
			raw := map[string]any{
				"apiVersion": bestbeforev1alpha1.GroupVersion.String(),
				"kind":       "BestBefore",
				"metadata":   map[string]any{"name": bestBeforeFor(nil).Name},
				"spec":       map[string]any{"nodeClaimSelector": map[string]any{}, "maxAge": maxAge},
			}
			Expect(k8sClient.Create(ctx, &unstructured.Unstructured{Object: raw})).ToNot(Succeed(), "maxAge %q should be rejected", maxAge)
		}
		ExpectApplied(ctx, k8sClient, withMaxAge(bestBeforeFor(nil), time.Second))
	})

	It("counts matched, stale and drifted NodeClaims and requeues for the next stale one", func() {
		bestBefore := bestBeforeFor(map[string]string{teamLabel: "a"})
		drifted := nodeClaimFor(nodePool, map[string]string{teamLabel: "a"})
		drifted.Annotations[karpv1.NodePoolHashAnnotationKey] = bestbeforev1alpha1.DriftedHashValue
		drifted.Annotations[bestbeforev1alpha1.PolicyAnnotationKey] = bestBefore.Name
		ExpectApplied(ctx, k8sClient, drifted, nodeClaimFor(nodePool, map[string]string{teamLabel: "a"}),
			nodeClaimFor(nodePool, map[string]string{teamLabel: "b"}), bestBefore)
		clk.Step(2 * testMaxAge)
		young := nodeClaimFor(nodePool, map[string]string{teamLabel: "a"})
		ExpectApplied(ctx, k8sClient, young)

		result := ExpectReconciled(ctx, reconciler, requestFor(bestBefore))

		// young was created at the real time, which the fake clock is now ahead of by 2h.
		Expect(result.RequeueAfter).To(BeZero())
		bestBefore = ExpectExists(ctx, k8sClient, bestBefore)
		Expect(bestBefore.Status.MatchedNodeClaims).To(BeEquivalentTo(3))
		Expect(bestBefore.Status.StaleNodeClaims).To(BeEquivalentTo(3))
		Expect(bestBefore.Status.DriftedNodeClaims).To(BeEquivalentTo(1))
		Expect(meta.IsStatusConditionTrue(bestBefore.Status.Conditions, ConditionTypeReady)).To(BeTrue())
	})

	It("exports per-policy metrics and removes them when the policy is deleted", func() {
		bestBefore := bestBeforeFor(nil)
		oldest, newer := nodeClaimFor(nodePool, nil), nodeClaimFor(nodePool, nil)
		for nodeClaim, driftedAt := range map[*karpv1.NodeClaim]time.Time{oldest: clk.Now().Add(-3 * time.Hour), newer: clk.Now().Add(-time.Hour)} {
			nodeClaim.Annotations[karpv1.NodePoolHashAnnotationKey] = bestbeforev1alpha1.DriftedHashValue
			nodeClaim.Annotations[bestbeforev1alpha1.PolicyAnnotationKey] = bestBefore.Name
			nodeClaim.Annotations[bestbeforev1alpha1.DriftedAtAnnotationKey] = driftedAt.UTC().Format(time.RFC3339)
		}
		ExpectApplied(ctx, k8sClient, oldest, newer, nodeClaimFor(nodePool, nil), bestBefore)
		clk.Step(2 * testMaxAge)

		ExpectReconciled(ctx, reconciler, requestFor(bestBefore))

		Expect(testutil.ToFloat64(matchedNodeClaimsGauge.WithLabelValues(bestBefore.Name))).To(BeEquivalentTo(3))
		Expect(testutil.ToFloat64(staleNodeClaimsGauge.WithLabelValues(bestBefore.Name))).To(BeEquivalentTo(3))
		Expect(testutil.ToFloat64(driftedNodeClaimsGauge.WithLabelValues(bestBefore.Name))).To(BeEquivalentTo(2))
		Expect(testutil.ToFloat64(oldestDriftTimestampGauge.WithLabelValues(bestBefore.Name))).
			To(BeEquivalentTo(clk.Now().Add(-2*testMaxAge - 3*time.Hour).Unix()))

		Expect(k8sClient.Delete(ctx, bestBefore)).To(Succeed())
		ExpectReconciled(ctx, reconciler, requestFor(bestBefore))

		// DeleteLabelValues reports whether the series still existed.
		Expect(driftedNodeClaimsGauge.DeleteLabelValues(bestBefore.Name)).To(BeFalse())
		Expect(oldestDriftTimestampGauge.DeleteLabelValues(bestBefore.Name)).To(BeFalse())
	})

	It("requeues for when a matched NodeClaim will go stale", func() {
		bestBefore := bestBeforeFor(nil)
		ExpectApplied(ctx, k8sClient, nodeClaimFor(nodePool, nil), bestBefore)

		result := ExpectReconciled(ctx, reconciler, requestFor(bestBefore))

		Expect(result.RequeueAfter).To(BeNumerically("~", testMaxAge, 5*time.Second))
		Expect(ExpectExists(ctx, k8sClient, bestBefore).Status.StaleNodeClaims).To(BeZero())
	})

	It("reports an invalid selector", func() {
		bestBefore := bestBeforeFor(nil)
		bestBefore.Spec.NodeClaimSelector.MatchExpressions = []metav1.LabelSelectorRequirement{{Key: teamLabel, Operator: "Bogus"}}
		ExpectApplied(ctx, k8sClient, bestBefore)

		ExpectReconciled(ctx, reconciler, requestFor(bestBefore))

		ready := meta.FindStatusCondition(ExpectExists(ctx, k8sClient, bestBefore).Status.Conditions, ConditionTypeReady)
		Expect(ready).ToNot(BeNil())
		Expect(ready.Reason).To(Equal("InvalidSelector"))
	})

	It("enqueues every BestBefore when a NodeClaim changes", func() {
		first, second := bestBeforeFor(nil), bestBeforeFor(nil)
		ExpectApplied(ctx, k8sClient, first, second)

		Expect(reconciler.allBestBefores(ctx, nodeClaimFor(nodePool, nil))).To(ConsistOf(requestFor(first), requestFor(second)))
	})
})
