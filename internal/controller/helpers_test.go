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
	"errors"
	"time"

	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	bestbeforev1alpha1 "github.com/rjbez17/k-belt/api/v1alpha1"
)

const (
	// testMaxAge is the maxAge of every BestBefore built by bestBeforeFor.
	testMaxAge = time.Hour
	teamLabel  = "team"
	// newHashVersion stands in for a nodepool-hash-version from a future Karpenter release.
	newHashVersion = "v-new"
	// annotationSet is the value used when a test only needs an annotation to be present.
	annotationSet = "true"
)

// nodePoolWithHash builds a NodePool annotated with its hash, as Karpenter's hash controller leaves it.
func nodePoolWithHash() *karpv1.NodePool {
	nodePool := test.NodePool()
	nodePool.Annotations = lo.Assign(nodePool.Annotations, map[string]string{
		karpv1.NodePoolHashAnnotationKey:        nodePool.Hash(),
		karpv1.NodePoolHashVersionAnnotationKey: karpv1.NodePoolHashVersion,
	})
	return nodePool
}

// nodeClaimFor builds a NodeClaim as Karpenter's provisioner would, stamped with the NodePool's hash.
func nodeClaimFor(nodePool *karpv1.NodePool, labels map[string]string) *karpv1.NodeClaim {
	return test.NodeClaim(karpv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Labels: lo.Assign(map[string]string{karpv1.NodePoolLabelKey: nodePool.Name}, labels),
			Annotations: map[string]string{
				karpv1.NodePoolHashAnnotationKey:        nodePool.Hash(),
				karpv1.NodePoolHashVersionAnnotationKey: karpv1.NodePoolHashVersion,
			},
		},
	})
}

func bestBeforeFor(matchLabels map[string]string) *bestbeforev1alpha1.BestBefore {
	return &bestbeforev1alpha1.BestBefore{
		ObjectMeta: metav1.ObjectMeta{Name: test.RandomName()},
		Spec: bestbeforev1alpha1.BestBeforeSpec{
			NodeClaimSelector: metav1.LabelSelector{MatchLabels: matchLabels},
			MaxAge:            metav1.Duration{Duration: testMaxAge},
		},
	}
}

func withMaxAge(bestBefore *bestbeforev1alpha1.BestBefore, maxAge time.Duration) *bestbeforev1alpha1.BestBefore {
	bestBefore.Spec.MaxAge.Duration = maxAge
	return bestBefore
}

func requestFor(obj client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
}

// patchAnnotations sets (or, for empty values, removes) annotations on a NodeClaim, standing in for
// Karpenter or an operator changing them.
func patchAnnotations(nodeClaim *karpv1.NodeClaim, annotations map[string]string) {
	nodeClaim = expectNodeClaim(nodeClaim)
	stored := nodeClaim.DeepCopy()
	for key, value := range annotations {
		if value == "" {
			delete(nodeClaim.Annotations, key)
			continue
		}
		nodeClaim.Annotations[key] = value
	}
	Expect(k8sClient.Patch(ctx, nodeClaim, client.MergeFrom(stored))).To(Succeed())
}

func expectNodeClaim(nodeClaim *karpv1.NodeClaim) *karpv1.NodeClaim {
	latest := &karpv1.NodeClaim{}
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nodeClaim), latest)).To(Succeed())
	return latest
}

func cleanUp() {
	Expect(k8sClient.DeleteAllOf(ctx, &bestbeforev1alpha1.BestBefore{})).To(Succeed())
	Expect(k8sClient.DeleteAllOf(ctx, &karpv1.NodeClaim{})).To(Succeed())
	Expect(k8sClient.DeleteAllOf(ctx, &karpv1.NodePool{})).To(Succeed())
}

// conflictingClient returns a client whose patches to the named NodeClaim always conflict.
func conflictingClient(nodeClaimName string) client.Client {
	base, err := client.NewWithWatch(cfg, client.Options{Scheme: k8sClient.Scheme()})
	Expect(err).ToNot(HaveOccurred())
	return interceptor.NewClient(base, interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if _, ok := obj.(*karpv1.NodeClaim); ok && obj.GetName() == nodeClaimName {
				return apierrors.NewConflict(schema.GroupResource{Group: "karpenter.sh", Resource: "nodeclaims"}, nodeClaimName, errors.New("injected"))
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
	})
}
