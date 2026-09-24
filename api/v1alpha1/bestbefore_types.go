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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// BestBeforeSpec defines which NodeClaims to rotate and how old they may get.
type BestBeforeSpec struct {
	// nodeClaimSelector selects the Karpenter NodeClaims this policy applies to.
	// An empty selector matches every NodeClaim.
	// +required
	NodeClaimSelector metav1.LabelSelector `json:"nodeClaimSelector"`

	// maxAge is how long a NodeClaim may live before k-belt marks it as drifted.
	// Drifted NodeClaims are replaced by Karpenter under the NodePool's disruption budgets,
	// unlike expireAfter which deletes them regardless of budgets.
	// +kubebuilder:validation:XValidation:rule="duration(self) > duration('0s')",message="maxAge must be a positive duration"
	// +required
	MaxAge metav1.Duration `json:"maxAge"`

	// maxConcurrent caps how many of this policy's NodeClaims may be drifted and awaiting
	// replacement at once, either as a count ("3") or as a percentage of the NodeClaims the
	// selector matches ("10%"). Percentages round up, so a policy always makes progress.
	// Unset means no cap: every stale NodeClaim is drifted at once and the NodePool's disruption
	// budgets alone pace the rollout. Karpenter's budgets still apply either way.
	// Cannot be an IntOrString: kubebuilder can't pattern-check the int side of one.
	// +kubebuilder:validation:Pattern:="^((100|[0-9]{1,2})%|[0-9]+)$"
	// +optional
	MaxConcurrent *string `json:"maxConcurrent,omitempty"`

	// taintDriftedNodes adds a bestbefore.k-belt.io/drifted:PreferNoSchedule taint to the Nodes of
	// NodeClaims this policy drifts, so pods prefer nodes that aren't about to be replaced.
	//
	// Use with maxConcurrent, which limits how many NodeClaims are marked and therefore how many
	// nodes are tainted at one time.
	//
	// Warning: Karpenter's scheduling simulation treats PreferNoSchedule taints on existing nodes
	// as hard constraints unless a NodePool template also has a PreferNoSchedule taint. With this
	// enabled, Karpenter may provision extra capacity for pods that would fit on tainted nodes and
	// consolidate less effectively while drifted nodes remain.
	// +optional
	TaintDriftedNodes bool `json:"taintDriftedNodes,omitempty"`
}

// BestBeforeStatus defines the observed state of BestBefore.
type BestBeforeStatus struct {
	// observedGeneration is the generation of the spec last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// matchedNodeClaims is the number of NodeClaims selected by nodeClaimSelector.
	// +optional
	MatchedNodeClaims int32 `json:"matchedNodeClaims"`

	// staleNodeClaims is the number of selected NodeClaims older than maxAge.
	// +optional
	StaleNodeClaims int32 `json:"staleNodeClaims"`

	// driftedNodeClaims is the number of NodeClaims this policy has marked drifted and Karpenter
	// hasn't removed yet. Fewer drifted than stale NodeClaims means some couldn't be drifted,
	// e.g. they lack Karpenter's nodepool hash annotations or another policy owns them.
	// +optional
	DriftedNodeClaims int32 `json:"driftedNodeClaims"`

	// conditions represent the current state of the BestBefore resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Max Age",type=string,JSONPath=`.spec.maxAge`
// +kubebuilder:printcolumn:name="Matched",type=integer,JSONPath=`.status.matchedNodeClaims`
// +kubebuilder:printcolumn:name="Stale",type=integer,JSONPath=`.status.staleNodeClaims`
// +kubebuilder:printcolumn:name="Drifted",type=integer,JSONPath=`.status.driftedNodeClaims`
// +kubebuilder:printcolumn:name="Max Concurrent",type=string,JSONPath=`.spec.maxConcurrent`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// BestBefore rotates NodeClaims older than maxAge by marking them drifted, so Karpenter replaces
// them under the NodePool's disruption budgets instead of expiring them all at once.
type BestBefore struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec BestBeforeSpec `json:"spec"`

	// +optional
	Status BestBeforeStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// BestBeforeList contains a list of BestBefore.
type BestBeforeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []BestBefore `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &BestBefore{}, &BestBeforeList{})
		return nil
	})
}
