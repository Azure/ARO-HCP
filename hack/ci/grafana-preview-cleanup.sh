#!/bin/bash
# Copyright 2026 Microsoft Corporation.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# ==========================================================================

# This script deletes the Grafana dashboard preview resources that
# hack/ci/grafana-preview.sh creates. It also updates the Preview PR bot comment
# to inform the user that the resources have been deleted.
#
# Two triggers start this script:
# - A webhook, when a PR merges or closes.
# - A periodic job in openshift/release. This job deletes the resources from merged
#   or closed PRs if the webhook missed them.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/ci/grafana-preview-lib.sh
source "${SCRIPT_DIR}/grafana-preview-lib.sh"

# Exit early when another repository's pull request started this job.
if [[ -n "${REPO_OWNER:-}${REPO_NAME:-}" \
    && "${REPO_OWNER:-}/${REPO_NAME:-}" != "${GITHUB_REPO_OWNER}/${GITHUB_REPO_NAME}" ]]; then
    echo "This job runs for ${REPO_OWNER:-}/${REPO_NAME:-}, not ${GITHUB_REPO_OWNER}/${GITHUB_REPO_NAME}. Skipping the preview cleanup."
    exit 0
fi

require_tools az curl jq grep sed tr head tail wc mktemp date

: "${GITHUB_TOKEN:?GITHUB_TOKEN must be set}"

PREVIEW_TTL_HOURS="${PREVIEW_TTL_HOURS:-168}"

PREVIEW_REMOVING_COMMENT="## Grafana Dashboard Preview

The preview dashboards for this PR are being deleted."

PREVIEW_REMOVED_COMMENT="## Grafana Dashboard Preview

The preview dashboards for this PR have been deleted."

deleted_count=0
kept_count=0
skipped_count=0
failed_count=0

# Prints the state of a PR: "open", "closed", "missing" when GitHub returns 404, or "unknown" for any other outcome.
# Note that the github API returns "closed" for both closed and merged PRs.
get_pr_state() {
    local pull_number="$1"

    local body http_code tmp
    tmp=$(mktemp)
    http_code=$(send_github_request_for_status GET \
        "/repos/${GITHUB_REPO_OWNER}/${GITHUB_REPO_NAME}/pulls/${pull_number}" "${tmp}")
    body=$(cat "${tmp}")
    rm -f "${tmp}"

    case "${http_code}" in
        200)
            local state
            state=$(printf '%s' "${body}" | jq -r '.state // empty')
            if [[ "${state}" == "open" || "${state}" == "closed" ]]; then
                printf '%s' "${state}"
            else
                echo "WARNING: GitHub answered PR ${pull_number} without a usable state" >&2
                printf 'unknown'
                return 1
            fi
            ;;
        404) printf 'missing' ;;
        *)
            echo "WARNING: GitHub returned HTTP ${http_code} for PR ${pull_number}" >&2
            printf 'unknown'
            return 1
            ;;
    esac
}

# Prints the age of a folder in hours, or nothing when the timestamp is unreadable.
get_folder_age_hours() {
    local folder_uid="$1"

    local created
    created=$(send_grafana_request GET "/api/folders/${folder_uid}" 2>/dev/null | jq -r '.created // empty')
    [[ -z "${created}" ]] && return 0

    local created_epoch now_epoch
    created_epoch=$(date -u -d "${created}" +%s 2>/dev/null) || return 0
    now_epoch=$(date -u +%s)
    echo $(( (now_epoch - created_epoch) / 3600 ))
}

# Prints the UID of every pr-<number> folder, one per line.
list_preview_folders() {
    local max_pages=50 # Each page holds at most 1000 folders.
    local page=1 response uids all=""
    while (( page <= max_pages )); do
        if ! response=$(send_grafana_request GET "/api/folders?limit=1000&page=${page}"); then
            echo "ERROR: Failed to read the Grafana folder list (page ${page})" >&2
            return 1
        fi
        if [[ "$(printf '%s' "${response}" | jq -r 'type' 2>/dev/null)" != "array" ]]; then
            echo "ERROR: Grafana answered the folder list with something other than an array (page ${page})" >&2
            return 1
        fi

        uids=$(printf '%s' "${response}" | jq -r '.[].uid')
        [[ -z "${uids}" ]] && break
        all+="${uids}"$'\n'
        page=$(( page + 1 ))
    done

    if (( page > max_pages )); then
        echo "ERROR: The Grafana folder list did not end after ${max_pages} pages." >&2
        return 1
    fi

    printf '%s' "${all}" | grep -E '^pr-[0-9]+$' || true
}

