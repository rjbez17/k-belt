#!/usr/bin/env bash
# Copies generated CRDs and RBAC rules from config/ into the Helm chart, so the chart can't drift
# from the kubebuilder markers. Run by `make manifests`.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART="${ROOT_DIR}/charts/k-belt"

install -m 0644 "${ROOT_DIR}"/config/crd/bases/*.yaml "${CHART}/crds/"

# The chart renders these rules into its ClusterRole via .Files.Get.
{
  echo "# Generated from config/rbac/role.yaml by hack/sync-chart.sh. DO NOT EDIT."
  python3 - "${ROOT_DIR}/config/rbac/role.yaml" <<'PY'
import sys
lines = open(sys.argv[1]).read().splitlines()
rules = lines[lines.index("rules:") + 1:]
print("\n".join(line for line in rules if line.strip()))
PY
} > "${CHART}/rbac-rules.yaml"
