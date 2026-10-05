#!/usr/bin/env bash
# Scratch script, not for commit. Redeploys the ClusterService pipeline to the
# personal-dev (pers) environment using the override file produced by
# build-and-push-cs-image.sh in this same folder.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OVERRIDE_FILE="${OVERRIDE_FILE:-${SCRIPT_DIR}/cs-override.yaml}"
ARO_HCP_ROOT="${ARO_HCP_ROOT:-$HOME/Coding/worktree/ARO-HCP/karpenter}"

if [[ ! -f "${OVERRIDE_FILE}" ]]; then
	echo "override file not found at ${OVERRIDE_FILE}; run build-and-push-cs-image.sh first" >&2
	exit 1
fi

echo "==> Redeploying ClusterService (pers) with override:"
cat "${OVERRIDE_FILE}"
echo ""

make -C "${ARO_HCP_ROOT}" pipeline/ClusterService \
	DEPLOY_ENV=pers \
	OVERRIDE_CONFIG_FILE="${OVERRIDE_FILE}" \
	STEP_CACHE_DIR=""

echo "==> Verifying the deployed image:"
kubectl -n clusters-service get deploy -o jsonpath='{..image}'
echo ""
