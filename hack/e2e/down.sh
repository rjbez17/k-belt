#!/usr/bin/env bash
# Deletes the e2e kind cluster.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
log "Deleting kind cluster ${CLUSTER}"
"${KIND}" delete cluster --name "${CLUSTER}"
