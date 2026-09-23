#!/usr/bin/env bash
# Regenerates the Helm chart's RBAC rules from the kubebuilder markers, so the chart can't drift
# from the code. CRDs are written straight into charts/k-belt/crds by controller-gen.
# Run by `make manifests`; takes the path to controller-gen.
set -euo pipefail
CONTROLLER_GEN="${1:?usage: sync-chart.sh <controller-gen>}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

generated="$("${CONTROLLER_GEN}" rbac:roleName=manager paths="./..." output:rbac:stdout)"

# The chart renders exactly one ClusterRole and supplies everything but its rules. A namespaced
# RBAC marker makes controller-gen emit a Role as well, which this cannot express: fail rather
# than splice the second document into the rules list.
if [ "$(grep -c '^kind:' <<<"${generated}")" -ne 1 ]; then
  echo "controller-gen emitted more than one RBAC object; charts/k-belt/templates/rbac.yaml needs updating to match:" >&2
  grep '^kind:' <<<"${generated}" >&2
  exit 1
fi

{
  echo "# Generated from the kubebuilder RBAC markers by hack/sync-chart.sh. DO NOT EDIT."
  awk '/^rules:/ {rules = 1; next} rules && NF' <<<"${generated}"
} > "${ROOT_DIR}/charts/k-belt/rbac-rules.yaml"
