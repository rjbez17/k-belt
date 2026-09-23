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
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	metricsNamespace = "kbelt"
	metricsSubsystem = "bestbefore"
	bestBeforeLabel  = "bestbefore"
)

var (
	matchedNodeClaimsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "matched_nodeclaims",
		Help:      "Number of NodeClaims selected by the BestBefore policy.",
	}, []string{bestBeforeLabel})
	staleNodeClaimsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "stale_nodeclaims",
		Help:      "Number of selected NodeClaims older than the BestBefore policy's maxAge.",
	}, []string{bestBeforeLabel})
	driftedNodeClaimsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "drifted_nodeclaims",
		Help:      "Number of NodeClaims the BestBefore policy has marked drifted that Karpenter hasn't removed yet.",
	}, []string{bestBeforeLabel})
	oldestDriftTimestampGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "oldest_drift_timestamp_seconds",
		Help: "Unix time the policy's longest-standing drifted NodeClaim was marked drifted, or 0 if none. " +
			"Alert on time() - this value to catch rollouts Karpenter isn't making progress on.",
	}, []string{bestBeforeLabel})
)

func init() {
	metrics.Registry.MustRegister(matchedNodeClaimsGauge, staleNodeClaimsGauge, driftedNodeClaimsGauge, oldestDriftTimestampGauge)
}

func deleteBestBeforeMetrics(name string) {
	for _, gauge := range []*prometheus.GaugeVec{matchedNodeClaimsGauge, staleNodeClaimsGauge, driftedNodeClaimsGauge, oldestDriftTimestampGauge} {
		gauge.DeleteLabelValues(name)
	}
}
