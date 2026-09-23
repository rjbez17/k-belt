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

// Keys BestBefore sets on the NodeClaims and Nodes it manages, and the two annotations users set
// to control it. They are part of the API: scripts and operators depend on them.
const (
	// DriftedHashValue is what BestBefore writes into karpenter.sh/nodepool-hash to make Karpenter
	// see the NodeClaim as drifted. Karpenter's hashes are numeric, so this can never collide with
	// a real one.
	DriftedHashValue = "bestbefore.k-belt.io"

	// PolicyAnnotationKey records which BestBefore drifted the NodeClaim.
	PolicyAnnotationKey = "bestbefore.k-belt.io/policy"
	// OriginalHashAnnotationKey holds the nodepool-hash BestBefore replaced, for restoring it.
	OriginalHashAnnotationKey = "bestbefore.k-belt.io/original-nodepool-hash"
	// OriginalHashVersionAnnotationKey holds the nodepool-hash-version the original hash was computed with.
	OriginalHashVersionAnnotationKey = "bestbefore.k-belt.io/original-nodepool-hash-version"
	// DriftedAtAnnotationKey records when BestBefore drifted the NodeClaim (RFC 3339).
	DriftedAtAnnotationKey = "bestbefore.k-belt.io/drifted-at"

	// PausedAnnotationKey, set on a NodeClaim with any value, makes BestBefore leave it exactly as
	// it is: no drift, no restore and no taint changes.
	PausedAnnotationKey = "bestbefore.k-belt.io/paused"
	// RevertAnnotationKey, set on a NodeClaim with any value, pauses it like PausedAnnotationKey
	// and first restores the hash BestBefore replaced.
	RevertAnnotationKey = "bestbefore.k-belt.io/revert"

	// DriftedTaintKey is the PreferNoSchedule taint added to drifted nodes by policies that set
	// spec.taintDriftedNodes.
	DriftedTaintKey = "bestbefore.k-belt.io/drifted"
)
