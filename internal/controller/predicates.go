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
	"maps"

	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

// metadataChanged passes creates and deletes, and updates that change labels (selector matching),
// annotations (every key k-belt reads and writes) or deletion state. Karpenter rewrites NodeClaim
// status constantly, and none of that changes what k-belt does.
func metadataChanged() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			return !maps.Equal(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels()) ||
				!maps.Equal(e.ObjectOld.GetAnnotations(), e.ObjectNew.GetAnnotations()) ||
				e.ObjectOld.GetDeletionTimestamp().IsZero() != e.ObjectNew.GetDeletionTimestamp().IsZero()
		},
	}
}

// nodeClaimChanged also passes the two status fields the NodeClaim controller acts on: the Node
// name (taint sync) and Karpenter's DisruptionReason (restore is skipped once it is set).
func nodeClaimChanged() predicate.Predicate {
	return predicate.Or(metadataChanged(), predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			before, beforeOK := e.ObjectOld.(*karpv1.NodeClaim)
			after, afterOK := e.ObjectNew.(*karpv1.NodeClaim)
			if !beforeOK || !afterOK {
				return true
			}
			return before.Status.NodeName != after.Status.NodeName ||
				before.StatusConditions().Get(karpv1.ConditionTypeDisruptionReason).IsTrue() !=
					after.StatusConditions().Get(karpv1.ConditionTypeDisruptionReason).IsTrue()
		},
	})
}
