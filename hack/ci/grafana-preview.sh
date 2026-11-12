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

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/ci/grafana-preview-lib.sh
source "${SCRIPT_DIR}/grafana-preview-lib.sh"

# Exit early when another repository's pull request started this job. Without
# this guard, PULL_BASE_SHA points at a commit that is absent from the ARO-HCP
# history, and the preview comment goes to the wrong pull request.
if [[ -n "${REPO_OWNER:-}${REPO_NAME:-}" \
    && "${REPO_OWNER:-}/${REPO_NAME:-}" != "${GITHUB_REPO_OWNER}/${GITHUB_REPO_NAME}" ]]; then
    echo "This job runs for ${REPO_OWNER:-}/${REPO_NAME:-}, not ${GITHUB_REPO_OWNER}/${GITHUB_REPO_NAME}. Skipping the dashboard preview."
    exit 0
fi

require_tools az curl jq git grep sed tr head tail mktemp sha1sum

: "${PULL_NUMBER:?PULL_NUMBER must be set}"
: "${PULL_BASE_SHA:?PULL_BASE_SHA must be set}"
: "${GITHUB_TOKEN:?GITHUB_TOKEN must be set}"

PULL_PULL_SHA="${PULL_PULL_SHA:-$(git rev-parse HEAD)}"

get_modified_dashboards() {
    if ! git cat-file -e "${PULL_BASE_SHA}^{commit}" 2>/dev/null; then
        echo "ERROR: PULL_BASE_SHA=${PULL_BASE_SHA} not found in git history (is the repo fetched deeply enough?)" >&2
        return 1
    fi

    if ! git merge-base "${PULL_BASE_SHA}" "${PULL_PULL_SHA}" >/dev/null 2>&1; then
        echo "ERROR: no merge base between ${PULL_BASE_SHA} and ${PULL_PULL_SHA} (is the repo fetched deeply enough?)" >&2
        return 1
    fi

    git diff --name-only --diff-filter=ACMR "${PULL_BASE_SHA}...${PULL_PULL_SHA}" -- "${DASHBOARD_DIR}" \
        | grep -E '\.json$' || true
}

create_or_read_preview_folder() {
    local folder_uid folder_title
    folder_uid=$(build_preview_folder_uid "${PULL_NUMBER}")
    folder_title=$(build_preview_folder_title "${PULL_NUMBER}")

    local folder_status_code
    folder_status_code=$(get_preview_folder_status_code "${folder_uid}")
    case "${folder_status_code}" in
        200)
            echo "Skipped creating folder '${folder_title}' (already exists)" >&2
            echo "${folder_uid}"
            return 0
            ;;
        404)
            echo "No preview folder currently exists. Creating folder..." >&2
            ;;
        *)
            echo "ERROR: Failed to read folder ${folder_uid} from Grafana (HTTP ${folder_status_code})" >&2
            return 1
            ;;
    esac

    local response
    response=$(send_grafana_request POST "/api/folders" \
        -d "{\"uid\": \"${folder_uid}\", \"title\": \"${folder_title}\"}")

    local created_uid
    created_uid=$(echo "${response}" | jq -r '.uid')
    if [[ -z "${created_uid}" || "${created_uid}" == "null" ]]; then
        echo "ERROR: Failed to create folder: ${response}" >&2
        return 1
    fi

    echo "Created folder '${folder_title}'" >&2
    echo "${created_uid}"
}

