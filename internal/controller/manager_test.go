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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	bestbeforev1alpha1 "github.com/rjbez17/k-belt/api/v1alpha1"
)

var _ = Describe("Controllers in a manager", func() {
	AfterEach(cleanUp)

	It("drifts NodeClaims for several policies and restores them when a policy is deleted", func() {
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:                 scheme.Scheme,
			Metrics:                metricsserver.Options{BindAddress: "0"},
			HealthProbeBindAddress: "0",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect((&BestBeforeReconciler{Client: mgr.GetClient(), Clock: clock.RealClock{}}).SetupWithManager(mgr)).To(Succeed())
		Expect((&NodeClaimReconciler{Client: mgr.GetClient(), Clock: clock.RealClock{}}).SetupWithManager(mgr)).To(Succeed())
		mgrCtx, stop := context.WithCancel(ctx)
		DeferCleanup(stop)
		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()

		// Real clock: short maxAges so requeue-after, not a spec change, triggers the drift.
		nodePool := nodePoolWithHash()
		teamA := nodeClaimFor(nodePool, map[string]string{teamLabel: "a"})
		teamB := nodeClaimFor(nodePool, map[string]string{teamLabel: "b"})
		untouched := nodeClaimFor(nodePool, map[string]string{teamLabel: "c"})
		policyA := withMaxAge(bestBeforeFor(map[string]string{teamLabel: "a"}), 2*time.Second)
		policyB := withMaxAge(bestBeforeFor(map[string]string{teamLabel: "b"}), 4*time.Second)
		// Plain creates: ExpectApplied's follow-up status update races with the running controllers.
		for _, obj := range []client.Object{nodePool, teamA, teamB, untouched, policyA, policyB} {
			Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		}

		annotationsOf := func(nodeClaim *karpv1.NodeClaim) func() map[string]string {
			return func() map[string]string { return expectNodeClaim(nodeClaim).Annotations }
		}
		Eventually(annotationsOf(teamA)).WithTimeout(15 * time.Second).Should(HaveKeyWithValue(bestbeforev1alpha1.PolicyAnnotationKey, policyA.Name))
		Eventually(annotationsOf(teamB)).WithTimeout(15 * time.Second).Should(HaveKeyWithValue(bestbeforev1alpha1.PolicyAnnotationKey, policyB.Name))
		Consistently(annotationsOf(untouched)).WithTimeout(2 * time.Second).ShouldNot(HaveKey(bestbeforev1alpha1.PolicyAnnotationKey))
		Eventually(func() int32 { return ExpectExists(ctx, k8sClient, policyA).Status.DriftedNodeClaims }).
			WithTimeout(10 * time.Second).Should(BeEquivalentTo(1))

		Expect(k8sClient.Delete(ctx, policyA)).To(Succeed())
		Eventually(annotationsOf(teamA)).WithTimeout(10 * time.Second).Should(HaveKeyWithValue(karpv1.NodePoolHashAnnotationKey, nodePool.Hash()))
		Expect(expectNodeClaim(teamB).Annotations).To(HaveKeyWithValue(karpv1.NodePoolHashAnnotationKey, bestbeforev1alpha1.DriftedHashValue))
	})
})
