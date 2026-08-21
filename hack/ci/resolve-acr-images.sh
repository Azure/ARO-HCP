#!/bin/bash
# Resolve ARO-HCP service images from ACR by commit SHA.
# Sourced (not exec'd) by provision-from-main.sh and provision-hypershift.sh.
#
# Callers must set:
#   ACR_CONFIG_FILE — path to rendered config YAML (for ACR name + repo paths)
#   TARGET_SHA      — 7-char git commit SHA to look up in ACR
#
# Exports: BACKEND_IMAGE, FRONTEND_IMAGE, ADMIN_API_IMAGE, SESSIONGATE_IMAGE,
#          FLEET_IMAGE, MGMT_AGENT_IMAGE, KUBE_APPLIER_IMAGE, EXPORTER_IMAGE
#
# hcpRecovery is not pushed to ACR by images-push, so it is excluded.

: "${ACR_CONFIG_FILE:?ACR_CONFIG_FILE must be set}"
: "${TARGET_SHA:?TARGET_SHA must be set}"

ACR_NAME=$(yq '.acr.svc.name' "${ACR_CONFIG_FILE}")
ACR_URL="${ACR_NAME}.azurecr.io"

BACKEND_REPO=$(yq '.backend.image.repository' "${ACR_CONFIG_FILE}")
FRONTEND_REPO=$(yq '.frontend.image.repository' "${ACR_CONFIG_FILE}")
ADMIN_API_REPO=$(yq '.adminApi.image.repository' "${ACR_CONFIG_FILE}")
SESSIONGATE_REPO=$(yq '.sessiongate.image.repository' "${ACR_CONFIG_FILE}")
FLEET_REPO=$(yq '.fleet.image.repository' "${ACR_CONFIG_FILE}")
MGMT_AGENT_REPO=$(yq '.mgmtAgent.image.repository' "${ACR_CONFIG_FILE}")
KUBE_APPLIER_REPO=$(yq '.kubeApplier.image.repository' "${ACR_CONFIG_FILE}")
EXPORTER_REPO=$(yq '.customExporter.image.repository' "${ACR_CONFIG_FILE}")
# hcpRecovery is not pushed to ACR by images-push; its config digest is
# empty by default, so we leave it as-is rather than trying to resolve it.

echo "ACR: ${ACR_URL}, target SHA: ${TARGET_SHA}"
echo "Repos: backend=${BACKEND_REPO} frontend=${FRONTEND_REPO} admin-api=${ADMIN_API_REPO} sessiongate=${SESSIONGATE_REPO} fleet=${FLEET_REPO} mgmt-agent=${MGMT_AGENT_REPO} kube-applier=${KUBE_APPLIER_REPO} exporter=${EXPORTER_REPO}"

ACR_IMAGE_REPOS=(
  "${BACKEND_REPO}"
  "${FRONTEND_REPO}"
  "${ADMIN_API_REPO}"
  "${SESSIONGATE_REPO}"
  "${FLEET_REPO}"
  "${MGMT_AGENT_REPO}"
  "${KUBE_APPLIER_REPO}"
  "${EXPORTER_REPO}"
)

image_set_available() {
  local tag=$1
  local repo

  # images-push publishes repositories sequentially, so one available image
  # does not guarantee that the commit's complete service image set is ready.
  for repo in "${ACR_IMAGE_REPOS[@]}"; do
    if ! az acr manifest show -r "${ACR_NAME}" -n "${repo}:${tag}" &>/dev/null; then
      return 1
    fi
  done
  return 0
}

# Prefer the latest main commit's images. Poll ACR in case the postsubmit
# images-push job is still running. If HEAD's images never appear, walk
# back through history to find the newest commit with images available.
MAX_POLL=30
POLL_INTERVAL=30
echo "Polling ACR for the complete image set tagged ${TARGET_SHA} (${MAX_POLL} attempts, ${POLL_INTERVAL}s between attempts) ..."
FOUND_HEAD=false
for attempt in $(seq 1 ${MAX_POLL}); do
  if image_set_available "${TARGET_SHA}"; then
    echo "Images for ${TARGET_SHA} available after attempt ${attempt}"
    FOUND_HEAD=true
    break
  fi
  echo "Attempt ${attempt}/${MAX_POLL}: not yet available, retrying in ${POLL_INTERVAL}s ..."
  sleep ${POLL_INTERVAL}
