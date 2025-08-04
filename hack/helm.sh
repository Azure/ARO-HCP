#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

HACK_DIR=$(dirname "$(realpath "${BASH_SOURCE[0]}")")

HELM_RELEASE_NAME="$1"
CHART="$2"
NAMESPACE="$3"
shift 3

# Check if the namespace exists
if ! kubectl get namespace "${NAMESPACE}" &>/dev/null; then
    echo "Namespace '${NAMESPACE}' does not exist. Please create it before running helm."
    exit 1
fi

if [ "${DRY_RUN:-false}" == "true" ]; then
  if [ "${HELM_ADOPT:-false}" == "false" ]; then
    HELM_DRY_RUN_MODE_VALUE="${HELM_DRY_RUN_MODE:-server}"
    echo "Run Helm diff with --dry-run=${HELM_DRY_RUN_MODE_VALUE} ..."

    HELM_ARGS="--install --dry-run=${HELM_DRY_RUN_MODE_VALUE} --suppress-secrets"
    if [ "${HELM_DRY_RUN_MODE_VALUE}" == "server" ]; then
      HELM_ARGS="${HELM_ARGS} --three-way-merge"
    fi

    helm diff upgrade ${HELM_ARGS} "${HELM_RELEASE_NAME}" "${CHART}" --namespace "${NAMESPACE}" "$@"
  else
    echo "Skipping Helm diff for dry-run check because Helm adopt mode is enabled"
  fi
else
  if [ "${HELM_ADOPT:-false}" == "true" ]; then
    echo "Adopting Helm release ${HELM_RELEASE_NAME} in namespace ${NAMESPACE}"
    "${HACK_DIR}/helm-adopt.sh" "${HELM_RELEASE_NAME}" "${CHART}" "${NAMESPACE}" "$@"
  fi

  echo "Run Helm upgrade with release name ${HELM_RELEASE_NAME} in namespace ${NAMESPACE}"
  helm upgrade --install --timeout="${HELM_TIMEOUT:-600s}" --wait --wait-for-jobs "${HELM_RELEASE_NAME}" "${CHART}" --namespace "${NAMESPACE}" "$@"
  HELM_EXIT_CODE=$?
  if [ "${HELM_EXIT_CODE}" -eq 0 ]; then
      echo "Helm upgrade succeeded with exit code: ${HELM_EXIT_CODE}"
  else
      echo "Helm upgrade failed with exit code: ${HELM_EXIT_CODE}"
  fi

  # run diagnostics
  "${HACK_DIR}"/deployment-diagnostics.sh "${HELM_RELEASE_NAME}" "${NAMESPACE}"

  # exit with the original exit code
  exit $HELM_EXIT_CODE
fi
