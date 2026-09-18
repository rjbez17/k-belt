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

// Package karpenter adapts Karpenter's API types for use in k-belt.
package karpenter

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/karpenter/pkg/apis"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

// GroupVersion is the Karpenter core API group version k-belt works with.
var GroupVersion = schema.GroupVersion{Group: apis.Group, Version: "v1"}

// AddToScheme registers Karpenter's NodePool and NodeClaim types.
// Karpenter only registers them into client-go's global scheme, so managers
// with their own scheme need this.
func AddToScheme(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion,
		&karpv1.NodePool{},
		&karpv1.NodePoolList{},
		&karpv1.NodeClaim{},
		&karpv1.NodeClaimList{},
	)
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}
