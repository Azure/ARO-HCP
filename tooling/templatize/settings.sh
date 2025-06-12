#!/bin/bash
set -euo pipefail

TEMPLATIZE_DIR=$(dirname "$(realpath "${BASH_SOURCE[0]}")")

export DEPLOY_ENV=${1:-}
export EXPRESSION=${2:-""}
if [ -z "${DEPLOY_ENV}" ]; then
    echo "Usage: $0 <deploy_env> [yq expression]"
    echo "Example: $0 dev .defaults.region"
    exit 1
fi

CFG=$(yq ".environments[] | select(.name == env(DEPLOY_ENV))" "${TEMPLATIZE_DIR}/settings.yaml")
if [ -z "${CFG}" ]; then
    echo "Error: No deployment environment found with name '${DEPLOY_ENV}' in settings.yaml" >&2
    exit 1
fi
if [ -n "${EXPRESSION}" ]; then
    CFG=$(yq "${EXPRESSION}" - <<< "${CFG}")
fi
echo "${CFG}"
