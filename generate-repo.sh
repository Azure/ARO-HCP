#!/bin/bash
#
# Outputs an image repo based on the current git revision.
#
# For personal development environments, the baseline image repo is
# prefixed with "test-${USER}-" to help distinguish from CI generated images.
#
# We want to keep such images separate on the repo level to prevent
# accidental deployment to any non-dev context.
#

set -o errexit
set -o nounset
set -o pipefail

repo=${BASELINE_REPO:-}

if [[ "${DEPLOY_ENV:-}" == "pers" ]]
then
    repo="test-${USER}-${repo}"
fi

echo "${repo}"
