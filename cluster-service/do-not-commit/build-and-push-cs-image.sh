#!/usr/bin/env bash
# Scratch script, not for commit. Builds the local clusters-service checkout
# (with the AutoNode fix) and pushes it to the personal-dev ACR so it can be
# deployed via OVERRIDE_CONFIG_FILE (see deploy-cs-image.sh in this folder).
#
# See the "AutoNode e2e fix plan" memory (project_autonode_e2e_fix_plan.md)
# for the full context on why this is needed.
set -euo pipefail

CS_CHECKOUT="${CS_CHECKOUT:-$HOME/Coding/worktree/clusters-service/karpenter}"
ACR="${ACR:-arohcpsvcdev}"
REG="${ACR}.azurecr.io"
REPO="${REPO:-cluster-service/aro-hcp-clusters-service-${USER}}"
TAG="${TAG:-autonode}"

if [[ ! -d "${CS_CHECKOUT}" ]]; then
	echo "clusters-service checkout not found at ${CS_CHECKOUT} (override with CS_CHECKOUT=...)" >&2
	exit 1
fi

echo "==> Logging podman into ${REG}"
make -C "${CS_CHECKOUT}" external_image_registry="${REG}" aks/registry

echo "==> Building and pushing ${REG}/${REPO}:${TAG}"
make -C "${CS_CHECKOUT}" push \
	external_image_registry="${REG}" \
	image_repository="${REPO}" \
	image_tag="${TAG}"

echo "==> Resolving pushed digest"
DIGEST="$(oras manifest fetch --descriptor "${REG}/${REPO}:${TAG}" | yq .digest)"

OVERRIDE_FILE="$(dirname "$0")/cs-override.yaml"
cat >"${OVERRIDE_FILE}" <<EOF
clouds:
  dev:
    environments:
      pers:
        defaults:
          clustersService:
            image:
              registry: ${REG}
              repository: ${REPO}
              digest: ${DIGEST}
EOF

echo "==> Wrote override file: ${OVERRIDE_FILE}"
echo "    registry:   ${REG}"
echo "    repository: ${REPO}"
echo "    tag:        ${TAG}"
echo "    digest:     ${DIGEST}"
echo ""
echo "Next: deploy it with:"
echo "  make pipeline/ClusterService DEPLOY_ENV=pers OVERRIDE_CONFIG_FILE=${OVERRIDE_FILE} STEP_CACHE_DIR=\"\""
echo "(or run deploy-cs-image.sh in this folder, which does exactly that)"
