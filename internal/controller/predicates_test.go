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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	bestbeforev1alpha1 "github.com/rjbez17/k-belt/api/v1alpha1"
)

var _ = Describe("Watch predicates", func() {
	update := func(mutate func(*karpv1.NodeClaim)) event.UpdateEvent {
		before := test.NodeClaim(karpv1.NodeClaim{ObjectMeta: metav1.ObjectMeta{
			Labels:      map[string]string{teamLabel: "a"},
			Annotations: map[string]string{karpv1.NodePoolHashAnnotationKey: "123"},
		}})
		after := before.DeepCopy()
		mutate(after)
		return event.UpdateEvent{ObjectOld: before, ObjectNew: after}
	}
	statusOnly := func(nc *karpv1.NodeClaim) { nc.StatusConditions().SetTrue(karpv1.ConditionTypeConsolidatable) }

	DescribeTable("metadataChanged",
		func(e event.UpdateEvent, expected bool) {
			Expect(metadataChanged().Update(e)).To(Equal(expected))
		},
		Entry("ignores status-only updates", update(statusOnly), false),
		Entry("ignores no-op updates", update(func(*karpv1.NodeClaim) {}), false),
		Entry("passes annotation changes", update(func(nc *karpv1.NodeClaim) {
			nc.Annotations[bestbeforev1alpha1.PausedAnnotationKey] = annotationSet
		}), true),
		Entry("passes label changes", update(func(nc *karpv1.NodeClaim) { nc.Labels[teamLabel] = "b" }), true),
		Entry("passes deletions", update(func(nc *karpv1.NodeClaim) {
			nc.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
		}), true),
		Entry("ignores the Node name", update(func(nc *karpv1.NodeClaim) { nc.Status.NodeName = "node-1" }), false),
	)

	DescribeTable("nodeClaimChanged",
		func(e event.UpdateEvent, expected bool) {
			Expect(nodeClaimChanged().Update(e)).To(Equal(expected))
		},
		Entry("ignores unrelated status churn", update(statusOnly), false),
		Entry("passes the Node name being set", update(func(nc *karpv1.NodeClaim) { nc.Status.NodeName = "node-1" }), true),
		Entry("passes Karpenter starting a disruption", update(func(nc *karpv1.NodeClaim) {
			nc.StatusConditions().SetTrueWithReason(karpv1.ConditionTypeDisruptionReason, "Drifted", "Drifted")
		}), true),
		Entry("passes annotation changes", update(func(nc *karpv1.NodeClaim) {
			nc.Annotations[bestbeforev1alpha1.RevertAnnotationKey] = annotationSet
		}), true),
	)

	It("passes creates and deletes", func() {
		for _, p := range []predicate.Predicate{metadataChanged(), nodeClaimChanged()} {
			Expect(p.Create(event.CreateEvent{})).To(BeTrue())
			Expect(p.Delete(event.DeleteEvent{})).To(BeTrue())
		}
	})
})
