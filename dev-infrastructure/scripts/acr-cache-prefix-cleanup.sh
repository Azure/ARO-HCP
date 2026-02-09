#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

usage() {
    echo "Usage: SVC_ACR_NAME=<acr> $0 [prefix...]"
    echo "  DRY_RUN=true to preview only. Prefixes default to CACHE_PREFIXES in script."
    exit 1
}

if [ -z "${SVC_ACR_NAME:-}" ]; then
    echo "Error: SVC_ACR_NAME environment variable is not set"
    usage
fi

DRY_RUN_MODE=false
[ "${DRY_RUN:-}" = "true" ] && DRY_RUN_MODE=true

execute() {
    if [ "$DRY_RUN_MODE" = true ]; then
        echo "[DRY_RUN] Would run: $*"
        return 0
    else
        "$@"
    fi
}

CACHE_PREFIXES=(
    "k8s-cache/ingress-nginx/"
)

if [ $# -gt 0 ]; then
    CACHE_PREFIXES+=("$@")
fi

list_repos_with_prefix() {
    local prefix=$1
    az acr repository list --name "${SVC_ACR_NAME}" \
        --query "[?starts_with(@, '${prefix}')]" -o tsv 2>/dev/null || true
}

delete_repository() {
    local repo=$1
    execute az acr repository delete --name "${SVC_ACR_NAME}" --repository "${repo}" --yes
}

cleanup_performed=false
total_repos_found=0

for prefix in "${CACHE_PREFIXES[@]}"; do
    repos=$(list_repos_with_prefix "${prefix}")
    [ -z "$repos" ] && continue

    for repo in $repos; do
        total_repos_found=$((total_repos_found + 1))
        echo "${repo}"
        if delete_repository "${repo}"; then
            cleanup_performed=true
        else
            echo "  (failed: ${repo})" >&2
        fi
    done
done

if [ "$DRY_RUN_MODE" = true ]; then
    [ "$total_repos_found" -gt 0 ] && echo "Would delete ${total_repos_found} repo(s). Run without DRY_RUN to apply." || echo "No repos to delete."
elif [ "$cleanup_performed" = true ]; then
    echo "Cleanup complete."
else
    echo "No conflicts found."
fi
