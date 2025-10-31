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

if [[ "${DETECT_DIRTY_GIT_WORKTREE:-1}" == "1" ]]
then
    if [ -n "$(git status --porcelain --untracked-files=no)" ]
    then
        # Fail if EXPECT_CLEAN_GIT_WORKTREE=1 and worktree is not clean
        if [[ "${EXPECT_CLEAN_GIT_WORKTREE:-}" == "1" ]]
        then
            echo "Error: Git worktree is not clean and EXPECT_CLEAN_GIT_WORKTREE=1 is set" >&2
            echo "The following modifications are present in the repository:" >&2
            git status --porcelain --untracked-files=no >&2
            exit 1
        fi
        tag="${tag}-dirty"
    fi
fi

if [[ -n "${DEPLOY_ENV:-}" ]] && [[ "${DEPLOY_ENV}" == "pers" ]]
then
    tag="test-${USER}-${tag}"
fi

echo "${tag}"