upload_dashboard() {
    local file="$1"
    local folder_uid="$2"
    local preview_uid="$3"
    local title="$4"

    local dashboard_json
    dashboard_json=$(jq \
        --arg uid "${preview_uid}" \
        --arg title "${title}" \
        'if .dashboard then .dashboard else . end
         | .uid = $uid
         | .title = $title
         | .id = null
         | .version = 0' \
        "${file}")

    local payload
    payload=$(jq -n \
        --arg folder_uid "${folder_uid}" \
        --argjson dashboard "${dashboard_json}" \
        '{dashboard: $dashboard, folderUid: $folder_uid, overwrite: true}')

    local response
    response=$(send_grafana_request POST "/api/dashboards/db" -d "${payload}")

    local status
    status=$(echo "${response}" | jq -r '.status')
    if [[ "${status}" != "success" ]]; then
        echo "ERROR: Failed to upload dashboard from ${file}: ${response}" >&2
        return 1
    fi
}

# Deletes the dashboards that are in the preview folder but are not in the current
# set of changes in the Pull Request due to a force-push that dropped dashboards.
prune_removed_dashboards() {
    local folder_uid="$1"
    shift
    local -a kept_uids=("$@")

    local existing_uids
    if ! existing_uids=$(list_folder_dashboards "${folder_uid}"); then
        echo "ERROR: Failed to list the dashboards in folder ${folder_uid}" >&2
        return 1
    fi
    [[ -z "${existing_uids}" ]] && return 0

    local existing_uid
    while IFS= read -r existing_uid; do
        [[ -z "${existing_uid}" ]] && continue

        # Prune only the dashboards that this PR created.
        if [[ "${existing_uid}" != "pr-${PULL_NUMBER}-"* ]]; then
            echo "WARNING: Skipping ${existing_uid}: it is not a preview of PR ${PULL_NUMBER}" >&2
            continue
        fi

        local should_keep=false uid
        for uid in "${kept_uids[@]}"; do
            if [[ "${uid}" == "${existing_uid}" ]]; then
                should_keep=true
                break
            fi
        done
        "${should_keep}" && continue

        echo "Pruning dashboard no longer changed by this PR: ${existing_uid}"
        delete_dashboard "${existing_uid}" || echo "WARNING: Failed to prune ${existing_uid}" >&2
    done <<< "${existing_uids}"
}

# Removes the preview folder and comment when the PR no longer contains dashboard changes
# after a force-push that reverted the dashboard updates.
delete_preview_folder_and_comments() {
    local folder_uid
    folder_uid=$(build_preview_folder_uid "${PULL_NUMBER}")

    local folder_status_code
    folder_status_code=$(get_preview_folder_status_code "${folder_uid}")

    local has_failed=0
    case "${folder_status_code}" in
        200)
            echo "No Grafana dashboard changes detected, removing the existing preview."
            delete_preview_folder "${folder_uid}" || has_failed=1
            ;;
        404)
            echo "No Grafana dashboard changes detected and no preview folder exists."
            ;;
        *)
            echo "ERROR: Could not read the preview folder ${folder_uid} (HTTP ${folder_status_code}), so the cleanup did nothing." >&2
            return 1
            ;;
    esac

    # An earlier run can delete the folder and still leave the comment behind, so
    # look for a comment even when the folder is already gone. A failure here must
    # fail the job: the dead links stay on the PR until a rerun removes them.
    if ! delete_preview_comments; then
        echo "ERROR: Failed to delete the preview PR comment." >&2
        has_failed=1
    fi

    return "${has_failed}"
}

main() {
    local modified_dashboards
    modified_dashboards=$(get_modified_dashboards)

    resolve_grafana_access

    if [[ -z "${modified_dashboards}" ]]; then
        delete_preview_folder_and_comments
        exit 0
    fi

    echo "Modified dashboards:"
    echo "${modified_dashboards}"
    echo ""

    local folder_uid
    folder_uid=$(create_or_read_preview_folder)

    local dashboard_links=()
    local uploaded_uids=()
    local file original_uid title preview_uid link

    while IFS= read -r file; do
        original_uid=$(jq -r 'if .dashboard then .dashboard.uid else .uid end' "${file}")
        title=$(jq -r 'if .dashboard then .dashboard.title else .title end' "${file}")

        if [[ -z "${original_uid}" || "${original_uid}" == "null" ]]; then
            echo "WARNING: Skipping ${file} — no uid found" >&2
            continue
        fi

        preview_uid=$(build_preview_dashboard_uid "${PULL_NUMBER}" "${original_uid}")

        echo "Uploading: ${title} (${file})"
        upload_dashboard "${file}" "${folder_uid}" "${preview_uid}" "${title}"
        uploaded_uids+=("${preview_uid}")

        link="${GRAFANA_ENDPOINT}/d/${preview_uid}"
        local title_md
        title_md=$(printf '%s' "${title}" | tr '\n' ' ' | sed 's/[][]/\\&/g') # prevent PR title from escaping markdown
        dashboard_links+=("- [${title_md}](${link})")
    done <<< "${modified_dashboards}"

    # Every changed file was skipped for a missing uid, so nothing is previewable.
    if (( ${#uploaded_uids[@]} == 0 )); then
        echo "WARNING: No dashboard had a usable uid, removing the preview." >&2
        delete_preview_folder_and_comments
        exit 0
    fi

    prune_removed_dashboards "${folder_uid}" "${uploaded_uids[@]}"

    echo ""
    echo "============================================"
    echo "Grafana Dashboard Preview Links"
    echo "============================================"
    echo "Folder: ${GRAFANA_ENDPOINT}/dashboards/f/${folder_uid}"
    for entry in "${dashboard_links[@]}"; do
        echo "${entry}"
    done
    echo "============================================"

    local links_md
    links_md=$(printf '%s\n' "${dashboard_links[@]}")

    local comment_body
    comment_body="## Grafana Dashboard Preview

The following Grafana dashboards have been deployed for preview:

${links_md}

:file_folder: [View all in PR folder](${GRAFANA_ENDPOINT}/dashboards/f/${folder_uid})

When this PR closes or merges, the preview dashboards will be deleted.

*Preview updated for commit ${PULL_PULL_SHA:0:7}*"

    if ! create_or_update_comment "${comment_body}"; then
        echo "WARNING: Failed to create/update PR comment." >&2
    fi
}

main "$@"