done

if [[ "${FOUND_HEAD}" != "true" ]]; then
  echo "Images for ${TARGET_SHA} not found after polling. Walking back through history ..."
  MAX_WALK=20
  IMAGE_SHA=""
  for sha in $(curl -sS "https://api.github.com/repos/Azure/ARO-HCP/commits?sha=${TARGET_SHA}&per_page=${MAX_WALK}" | jq -r '.[].sha' | cut -c1-7 | tail -n +2); do
    if image_set_available "${sha}"; then
      IMAGE_SHA="${sha}"
      echo "Found images in ACR for commit ${sha}"
      break
    fi
    echo "  ${sha}: not in ACR, trying older ..."
  done

  if [[ -z "${IMAGE_SHA}" ]]; then
    echo "ERROR: No images found in ${ACR_NAME} for any of the last ${MAX_WALK} commits from ${TARGET_SHA}. Aborting."
    exit 1
  fi
  TARGET_SHA="${IMAGE_SHA}"
fi

# Resolve each image tag to its digest from ACR.
resolve_digest() {
  local repo=$1 tag=$2
  local digest
  digest=$(az acr manifest show-metadata -r "${ACR_NAME}" -n "${repo}:${tag}" --query 'digest' -o tsv) || true
  if [[ -z "${digest}" ]]; then
    echo "ERROR: Failed to resolve digest for ${repo}:${tag}" >&2
    return 1
  fi
  echo "${digest}"
}

echo "Resolving image digests for tag ${TARGET_SHA} ..."
BACKEND_DIGEST=$(resolve_digest "${BACKEND_REPO}" "${TARGET_SHA}")
FRONTEND_DIGEST=$(resolve_digest "${FRONTEND_REPO}" "${TARGET_SHA}")
ADMIN_API_DIGEST=$(resolve_digest "${ADMIN_API_REPO}" "${TARGET_SHA}")
SESSIONGATE_DIGEST=$(resolve_digest "${SESSIONGATE_REPO}" "${TARGET_SHA}")
FLEET_DIGEST=$(resolve_digest "${FLEET_REPO}" "${TARGET_SHA}")
MGMT_AGENT_DIGEST=$(resolve_digest "${MGMT_AGENT_REPO}" "${TARGET_SHA}")
KUBE_APPLIER_DIGEST=$(resolve_digest "${KUBE_APPLIER_REPO}" "${TARGET_SHA}")
EXPORTER_DIGEST=$(resolve_digest "${EXPORTER_REPO}" "${TARGET_SHA}")

echo "Resolved digests:"
echo "  backend:      ${BACKEND_DIGEST}"
echo "  frontend:     ${FRONTEND_DIGEST}"
echo "  admin-api:    ${ADMIN_API_DIGEST}"
echo "  sessiongate:  ${SESSIONGATE_DIGEST}"
echo "  fleet:        ${FLEET_DIGEST}"
echo "  mgmt-agent:   ${MGMT_AGENT_DIGEST}"
echo "  kube-applier: ${KUBE_APPLIER_DIGEST}"
echo "  exporter:     ${EXPORTER_DIGEST}"

export BACKEND_IMAGE="${ACR_URL}/${BACKEND_REPO}@${BACKEND_DIGEST}"
export FRONTEND_IMAGE="${ACR_URL}/${FRONTEND_REPO}@${FRONTEND_DIGEST}"
export ADMIN_API_IMAGE="${ACR_URL}/${ADMIN_API_REPO}@${ADMIN_API_DIGEST}"
export SESSIONGATE_IMAGE="${ACR_URL}/${SESSIONGATE_REPO}@${SESSIONGATE_DIGEST}"
export FLEET_IMAGE="${ACR_URL}/${FLEET_REPO}@${FLEET_DIGEST}"
export MGMT_AGENT_IMAGE="${ACR_URL}/${MGMT_AGENT_REPO}@${MGMT_AGENT_DIGEST}"
export KUBE_APPLIER_IMAGE="${ACR_URL}/${KUBE_APPLIER_REPO}@${KUBE_APPLIER_DIGEST}"
export EXPORTER_IMAGE="${ACR_URL}/${EXPORTER_REPO}@${EXPORTER_DIGEST}"
