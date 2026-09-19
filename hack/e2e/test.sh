#!/usr/bin/env bash
# Exercises BestBefore against a real Karpenter (KWOK provider) in the kind cluster from up.sh:
# nodes rotate under the NodePool's disruption budget, drift is undone when the policy goes away,
# and the paused/revert annotations are honoured.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

NODES="${NODES:-6}"          # replicas, and with one pod per node, nodes
BUDGET_NODES=1               # NodePool disruption budget in manifests/nodepool.yaml
ROLLOUT_TIMEOUT="${ROLLOUT_TIMEOUT:-360}"

fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*" >&2; exit 1; }
pass() { printf '\033[32mok: %s\033[0m\n' "$*"; }
claims() { kubectl get nodeclaims -o json | python3 "${E2E_DIR}/state.py" "$1"; }
nodes() { kubectl get nodes -o json | python3 "${E2E_DIR}/state.py" "$1"; }

# retry <seconds> <description> <command...> — runs until it succeeds or the deadline passes.
retry() {
  local deadline=$((SECONDS + $1)) description="$2"; shift 2
  until "$@"; do
    ((SECONDS < deadline)) || fail "timed out waiting for ${description}"
    sleep 5
  done
  pass "${description}"
}

log "Resetting cluster state"
kubectl delete bestbefore --all --ignore-not-found
kubectl delete -f "${E2E_DIR}/manifests/workload.yaml" --ignore-not-found
kubectl delete nodeclaims --all --timeout=120s 2>/dev/null || true
# Nodes can outlive their NodeClaim if a previous run was interrupted.
kubectl delete nodes -l karpenter.sh/nodepool --timeout=60s 2>/dev/null || true

log "Provisioning ${NODES} nodes"
kubectl apply -f "${E2E_DIR}/manifests/nodepool.yaml" -f "${E2E_DIR}/manifests/workload.yaml"
kubectl scale deployment/filler --replicas "${NODES}"
retry 180 "${NODES} NodeClaims" bash -c "[ \$(kubectl get nodeclaims --no-headers 2>/dev/null | grep -c True) -eq ${NODES} ]"
original=$(claims names)

log "Rotating them with a BestBefore"
kubectl apply -f "${E2E_DIR}/manifests/bestbefore.yaml"
deadline=$((SECONDS + ROLLOUT_TIMEOUT))
max_disrupting=0 saw_marked=0 saw_taint=0 saw_status=0 rotated=0
while ((SECONDS < deadline)); do
  read -r total marked drifted disrupting <<<"$(claims summary)"
  if ((disrupting > max_disrupting)); then max_disrupting=${disrupting}; fi
  if ((marked > 0)); then saw_marked=1; fi
  if ((drifted < marked)); then fail "k-belt marked ${marked} NodeClaims but Karpenter sees ${drifted} drifted"; fi
  if (($(nodes taints) > 0)); then saw_taint=1; fi
  status_drifted=$(kubectl get bestbefore e2e -o jsonpath='{.status.driftedNodeClaims}' 2>/dev/null)
  if ((${status_drifted:-0} > 0)); then saw_status=1; fi
  # Every original NodeClaim replaced means the rollout completed.
  remaining=$(comm -12 <(tr ' ' '\n' <<<"${original}" | sort) <(claims names | tr ' ' '\n' | sort) | grep -c . || true)
  if ((remaining == 0)); then rotated=1; break; fi
  sleep 5
done

if ((rotated != 1)); then fail "rollout did not replace every original NodeClaim within ${ROLLOUT_TIMEOUT}s"; fi
pass "all ${NODES} original NodeClaims replaced"
if ((max_disrupting > BUDGET_NODES)); then fail "budget allows ${BUDGET_NODES} node, saw ${max_disrupting} disrupting at once"; fi
pass "never more than ${BUDGET_NODES} node disrupting at once (budget honoured)"
if ((saw_marked != 1)); then fail "k-belt never marked a NodeClaim"; fi
if ((saw_taint != 1)); then fail "no node ever carried the drifted taint"; fi
pass "drifted taint applied while taintDriftedNodes is set"
if ((saw_status != 1)); then fail "BestBefore status never reported drifted NodeClaims"; fi
pass "status reported drifted NodeClaims"

log "Deleting the policy restores the NodeClaims"
kubectl delete bestbefore e2e
retry 90 "k-belt annotations and taints removed" bash -c '[ -z "$(kubectl get nodeclaims -o json | python3 '"${E2E_DIR}"'/state.py annotation-keys)" ] && [ "$(kubectl get nodes -o json | python3 '"${E2E_DIR}"'/state.py taints)" = 0 ]'
read -r _ _ drifted _ <<<"$(claims summary)"
if ((drifted != 0)); then fail "${drifted} NodeClaims still drifted after the policy was deleted"; fi
pass "Karpenter cleared Drifted after restore"

log "Honouring the paused and revert annotations"
retry 120 "cluster settled" bash -c "[ \$(kubectl get nodeclaims --no-headers | grep -c True) -eq ${NODES} ]"
# Stop Karpenter replacing nodes for this phase: it would delete the NodeClaim under test, and a
# deleted NodeClaim would satisfy the "no longer drifted" check for the wrong reason.
kubectl patch nodepool default --type merge -p '{"spec":{"disruption":{"budgets":[{"nodes":"0"}]}}}'
for claim in $(claims names); do
  kubectl annotate nodeclaim "${claim}" bestbefore.k-belt.sh/paused=true --overwrite >/dev/null
done
kubectl apply -f "${E2E_DIR}/manifests/bestbefore.yaml"
sleep 45  # well past maxAge: every NodeClaim is stale but paused
[ -z "$(claims marked)" ] || fail "paused NodeClaims were drifted anyway: $(claims marked)"
pass "paused NodeClaims left alone"

unpaused=$(claims names | awk '{print $1}')
kubectl annotate nodeclaim "${unpaused}" bestbefore.k-belt.sh/paused- >/dev/null
retry 60 "unpaused NodeClaim drifted" bash -c "kubectl get nodeclaim ${unpaused} -o jsonpath='{.metadata.annotations}' | grep -q bestbefore.k-belt.sh/policy"
kubectl annotate nodeclaim "${unpaused}" bestbefore.k-belt.sh/revert=true --overwrite >/dev/null
# The NodeClaim must still exist: a deleted one has no annotations either.
retry 60 "reverted NodeClaim restored" bash -c "annotations=\$(kubectl get nodeclaim ${unpaused} -o jsonpath='{.metadata.annotations}') && ! grep -q bestbefore.k-belt.sh/policy <<<\"\${annotations}\""

kubectl delete bestbefore e2e --ignore-not-found
log "All end-to-end checks passed"
