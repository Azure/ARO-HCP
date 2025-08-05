#!/bin/bash
#
# Outputs an image tag based on the current git revision.
#
# For personal development environments, the image tag is
# prefixed with "test-${USER}" to help distinguish from CI
# generated images.
#

set -o errexit
set -o nounset
set -o pipefail

tag=$(git rev-parse --short=7 HEAD)

if [ -n "$(git status --porcelain --untracked-files=no)" ]
then
    tag="${tag}-dirty"
fi

if [ -v DEPLOY_ENV ] && [ "${DEPLOY_ENV}" == "pers" ]
then
    tag="test-${USER}-${tag}"
fi

echo ${tag}
