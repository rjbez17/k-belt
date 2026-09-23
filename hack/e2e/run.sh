#!/usr/bin/env bash
# End-to-end run: bring up kind + KWOK + Karpenter + k-belt, assert BestBefore behaviour, tear down.
# KEEP=1 leaves the cluster running for debugging.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# Armed before up.sh: a cluster that fails halfway through setup still needs tearing down.
if [ "${KEEP:-0}" != "1" ]; then
  trap '"${E2E_DIR}/down.sh"' EXIT
fi
"${E2E_DIR}/up.sh"
"${E2E_DIR}/test.sh"
