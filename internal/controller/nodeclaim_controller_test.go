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
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	bestbeforev1alpha1 "github.com/rjbez17/k-belt/api/v1alpha1"
)

var _ = Describe("NodeClaim controller", func() {
	var (
		clk        *clocktesting.FakeClock
		reconciler *NodeClaimReconciler
		nodePool   *karpv1.NodePool
	)

	BeforeEach(func() {
		clk = clocktesting.NewFakeClock(time.Now())
		// Long resync so specs asserting exact deadlines aren't capped; the resync specs set their own.
		reconciler = &NodeClaimReconciler{Client: k8sClient, Clock: clk, ResyncPeriod: 24 * time.Hour}
		nodePool = nodePoolWithHash()
		ExpectApplied(ctx, k8sClient, nodePool)
	})

	AfterEach(cleanUp)

	Context("drift", func() {
		It("leaves NodeClaims younger than maxAge alone", func() {
			nodeClaim := nodeClaimFor(nodePool, nil)
			ExpectApplied(ctx, k8sClient, nodeClaim, bestBeforeFor(nil))

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(expectNodeClaim(nodeClaim).Annotations[karpv1.NodePoolHashAnnotationKey]).To(Equal(nodePool.Hash()))
		})

		It("drifts NodeClaims older than maxAge and saves the hash it replaces", func() {
			nodeClaim := nodeClaimFor(nodePool, nil)
			bestBefore := bestBeforeFor(nil)
			ExpectApplied(ctx, k8sClient, nodeClaim, bestBefore)
			clk.Step(testMaxAge + time.Minute)

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			annotations := expectNodeClaim(nodeClaim).Annotations
			Expect(annotations).To(HaveKeyWithValue(karpv1.NodePoolHashAnnotationKey, bestbeforev1alpha1.DriftedHashValue))
			Expect(annotations).To(HaveKeyWithValue(bestbeforev1alpha1.PolicyAnnotationKey, bestBefore.Name))
			Expect(annotations).To(HaveKeyWithValue(bestbeforev1alpha1.OriginalHashAnnotationKey, nodePool.Hash()))
			Expect(annotations).To(HaveKeyWithValue(bestbeforev1alpha1.OriginalHashVersionAnnotationKey, karpv1.NodePoolHashVersion))
			Expect(annotations).To(HaveKeyWithValue(bestbeforev1alpha1.DriftedAtAnnotationKey, clk.Now().UTC().Format(time.RFC3339)))

			// Reconciling again is a no-op.
			resourceVersion := expectNodeClaim(nodeClaim).ResourceVersion
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))
			Expect(expectNodeClaim(nodeClaim).ResourceVersion).To(Equal(resourceVersion))
		})

		It("ignores NodeClaims the selector doesn't match", func() {
			nodeClaim := nodeClaimFor(nodePool, map[string]string{teamLabel: "b"})
			ExpectApplied(ctx, k8sClient, nodeClaim, bestBeforeFor(map[string]string{teamLabel: "a"}))
			clk.Step(2 * testMaxAge)

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(expectNodeClaim(nodeClaim).Annotations).ToNot(HaveKey(bestbeforev1alpha1.PolicyAnnotationKey))
		})

		It("skips NodeClaims without Karpenter's hash annotations", func() {
			for _, missing := range []string{karpv1.NodePoolHashAnnotationKey, karpv1.NodePoolHashVersionAnnotationKey} {
				nodeClaim := nodeClaimFor(nodePool, nil)
				delete(nodeClaim.Annotations, missing)
				ExpectApplied(ctx, k8sClient, nodeClaim)
			}
			ExpectApplied(ctx, k8sClient, bestBeforeFor(nil))
			clk.Step(2 * testMaxAge)

			nodeClaims := &karpv1.NodeClaimList{}
			Expect(k8sClient.List(ctx, nodeClaims)).To(Succeed())
			for i := range nodeClaims.Items {
				ExpectReconciled(ctx, reconciler, requestFor(&nodeClaims.Items[i]))
				Expect(expectNodeClaim(&nodeClaims.Items[i]).Annotations).ToNot(HaveKey(bestbeforev1alpha1.PolicyAnnotationKey))
			}
		})

		It("skips NodeClaims that are already deleting", func() {
			nodeClaim := nodeClaimFor(nodePool, nil)
			ExpectApplied(ctx, k8sClient, nodeClaim, bestBeforeFor(nil))
			ExpectDeletionTimestampSet(ctx, k8sClient, nodeClaim)
			clk.Step(2 * testMaxAge)

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(expectNodeClaim(nodeClaim).Annotations[karpv1.NodePoolHashAnnotationKey]).To(Equal(nodePool.Hash()))
		})

		It("drifts again and saves the new hash if Karpenter rewrites it", func() {
			nodeClaim := nodeClaimFor(nodePool, nil)
			ExpectApplied(ctx, k8sClient, nodeClaim, bestBeforeFor(nil))
			clk.Step(2 * testMaxAge)
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			// Karpenter's hash-version migration resets NodeClaims it doesn't yet consider drifted.
			patchAnnotations(nodeClaim, map[string]string{
				karpv1.NodePoolHashAnnotationKey:        "rehashed",
				karpv1.NodePoolHashVersionAnnotationKey: newHashVersion,
			})
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			annotations := expectNodeClaim(nodeClaim).Annotations
			Expect(annotations).To(HaveKeyWithValue(karpv1.NodePoolHashAnnotationKey, bestbeforev1alpha1.DriftedHashValue))
			Expect(annotations).To(HaveKeyWithValue(bestbeforev1alpha1.OriginalHashAnnotationKey, "rehashed"))
			Expect(annotations).To(HaveKeyWithValue(bestbeforev1alpha1.OriginalHashVersionAnnotationKey, newHashVersion))
		})

		It("isolates a NodeClaim that can't be patched from the others", func() {
			failing, healthy := nodeClaimFor(nodePool, nil), nodeClaimFor(nodePool, nil)
			ExpectApplied(ctx, k8sClient, failing, healthy, bestBeforeFor(nil))
			clk.Step(2 * testMaxAge)
			reconciler.Client = conflictingClient(failing.Name)

			_, err := reconciler.Reconcile(ctx, requestFor(failing))
			Expect(err).To(HaveOccurred())
			ExpectReconciled(ctx, reconciler, requestFor(healthy))

			Expect(expectNodeClaim(failing).Annotations).ToNot(HaveKey(bestbeforev1alpha1.PolicyAnnotationKey))
			Expect(expectNodeClaim(healthy).Annotations).To(HaveKey(bestbeforev1alpha1.PolicyAnnotationKey))
		})
	})

	Context("resync period", func() {
		It("caps a distant deadline at the resync period, with jitter", func() {
			reconciler.ResyncPeriod = time.Minute
			nodeClaim := nodeClaimFor(nodePool, nil)
			ExpectApplied(ctx, k8sClient, nodeClaim, withMaxAge(bestBeforeFor(nil), 10*time.Hour))

			result := ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(result.RequeueAfter).To(BeNumerically("~", time.Minute, 6*time.Second))
		})

		It("keeps a deadline that lands before the resync period", func() {
			nodeClaim := nodeClaimFor(nodePool, nil)
			ExpectApplied(ctx, k8sClient, nodeClaim, bestBeforeFor(nil))

			result := ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			// creationTimestamp comes from the API server, so allow for skew against the fake clock.
			Expect(result.RequeueAfter).To(BeNumerically("~", testMaxAge, 5*time.Second))
		})

		It("still requeues after restoring, so a raised maxAge is applied later", func() {
			reconciler.ResyncPeriod = time.Minute
			nodeClaim := nodeClaimFor(nodePool, nil)
			bestBefore := bestBeforeFor(nil)
			ExpectApplied(ctx, k8sClient, nodeClaim, bestBefore)
			clk.Step(2 * testMaxAge)
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))
			ExpectApplied(ctx, k8sClient, withMaxAge(bestBefore, 10*testMaxAge))

			result := ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(expectNodeClaim(nodeClaim).Annotations[karpv1.NodePoolHashAnnotationKey]).To(Equal(nodePool.Hash()))
			Expect(result.RequeueAfter).To(BeNumerically("~", time.Minute, 6*time.Second))
		})

		It("keeps paused NodeClaims on the resync cadence", func() {
			reconciler.ResyncPeriod = time.Minute
			nodeClaim := nodeClaimFor(nodePool, nil)
			ExpectApplied(ctx, k8sClient, nodeClaim, bestBeforeFor(nil))
			patchAnnotations(nodeClaim, map[string]string{bestbeforev1alpha1.PausedAnnotationKey: annotationSet})

			result := ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(result.RequeueAfter).To(BeNumerically("~", time.Minute, 6*time.Second))
		})
	})

	Context("overlapping policies", func() {
		It("gives a NodeClaim to the stale policy with the shortest maxAge", func() {
			nodeClaim := nodeClaimFor(nodePool, nil)
			short, long := bestBeforeFor(nil), withMaxAge(bestBeforeFor(nil), 2*testMaxAge)
			ExpectApplied(ctx, k8sClient, nodeClaim, short, long)
			clk.Step(3 * testMaxAge)

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(expectNodeClaim(nodeClaim).Annotations).To(HaveKeyWithValue(bestbeforev1alpha1.PolicyAnnotationKey, short.Name))
		})

		It("keeps the current owner when another policy also goes stale", func() {
			nodeClaim := nodeClaimFor(nodePool, nil)
			first := withMaxAge(bestBeforeFor(nil), 2*testMaxAge)
			ExpectApplied(ctx, k8sClient, nodeClaim, first)
			clk.Step(3 * testMaxAge)
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			ExpectApplied(ctx, k8sClient, bestBeforeFor(nil))
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(expectNodeClaim(nodeClaim).Annotations).To(HaveKeyWithValue(bestbeforev1alpha1.PolicyAnnotationKey, first.Name))
		})

		It("hands a drifted NodeClaim to another stale policy when its owner is deleted", func() {
			nodeClaim := nodeClaimFor(nodePool, nil)
			owner, other := bestBeforeFor(nil), withMaxAge(bestBeforeFor(nil), 2*testMaxAge)
			ExpectApplied(ctx, k8sClient, nodeClaim, owner, other)
			clk.Step(3 * testMaxAge)
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(k8sClient.Delete(ctx, owner)).To(Succeed())
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			annotations := expectNodeClaim(nodeClaim).Annotations
			Expect(annotations).To(HaveKeyWithValue(bestbeforev1alpha1.PolicyAnnotationKey, other.Name))
			Expect(annotations).To(HaveKeyWithValue(karpv1.NodePoolHashAnnotationKey, bestbeforev1alpha1.DriftedHashValue))
			Expect(annotations).To(HaveKeyWithValue(bestbeforev1alpha1.OriginalHashAnnotationKey, nodePool.Hash()))
		})
	})

	Context("taintDriftedNodes", func() {
		var (
			node       *corev1.Node
			nodeClaim  *karpv1.NodeClaim
			otherTaint = corev1.Taint{Key: "example.com/other", Effect: corev1.TaintEffectNoSchedule}
		)
		hasDriftedTaint := func() bool {
			latest := ExpectExists(ctx, k8sClient, node)
			return lo.ContainsBy(latest.Spec.Taints, func(t corev1.Taint) bool { return t.Key == bestbeforev1alpha1.DriftedTaintKey })
		}

		BeforeEach(func() {
			node = test.Node(test.NodeOptions{Taints: []corev1.Taint{otherTaint}})
			nodeClaim = nodeClaimFor(nodePool, nil)
			nodeClaim.Status.NodeName = node.Name
			ExpectApplied(ctx, k8sClient, node, nodeClaim)
		})

		AfterEach(func() { ExpectDeleted(ctx, k8sClient, node) })

		It("taints the node of a drifted NodeClaim and keeps other taints", func() {
			bestBefore := bestBeforeFor(nil)
			bestBefore.Spec.TaintDriftedNodes = true
			ExpectApplied(ctx, k8sClient, bestBefore)
			clk.Step(2 * testMaxAge)

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(hasDriftedTaint()).To(BeTrue())
			Expect(ExpectExists(ctx, k8sClient, node).Spec.Taints).To(ContainElement(HaveField("Key", otherTaint.Key)))
			Expect(ExpectExists(ctx, k8sClient, node).Spec.Taints).To(ContainElement(And(
				HaveField("Key", bestbeforev1alpha1.DriftedTaintKey), HaveField("Effect", corev1.TaintEffectPreferNoSchedule))))
		})

		It("doesn't taint nodes unless the policy asks for it", func() {
			ExpectApplied(ctx, k8sClient, bestBeforeFor(nil))
			clk.Step(2 * testMaxAge)

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(hasDriftedTaint()).To(BeFalse())
		})

		It("removes the taint when the drift is restored", func() {
			bestBefore := bestBeforeFor(nil)
			bestBefore.Spec.TaintDriftedNodes = true
			ExpectApplied(ctx, k8sClient, bestBefore)
			clk.Step(2 * testMaxAge)
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))
			Expect(hasDriftedTaint()).To(BeTrue())

			Expect(k8sClient.Delete(ctx, bestBefore)).To(Succeed())
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(hasDriftedTaint()).To(BeFalse())
			Expect(ExpectExists(ctx, k8sClient, node).Spec.Taints).To(ContainElement(HaveField("Key", otherTaint.Key)))
		})

		It("removes the taint when the policy turns tainting off", func() {
			bestBefore := bestBeforeFor(nil)
			bestBefore.Spec.TaintDriftedNodes = true
			ExpectApplied(ctx, k8sClient, bestBefore)
			clk.Step(2 * testMaxAge)
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			bestBefore = ExpectExists(ctx, k8sClient, bestBefore)
			bestBefore.Spec.TaintDriftedNodes = false
			ExpectApplied(ctx, k8sClient, bestBefore)
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(hasDriftedTaint()).To(BeFalse())
			Expect(expectNodeClaim(nodeClaim).Annotations[karpv1.NodePoolHashAnnotationKey]).To(Equal(bestbeforev1alpha1.DriftedHashValue))
		})

		It("taints the node once the NodeClaim registers it", func() {
			unregistered := nodeClaimFor(nodePool, nil)
			bestBefore := bestBeforeFor(nil)
			bestBefore.Spec.TaintDriftedNodes = true
			ExpectApplied(ctx, k8sClient, unregistered, bestBefore)
			clk.Step(2 * testMaxAge)
			ExpectReconciled(ctx, reconciler, requestFor(unregistered))

			laterNode := test.Node()
			ExpectApplied(ctx, k8sClient, laterNode)
			DeferCleanup(func() { ExpectDeleted(ctx, k8sClient, laterNode) })
			unregistered = expectNodeClaim(unregistered)
			unregistered.Status.NodeName = laterNode.Name
			Expect(k8sClient.Status().Update(ctx, unregistered)).To(Succeed())
			ExpectReconciled(ctx, reconciler, requestFor(unregistered))

			Expect(ExpectExists(ctx, k8sClient, laterNode).Spec.Taints).To(ContainElement(HaveField("Key", bestbeforev1alpha1.DriftedTaintKey)))
		})
	})

	Context("pause and revert annotations", func() {
		var (
			node       *corev1.Node
			nodeClaim  *karpv1.NodeClaim
			bestBefore *bestbeforev1alpha1.BestBefore
		)
		hasDriftedTaint := func() bool {
			return lo.ContainsBy(ExpectExists(ctx, k8sClient, node).Spec.Taints, func(t corev1.Taint) bool { return t.Key == bestbeforev1alpha1.DriftedTaintKey })
		}
		driftNodeClaim := func() {
			GinkgoHelper()
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))
			Expect(expectNodeClaim(nodeClaim).Annotations[karpv1.NodePoolHashAnnotationKey]).To(Equal(bestbeforev1alpha1.DriftedHashValue))
			Expect(hasDriftedTaint()).To(BeTrue())
		}

		BeforeEach(func() {
			node = test.Node()
			nodeClaim = nodeClaimFor(nodePool, nil)
			nodeClaim.Status.NodeName = node.Name
			bestBefore = bestBeforeFor(nil)
			bestBefore.Spec.TaintDriftedNodes = true
			ExpectApplied(ctx, k8sClient, node, nodeClaim, bestBefore)
			clk.Step(2 * testMaxAge)
		})

		AfterEach(func() { ExpectDeleted(ctx, k8sClient, node) })

		It("doesn't drift a stale NodeClaim while it is paused, whatever the annotation's value", func() {
			patchAnnotations(nodeClaim, map[string]string{bestbeforev1alpha1.PausedAnnotationKey: "anything"})

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(expectNodeClaim(nodeClaim).Annotations[karpv1.NodePoolHashAnnotationKey]).To(Equal(nodePool.Hash()))
			Expect(hasDriftedTaint()).To(BeFalse())
		})

		It("counts an empty value as present", func() {
			latest := expectNodeClaim(nodeClaim)
			stored := latest.DeepCopy()
			latest.Annotations[bestbeforev1alpha1.PausedAnnotationKey] = ""
			Expect(k8sClient.Patch(ctx, latest, client.MergeFrom(stored))).To(Succeed())

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(expectNodeClaim(nodeClaim).Annotations[karpv1.NodePoolHashAnnotationKey]).To(Equal(nodePool.Hash()))
		})

		It("leaves a paused drifted NodeClaim and its taint untouched when its policy goes away", func() {
			driftNodeClaim()
			patchAnnotations(nodeClaim, map[string]string{bestbeforev1alpha1.PausedAnnotationKey: annotationSet})
			Expect(k8sClient.Delete(ctx, bestBefore)).To(Succeed())

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(expectNodeClaim(nodeClaim).Annotations[karpv1.NodePoolHashAnnotationKey]).To(Equal(bestbeforev1alpha1.DriftedHashValue))
			Expect(hasDriftedTaint()).To(BeTrue())
		})

		It("resumes once the pause annotation is removed", func() {
			patchAnnotations(nodeClaim, map[string]string{bestbeforev1alpha1.PausedAnnotationKey: annotationSet})
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))
			patchAnnotations(nodeClaim, map[string]string{bestbeforev1alpha1.PausedAnnotationKey: ""})

			driftNodeClaim()
		})

		It("restores the original hash, removes the taint and keeps the NodeClaim reverted", func() {
			driftNodeClaim()
			patchAnnotations(nodeClaim, map[string]string{bestbeforev1alpha1.RevertAnnotationKey: annotationSet})

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			annotations := expectNodeClaim(nodeClaim).Annotations
			Expect(annotations).To(HaveKeyWithValue(karpv1.NodePoolHashAnnotationKey, nodePool.Hash()))
			Expect(annotations).To(HaveKey(bestbeforev1alpha1.RevertAnnotationKey))
			Expect(annotations).ToNot(HaveKey(bestbeforev1alpha1.PolicyAnnotationKey))
			Expect(annotations).ToNot(HaveKey(bestbeforev1alpha1.OriginalHashAnnotationKey))
			Expect(hasDriftedTaint()).To(BeFalse())
		})

		It("takes precedence over pause", func() {
			driftNodeClaim()
			patchAnnotations(nodeClaim, map[string]string{bestbeforev1alpha1.PausedAnnotationKey: annotationSet, bestbeforev1alpha1.RevertAnnotationKey: annotationSet})

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(expectNodeClaim(nodeClaim).Annotations[karpv1.NodePoolHashAnnotationKey]).To(Equal(nodePool.Hash()))
		})

		It("doesn't touch a NodeClaim k-belt never drifted", func() {
			patchAnnotations(nodeClaim, map[string]string{bestbeforev1alpha1.RevertAnnotationKey: annotationSet})
			resourceVersion := expectNodeClaim(nodeClaim).ResourceVersion

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(expectNodeClaim(nodeClaim).ResourceVersion).To(Equal(resourceVersion))
		})

		It("leaves a NodeClaim Karpenter is already disrupting", func() {
			driftNodeClaim()
			disrupting := expectNodeClaim(nodeClaim)
			disrupting.StatusConditions().SetTrueWithReason(karpv1.ConditionTypeDisruptionReason, "Drifted", "Drifted")
			Expect(k8sClient.Status().Update(ctx, disrupting)).To(Succeed())
			patchAnnotations(nodeClaim, map[string]string{bestbeforev1alpha1.RevertAnnotationKey: annotationSet})

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(expectNodeClaim(nodeClaim).Annotations[karpv1.NodePoolHashAnnotationKey]).To(Equal(bestbeforev1alpha1.DriftedHashValue))
		})

		It("drifts the NodeClaim again once the revert annotation is removed", func() {
			driftNodeClaim()
			patchAnnotations(nodeClaim, map[string]string{bestbeforev1alpha1.RevertAnnotationKey: annotationSet})
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))
			patchAnnotations(nodeClaim, map[string]string{bestbeforev1alpha1.RevertAnnotationKey: ""})

			driftNodeClaim()
		})
	})

	Context("restore", func() {
		var (
			nodeClaim  *karpv1.NodeClaim
			bestBefore = bestBeforeFor(nil)
		)

		BeforeEach(func() {
			nodeClaim = nodeClaimFor(nodePool, map[string]string{teamLabel: "a"})
			bestBefore = bestBeforeFor(map[string]string{teamLabel: "a"})
			ExpectApplied(ctx, k8sClient, nodeClaim, bestBefore)
			clk.Step(2 * testMaxAge)
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))
			Expect(expectNodeClaim(nodeClaim).Annotations[karpv1.NodePoolHashAnnotationKey]).To(Equal(bestbeforev1alpha1.DriftedHashValue))
		})

		expectRestored := func(hash string) {
			GinkgoHelper()
			annotations := expectNodeClaim(nodeClaim).Annotations
			Expect(annotations).To(HaveKeyWithValue(karpv1.NodePoolHashAnnotationKey, hash))
			Expect(annotations).ToNot(HaveKey(bestbeforev1alpha1.PolicyAnnotationKey))
			Expect(annotations).ToNot(HaveKey(bestbeforev1alpha1.OriginalHashAnnotationKey))
			Expect(annotations).ToNot(HaveKey(bestbeforev1alpha1.OriginalHashVersionAnnotationKey))
			Expect(annotations).ToNot(HaveKey(bestbeforev1alpha1.DriftedAtAnnotationKey))
		}

		It("restores the original hash when the policy is deleted", func() {
			Expect(k8sClient.Delete(ctx, bestBefore)).To(Succeed())
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))
			expectRestored(nodePool.Hash())
		})

		It("restores the original hash when maxAge is raised past the NodeClaim's age", func() {
			ExpectApplied(ctx, k8sClient, withMaxAge(bestBefore, 10*testMaxAge))
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))
			expectRestored(nodePool.Hash())
		})

		It("restores the original hash when the selector stops matching", func() {
			bestBefore.Spec.NodeClaimSelector.MatchLabels = map[string]string{teamLabel: "b"}
			ExpectApplied(ctx, k8sClient, bestBefore)
			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))
			expectRestored(nodePool.Hash())
		})

		It("leaves NodeClaims Karpenter is already disrupting", func() {
			disrupting := expectNodeClaim(nodeClaim)
			disrupting.StatusConditions().SetTrueWithReason(karpv1.ConditionTypeDisruptionReason, "Drifted", "Drifted")
			Expect(k8sClient.Status().Update(ctx, disrupting)).To(Succeed())
			Expect(k8sClient.Delete(ctx, bestBefore)).To(Succeed())

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(expectNodeClaim(nodeClaim).Annotations[karpv1.NodePoolHashAnnotationKey]).To(Equal(bestbeforev1alpha1.DriftedHashValue))
		})

		It("restores the NodePool's current hash if Karpenter changed hash versions since the drift", func() {
			patchAnnotations(nodeClaim, map[string]string{karpv1.NodePoolHashVersionAnnotationKey: newHashVersion})
			stored := nodePool.DeepCopy()
			nodePool.Annotations[karpv1.NodePoolHashAnnotationKey] = "rehashed"
			nodePool.Annotations[karpv1.NodePoolHashVersionAnnotationKey] = newHashVersion
			Expect(k8sClient.Patch(ctx, nodePool, client.MergeFrom(stored))).To(Succeed())
			Expect(k8sClient.Delete(ctx, bestBefore)).To(Succeed())

			ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			expectRestored("rehashed")
		})

		It("waits while Karpenter is mid-way through a hash-version migration", func() {
			patchAnnotations(nodeClaim, map[string]string{karpv1.NodePoolHashVersionAnnotationKey: newHashVersion})
			Expect(k8sClient.Delete(ctx, bestBefore)).To(Succeed())

			result := ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

			Expect(result.RequeueAfter).To(Equal(restoreRetryInterval))
			Expect(expectNodeClaim(nodeClaim).Annotations[karpv1.NodePoolHashAnnotationKey]).To(Equal(bestbeforev1alpha1.DriftedHashValue))
		})
	})
})
