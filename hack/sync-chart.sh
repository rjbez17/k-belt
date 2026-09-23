#!/usr/bin/env bash
# Regenerates the Helm chart's RBAC rules from the kubebuilder markers, so the chart can't drift
# from the code. CRDs are written straight into charts/k-belt/crds by controller-gen.
# Run by `make manifests`; takes the path to controller-gen.
set -euo pipefail
CONTROLLER_GEN="${1:?usage: sync-chart.sh <controller-gen>}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

{
  echo "# Generated from the kubebuilder RBAC markers by hack/sync-chart.sh. DO NOT EDIT."
  # controller-gen emits a whole ClusterRole; the chart's template supplies everything but the rules.
  "${CONTROLLER_GEN}" rbac:roleName=manager paths="./..." output:rbac:stdout |
    awk '/^rules:/ {rules = 1; next} rules && NF'
} > "${ROOT_DIR}/charts/k-belt/rbac-rules.yaml"
