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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider/fake"
	nodeclaimdisruption "sigs.k8s.io/karpenter/pkg/controllers/nodeclaim/disruption"
	nodepoolhash "sigs.k8s.io/karpenter/pkg/controllers/nodepool/hash"
	"sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	bestbeforev1alpha1 "github.com/rjbez17/k-belt/api/v1alpha1"
)

// These specs run Karpenter's own controllers against the NodeClaims k-belt modifies. They pin the
// behaviour BestBefore depends on, so a Karpenter upgrade that breaks it fails here.
var _ = Describe("Karpenter drift contract", func() {
	var (
		driftController *nodeclaimdisruption.Controller
		hashController  *nodepoolhash.Controller
		reconciler      *NodeClaimReconciler
		nodePool        *karpv1.NodePool
		nodeClaim       *karpv1.NodeClaim
	)

	// launchedNodeClaim creates a launched NodeClaim for the NodePool, as Karpenter would.
	launchedNodeClaim := func(nodePool *karpv1.NodePool) *karpv1.NodeClaim {
		nodeClaim := nodeClaimFor(nodePool, nil)
		nodeClaim.Annotations[karpv1.NodePoolHashAnnotationKey] = nodePool.Annotations[karpv1.NodePoolHashAnnotationKey]
		nodeClaim.Annotations[karpv1.NodePoolHashVersionAnnotationKey] = nodePool.Annotations[karpv1.NodePoolHashVersionAnnotationKey]
		nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeLaunched)
		ExpectApplied(ctx, k8sClient, nodeClaim)
		return nodeClaim
	}
	drifted := func(nodeClaim *karpv1.NodeClaim) bool {
		GinkgoHelper()
		ExpectObjectReconciled(ctx, k8sClient, driftController, expectNodeClaim(nodeClaim))
		return expectNodeClaim(nodeClaim).StatusConditions().Get(karpv1.ConditionTypeDrifted).IsTrue()
	}

	BeforeEach(func() {
		cloudProvider := fake.NewCloudProvider()
		driftController = nodeclaimdisruption.NewController(clocktesting.NewFakeClock(time.Now()), k8sClient, cloudProvider)
		hashController = nodepoolhash.NewController(k8sClient, cloudProvider)
		reconciler = &NodeClaimReconciler{Client: k8sClient, Clock: clocktesting.NewFakeClock(time.Now().Add(2 * testMaxAge))}

		nodePool = test.NodePool()
		ExpectApplied(ctx, k8sClient, nodePool)
		ExpectObjectReconciled(ctx, k8sClient, hashController, nodePool)
		nodePool = ExpectExists(ctx, k8sClient, nodePool)
		nodeClaim = launchedNodeClaim(nodePool)
	})

	AfterEach(cleanUp)

	It("does not consider a fresh NodeClaim drifted", func() {
		Expect(drifted(nodeClaim)).To(BeFalse())
	})

	It("marks a NodeClaim drifted once BestBefore rewrites its nodepool hash", func() {
		ExpectApplied(ctx, k8sClient, bestBeforeFor(nil))
		ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

		// Karpenter's hash controller must not restore the NodeClaim's hash.
		ExpectObjectReconciled(ctx, k8sClient, hashController, nodePool)
		Expect(drifted(nodeClaim)).To(BeTrue())

		latest := expectNodeClaim(nodeClaim)
		Expect(latest.Annotations[karpv1.NodePoolHashAnnotationKey]).To(Equal(bestbeforev1alpha1.DriftedHashValue))
		Expect(latest.StatusConditions().Get(karpv1.ConditionTypeDrifted).Reason).To(Equal(string(nodeclaimdisruption.NodePoolDrifted)))
	})

	It("keeps the NodeClaim drifted on later Karpenter reconciles", func() {
		ExpectApplied(ctx, k8sClient, bestBeforeFor(nil))
		ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

		for range 3 {
			Expect(drifted(nodeClaim)).To(BeTrue())
		}
	})

	It("clears the drift once BestBefore restores the hash", func() {
		bestBefore := bestBeforeFor(nil)
		ExpectApplied(ctx, k8sClient, bestBefore)
		ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))
		Expect(drifted(nodeClaim)).To(BeTrue())

		Expect(k8sClient.Delete(ctx, bestBefore)).To(Succeed())
		ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

		Expect(drifted(nodeClaim)).To(BeFalse())
	})

	It("clears the drift when a NodeClaim is reverted by annotation", func() {
		ExpectApplied(ctx, k8sClient, bestBeforeFor(nil))
		ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))
		Expect(drifted(nodeClaim)).To(BeTrue())

		patchAnnotations(nodeClaim, map[string]string{bestbeforev1alpha1.RevertAnnotationKey: annotationSet})
		ExpectReconciled(ctx, reconciler, requestFor(nodeClaim))

		Expect(drifted(nodeClaim)).To(BeFalse())
	})

	It("clears the drift when restoring after Karpenter migrated to a new hash version", func() {
		// A NodePool and NodeClaim hashed with an older hash version.
		oldPool := test.NodePool(karpv1.NodePool{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			karpv1.NodePoolHashAnnotationKey:        "old-hash",
			karpv1.NodePoolHashVersionAnnotationKey: "v-old",
		}}})
		ExpectApplied(ctx, k8sClient, oldPool)
		oldNodeClaim := launchedNodeClaim(oldPool)
		bestBefore := bestBeforeFor(nil)
		ExpectApplied(ctx, k8sClient, bestBefore)
		ExpectReconciled(ctx, reconciler, requestFor(oldNodeClaim))
		Expect(drifted(oldNodeClaim)).To(BeTrue())

		// Karpenter migrates: drifted NodeClaims keep k-belt's hash but get the new version.
		ExpectObjectReconciled(ctx, k8sClient, hashController, oldPool)
		Expect(expectNodeClaim(oldNodeClaim).Annotations).To(HaveKeyWithValue(karpv1.NodePoolHashAnnotationKey, bestbeforev1alpha1.DriftedHashValue))
		Expect(expectNodeClaim(oldNodeClaim).Annotations).To(HaveKeyWithValue(karpv1.NodePoolHashVersionAnnotationKey, karpv1.NodePoolHashVersion))

		Expect(k8sClient.Delete(ctx, bestBefore)).To(Succeed())
		ExpectReconciled(ctx, reconciler, requestFor(oldNodeClaim))

		Expect(drifted(oldNodeClaim)).To(BeFalse())
	})
})
