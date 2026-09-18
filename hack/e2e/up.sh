#!/usr/bin/env bash
# Brings up a kind cluster running KWOK, Karpenter's KWOK provider and k-belt.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

if ! "${KIND}" get clusters | grep -qx "${CLUSTER}"; then
  log "Creating kind cluster ${CLUSTER}"
  "${KIND}" create cluster --name "${CLUSTER}" --wait 120s
fi

log "Installing KWOK ${KWOK_RELEASE}"
kubectl apply -f "https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_RELEASE}/kwok.yaml"
kubectl apply -f "https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_RELEASE}/stage-fast.yaml"
# Karpenter's own stages: pods on fake nodes need to reach Running and be deletable.
kubectl apply -f "${E2E_DIR}/kwok-stages"
# Keep the KWOK controller itself off the fake nodes it creates.
kubectl -n kube-system patch deployment kwok-controller --type strategic -p '{"spec":{"template":{"spec":{"affinity":{"nodeAffinity":{"requiredDuringSchedulingIgnoredDuringExecution":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"kwok.x-k8s.io/node","operator":"DoesNotExist"}]}]}}}}}}}'

log "Building Karpenter's KWOK provider from ${KARPENTER_SRC}"
[ -d "${KARPENTER_SRC}" ] || { echo "Karpenter checkout not found; set KARPENTER_SRC" >&2; exit 1; }
BUILD_DIR="$(mktemp -d)"
trap 'rm -rf "${BUILD_DIR}"' EXIT
# Copy out rather than building in place: the checkout is read-only reference material.
rsync -a --exclude .git --exclude website --exclude test "${KARPENTER_SRC}/" "${BUILD_DIR}/"
cat > "${BUILD_DIR}/Dockerfile" <<'DOCKERFILE'
FROM golang:1.26 AS builder
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /kwok-controller ./kwok
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /kwok-controller /kwok-controller
USER 65532:65532
ENTRYPOINT ["/kwok-controller"]
DOCKERFILE
docker build -q -t "${KARPENTER_IMG}" "${BUILD_DIR}"
"${KIND}" load docker-image "${KARPENTER_IMG}" --name "${CLUSTER}"

log "Installing Karpenter (KWOK provider)"
kubectl apply -f "${KARPENTER_SRC}/kwok/charts/crds"
helm --kube-context "${KUBE_CONTEXT}" upgrade --install karpenter "${KARPENTER_SRC}/kwok/charts" \
  --namespace "${KARPENTER_NAMESPACE}" --skip-crds --wait \
  --set controller.image.repository="${KARPENTER_IMG%%:*}" \
  --set controller.image.tag="${KARPENTER_IMG##*:}" \
  --set controller.image.digest="" \
  --set settings.preferencePolicy=Ignore \
  --set replicas=1 \
  `# this checkout's chart templates gates its values.yaml doesn't define; empty values panic on boot` \
  --set settings.featureGates.staticCapacity=false \
  --set settings.featureGates.capacityBuffer=false
kubectl -n "${KARPENTER_NAMESPACE}" rollout status deployment/karpenter --timeout=180s

log "Deploying k-belt"
make -C "${ROOT_DIR}" build IMG="${IMG}"
"${KIND}" load docker-image "${IMG}" --name "${CLUSTER}"
make -C "${ROOT_DIR}" build-installer IMG="${IMG}"
# build-installer pins the image by editing a tracked kustomization; keep the tree clean.
git -C "${ROOT_DIR}" checkout -- config/manager/kustomization.yaml
kubectl apply -f "${ROOT_DIR}/dist/install.yaml"
# The image is side-loaded into kind, so don't try to pull it.
kubectl -n k-belt-system patch deployment k-belt-controller-manager --type strategic \
  -p '{"spec":{"template":{"spec":{"containers":[{"name":"manager","imagePullPolicy":"IfNotPresent"}]}}}}'
kubectl -n k-belt-system rollout status deployment/k-belt-controller-manager --timeout=180s

log "Cluster ready"
kubectl get pods -A | grep -E "karpenter|kwok|k-belt"
