# shellcheck shell=bash
# Shared settings for the kind + KWOK end-to-end run.
set -euo pipefail

CLUSTER="${CLUSTER:-k-belt-e2e}"
KUBE_CONTEXT="kind-${CLUSTER}"
KARPENTER_NAMESPACE="${KARPENTER_NAMESPACE:-kube-system}"
KBELT_NAMESPACE="${KBELT_NAMESPACE:-k-belt-system}"
KWOK_RELEASE="${KWOK_RELEASE:-v0.8.0}"
# Karpenter's KWOK provider is not published as an image, so it is built from a local checkout.
KARPENTER_SRC="${KARPENTER_SRC:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)/karpenter}"
KARPENTER_IMG="${KARPENTER_IMG:-karpenter-kwok:e2e}"
IMG="${IMG:-k-belt:dev}"
E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${E2E_DIR}/../.." && pwd)"
# `make test-e2e` downloads a pinned kind into bin/ and passes it in; fall back to PATH otherwise.
KIND="${KIND:-$(command -v kind || echo "${ROOT_DIR}/bin/kind")}"

kubectl() { command kubectl --context "${KUBE_CONTEXT}" "$@"; }
log() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