# Deletes a preview folder and updates the PR comment.
delete_pr_preview() {
    local folder_uid="$1" reason="$2" should_update_comment="${3:-true}"

    echo "Deleting ${folder_uid}: ${reason}"
    PULL_NUMBER="${folder_uid#pr-}"

    # Replace the dead links on the PR comment before the folder is removed.
    # The folder is the only thing that makes a later sweep find this PR, so a failed comment update must
    # keep the folder otherwise the stale comment stays on the PR forever.
    if [[ "${should_update_comment}" == "true" ]]; then
        if ! update_preview_comment_if_present "${PREVIEW_REMOVING_COMMENT}"; then
            echo "ERROR: Failed to update the preview comment on PR ${PULL_NUMBER}, keeping ${folder_uid} for the next sweep." >&2
            return 1
        fi
    fi

    if ! delete_preview_folder "${folder_uid}"; then
        return 1
    fi

    if [[ "${should_update_comment}" == "true" ]]; then
        update_preview_comment_if_present "${PREVIEW_REMOVED_COMMENT}" \
            || echo "WARNING: Deleted ${folder_uid}, but the preview comment on PR ${PULL_NUMBER} still reads \"being deleted\"." >&2
    fi
}

# Deletes a preview and counts the outcome for the sweep summary.
delete_pr_preview_and_count() {
    if delete_pr_preview "$@"; then
        deleted_count=$(( deleted_count + 1 ))
    else
        failed_count=$(( failed_count + 1 ))
    fi
}

delete_preview_if_pr_closed() {
    local folder_uid state
    folder_uid=$(build_preview_folder_uid "${PULL_NUMBER}")

    # Delete a preview only after its pull request is closed or merged.
    if ! state=$(get_pr_state "${PULL_NUMBER}"); then
        echo "ERROR: Could not read the state of PR ${PULL_NUMBER}, so the cleanup did nothing." >&2
        return 1
    fi
    if [[ "${state}" != "closed" ]]; then # Note that the github API returns "closed" for both closed and merged PRs.
        echo "Keeping the preview for PR ${PULL_NUMBER}: its state is \"${state}\", not \"closed\"."
        return 0
    fi

    delete_pr_preview "${folder_uid}" "PR ${PULL_NUMBER} is closed or merged"
}

sweep_all_previews() {
    local folders
    if ! folders=$(list_preview_folders); then
        echo "ERROR: Could not read the Grafana preview folders, so the sweep did nothing." >&2
        return 1
    fi

    if [[ -z "${folders}" ]]; then
        echo "No Grafana PR preview folders found, nothing to sweep."
        return 0
    fi

    echo "Found $(printf '%s\n' "${folders}" | wc -l) Grafana PR preview folder(s)."
    echo "Orphan TTL limit: ${PREVIEW_TTL_HOURS}h"
    echo ""

    local folder_uid pull_number state age
    while IFS= read -r folder_uid; do
        [[ -z "${folder_uid}" ]] && continue
        pull_number="${folder_uid#pr-}"

        if ! state=$(get_pr_state "${pull_number}"); then
            echo "Keeping ${folder_uid}: could not read the state of PR ${pull_number}"
            failed_count=$(( failed_count + 1 ))
            continue
        fi

        case "${state}" in
            closed)
                delete_pr_preview_and_count "${folder_uid}" "PR ${pull_number} is closed or merged"
                ;;
            open)
                echo "Keeping ${folder_uid}: PR ${pull_number} is open"
                kept_count=$(( kept_count + 1 ))
                ;;
            missing)
                age=$(get_folder_age_hours "${folder_uid}")
                if [[ -z "${age}" ]]; then
                    echo "Skipping ${folder_uid}: PR ${pull_number} does not exist but the folder age is unreadable"
                    skipped_count=$(( skipped_count + 1 ))
                elif (( age > PREVIEW_TTL_HOURS )); then
                    delete_pr_preview_and_count "${folder_uid}" "PR ${pull_number} does not exist and the folder is ${age}h old" false
                else
                    echo "Keeping ${folder_uid}: PR ${pull_number} does not exist but the folder is only ${age}h old"
                    kept_count=$(( kept_count + 1 ))
                fi
                ;;
            *)
                echo "Keeping ${folder_uid}: PR ${pull_number} has the unexpected state \"${state}\""
                failed_count=$(( failed_count + 1 ))
                ;;
        esac
    done <<< "${folders}"

    echo ""
    echo "============================================"
    echo "Grafana PR preview sweep"
    echo "============================================"
    echo "Deleted: ${deleted_count}"
    echo "Kept:    ${kept_count}"
    echo "Skipped: ${skipped_count}"
    echo "Failed:  ${failed_count}"
    echo "============================================"

    if (( failed_count > 0 )); then
        return 1
    fi
}

main() {
    resolve_grafana_access

    if [[ -n "${PULL_NUMBER:-}" ]]; then
        delete_preview_if_pr_closed
    else
        sweep_all_previews
    fi
}

main "$@"
