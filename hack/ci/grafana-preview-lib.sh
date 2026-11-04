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

# Shared helpers for the Grafana PR preview scripts.

# Application ID of Azure Managed Grafana. The token audience for its data-plane API.
GRAFANA_APP_ID="ce34e7e5-485f-4d76-964f-b3d2b16d1e4f"
# shellcheck disable=SC2034
DASHBOARD_DIR="observability/grafana-dashboards"
GITHUB_API="https://api.github.com"
PREVIEW_COMMENT_MARKER="<!-- grafana-preview -->"

GITHUB_REPO_OWNER="${GITHUB_REPO_OWNER:-Azure}"
GITHUB_REPO_NAME="${GITHUB_REPO_NAME:-ARO-HCP}"
DRY_RUN="${DRY_RUN:-false}"

# Fail early if a required tool is absent. Callers pass the tools they use.
require_tools() {
    local tool
    for tool in "$@"; do
        if ! command -v "${tool}" &>/dev/null; then
            echo "ERROR: ${tool} is required but not found" >&2
            exit 1
        fi
    done
}

# `az grafana` lives in the amg extension, so check for it before the first call.
require_amg_extension() {
    if ! az extension show --name amg &>/dev/null; then
        echo "ERROR: Azure Managed Grafana CLI extension (amg) is not installed." >&2
        echo "Install it with: az extension add --upgrade --name amg" >&2
        exit 1
    fi
}

get_grafana_endpoint() {
    az grafana show \
        --name "${GRAFANA_NAME}" \
        --resource-group "${GRAFANA_RESOURCE_GROUP}" \
        --query "properties.endpoint" \
        --output tsv
}

get_grafana_token() {
    az account get-access-token \
        --resource "${GRAFANA_APP_ID}" \
        --query "accessToken" \
        --output tsv
}

# Sets GRAFANA_ENDPOINT and GRAFANA_TOKEN. Keeps GRAFANA_ENDPOINT when the caller
# already set it, which lets a caller skip the ARM read and the amg extension.
resolve_grafana_access() {
    if [[ -z "${GRAFANA_ENDPOINT:-}" ]]; then
        : "${GRAFANA_NAME:?GRAFANA_NAME must be set}"
        : "${GRAFANA_RESOURCE_GROUP:?GRAFANA_RESOURCE_GROUP must be set}"
        require_amg_extension
        GRAFANA_ENDPOINT=$(get_grafana_endpoint)
    fi
    if [[ -z "${GRAFANA_ENDPOINT}" ]]; then
        echo "ERROR: Failed to resolve Grafana endpoint (check GRAFANA_NAME/GRAFANA_RESOURCE_GROUP and Azure auth)" >&2
        exit 1
    fi

    GRAFANA_TOKEN=$(get_grafana_token)
    if [[ -z "${GRAFANA_TOKEN}" ]]; then
        echo "ERROR: Failed to obtain Grafana access token (check Azure auth and GRAFANA_APP_ID)" >&2
        exit 1
    fi
    export GRAFANA_ENDPOINT GRAFANA_TOKEN
}

send_grafana_curl() {
    local method="$1"
    local path="$2"
    shift 2

    curl --silent \
        -X "${method}" \
        -H "Authorization: Bearer ${GRAFANA_TOKEN}" \
        -H "Content-Type: application/json" \
        "${GRAFANA_ENDPOINT}${path}" \
        "$@"
}

send_grafana_request() {
    local method="$1"
    local path="$2"
    shift 2

    send_grafana_curl "${method}" "${path}" --fail-with-body "$@"
}

# Same call as send_grafana_request, but prints only the HTTP status code and
# never fails. Use it when the status is needed, for example an existence check.
send_grafana_request_for_status() {
    local method="$1"
    local path="$2"
    shift 2

    send_grafana_curl "${method}" "${path}" --output /dev/null --write-out "%{http_code}" "$@"
}

# The naming rules for a preview. Both scripts must agree on these.
build_preview_folder_uid() {
    echo "pr-$1"
}

build_preview_folder_title() {
    echo "PR-$1"
}

# Grafana limits dashboard UIDs to 40 characters.
# If the prefixed UID is longer, replace the end with a hash of the full value so
# two truncated UIDs cannot collide.
build_preview_dashboard_uid() {
    local pull_number="$1" original_uid="$2"

    local preview_uid="pr-${pull_number}-${original_uid}"
    if (( ${#preview_uid} > 40 )); then
        local uid_hash
        uid_hash=$(printf '%s' "${preview_uid}" | sha1sum | head -c 6)
        preview_uid="${preview_uid:0:33}-${uid_hash}"
    fi
    printf '%s' "${preview_uid}"
}

# Get the HTTP status code of a GET request for the preview folder.
get_preview_folder_status_code() {
    send_grafana_request_for_status GET "/api/folders/$1"
}

# Prints the UID of every dashboard in a folder, one per line.
list_folder_dashboards() {
    local folder_uid="$1"
    send_grafana_request GET "/api/search?type=dash-db&limit=5000&folderUIDs=${folder_uid}" \
        | jq -r '.[].uid'
}

delete_dashboard() {
    local uid="$1"
    if [[ "${DRY_RUN}" == "true" ]]; then
        echo "DRY_RUN: would delete dashboard ${uid}" >&2
        return 0
    fi
    send_grafana_request DELETE "/api/dashboards/uid/${uid}" > /dev/null
}

# Deletes a folder and everything in it. A folder that is already gone counts as
# success, so a retry is harmless.
delete_preview_folder() {
    local folder_uid="$1"

    if [[ "${DRY_RUN}" == "true" ]]; then
        echo "DRY_RUN: would delete folder ${folder_uid}" >&2
        return 0
    fi

    local http_code
    http_code=$(send_grafana_request_for_status DELETE "/api/folders/${folder_uid}")
    case "${http_code}" in
        200|404)
            echo "Deleted folder ${folder_uid} (HTTP ${http_code})" >&2
            return 0
            ;;
        *)
            echo "ERROR: Failed to delete folder ${folder_uid} (HTTP ${http_code})" >&2
            return 1
            ;;
    esac
}

send_github_curl() {
    local method="$1" url="$2"
    shift 2
    case "${url}" in
        http*) : ;;
        *) url="${GITHUB_API}${url}" ;;
    esac
    curl --silent \
        -X "${method}" \
        -H "Authorization: Bearer ${GITHUB_TOKEN}" \
        -H "Accept: application/vnd.github+json" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        "${url}" "$@"
}

send_github_request() {
    local method="$1" url="$2"
    shift 2
    send_github_curl "${method}" "${url}" --fail-with-body "$@"
}

# Same call as send_github_request, but writes the body to $3 and prints only the HTTP
# status code. It never fails, so the caller can tell a 404 from another answer.
send_github_request_for_status() {
    local method="$1" url="$2" body_file="$3"
    shift 3
    send_github_curl "${method}" "${url}" --output "${body_file}" --write-out "%{http_code}" "$@"
}

# Fetch every issue comment, following the "Link: next" header across all pages.
# shellcheck disable=SC2153
list_all_issue_comments() {
    local url="${GITHUB_API}/repos/${GITHUB_REPO_OWNER}/${GITHUB_REPO_NAME}/issues/${PULL_NUMBER}/comments?per_page=100"
    local headers body next
    while [[ -n "${url}" ]]; do
        headers=$(mktemp)
        if ! body=$(send_github_request GET "${url}" -D "${headers}"); then
            echo "ERROR: Failed to read the comments of PR ${PULL_NUMBER}: ${body}" >&2
            rm -f "${headers}"
            exit 1
        fi
        printf '%s\n' "${body}"
        next=$({ grep -i '^link:' "${headers}" || true; } | tr ',' '\n' \
            | sed -n 's/.*<\([^>]*\)>;[[:space:]]*rel="next".*/\1/p' | tr -d '\r' | head -1)
        rm -f "${headers}"
        url="${next}"
    done | jq -s '[.[] | select(type == "array")] | add // []'
    # select(type == "array") drops an error body: GitHub answers a deleted or private
    # pull request with a {"message": ...} object, which would otherwise reach the
    # callers below as if it were a comment.
}

# Prints the IDs of the preview comments on PULL_NUMBER, oldest first.
find_preview_comment_ids() {
    # $marker must stay single-quoted so jq receives it literally
    # shellcheck disable=SC2016
    list_all_issue_comments \
        | jq -r --arg marker "${PREVIEW_COMMENT_MARKER}" \
            '[.[] | select(.body | startswith($marker))] | sort_by(.id) | .[].id'
}

# Writes the preview comment on PR PULL_NUMBER.
write_preview_comment() {
    local comment_body="$1" should_post_comment="$2"

    local marker_ids
    if ! marker_ids=$(find_preview_comment_ids); then
        return 1
    fi

    if [[ -z "${marker_ids}" && "${should_post_comment}" != "true" ]]; then
        return 0
    fi

    if [[ "${DRY_RUN}" == "true" ]]; then
        if [[ -z "${marker_ids}" ]]; then
            echo "DRY_RUN: would post the preview comment on PR ${PULL_NUMBER}" >&2
        else
            echo "DRY_RUN: would update the preview comment on PR ${PULL_NUMBER}" >&2
        fi
        return 0
    fi

    local full_body="${PREVIEW_COMMENT_MARKER}
${comment_body}"

    local payload
    payload=$(jq -n --arg body "${full_body}" '{body: $body}')

    if [[ -z "${marker_ids}" ]]; then
        if ! send_github_request POST "/repos/${GITHUB_REPO_OWNER}/${GITHUB_REPO_NAME}/issues/${PULL_NUMBER}/comments" \
            -H "Content-Type: application/json" \
            -d "${payload}" > /dev/null; then
            echo "ERROR: Failed to post the preview comment on PR ${PULL_NUMBER}" >&2
            return 1
        fi
        echo "Posted new PR comment" >&2
        return 0
    fi

    # Keep and edit the newest match; delete older duplicates.
    local latest_id old_id
    latest_id=$(printf '%s\n' "${marker_ids}" | tail -1)

    local has_failed=0
    while IFS= read -r old_id; do
        [[ -z "${old_id}" || "${old_id}" == "${latest_id}" ]] && continue
        if ! send_github_request DELETE "/repos/${GITHUB_REPO_OWNER}/${GITHUB_REPO_NAME}/issues/comments/${old_id}" > /dev/null; then
            echo "ERROR: Failed to delete stale preview comment ${old_id}" >&2
            has_failed=1
            continue
        fi
        echo "Deleted stale preview comment ${old_id}" >&2
    done <<< "${marker_ids}"

    if ! send_github_request PATCH "/repos/${GITHUB_REPO_OWNER}/${GITHUB_REPO_NAME}/issues/comments/${latest_id}" \
        -H "Content-Type: application/json" \
        -d "${payload}" > /dev/null; then
        echo "ERROR: Failed to update preview comment ${latest_id}" >&2
        return 1
    fi
    echo "Updated existing PR comment ${latest_id}" >&2

    return "${has_failed}"
}

# Create or update a comment. Creates a new comment if one does not exist.
create_or_update_comment() {
    write_preview_comment "$1" true
}

# Update a comment. Does NOT create a new comment if one does not exist.
update_preview_comment_if_present() {
    write_preview_comment "$1" false
}

# Removes every preview comment from PULL_NUMBER. Used when a PR no longer
# changes any dashboard, so the dead links do not stay on the PR.
delete_preview_comments() {
    local marker_ids comment_id
    if ! marker_ids=$(find_preview_comment_ids); then
        return 1
    fi
    [[ -z "${marker_ids}" ]] && return 0

    local has_failed=0
    while IFS= read -r comment_id; do
        [[ -z "${comment_id}" ]] && continue
        if [[ "${DRY_RUN}" == "true" ]]; then
            echo "DRY_RUN: would delete preview comment ${comment_id}" >&2
            continue
        fi
        if ! send_github_request DELETE "/repos/${GITHUB_REPO_OWNER}/${GITHUB_REPO_NAME}/issues/comments/${comment_id}" > /dev/null; then
            echo "ERROR: Failed to delete preview comment ${comment_id}" >&2
            has_failed=1
            continue
        fi
        echo "Deleted preview comment ${comment_id}" >&2
    done <<< "${marker_ids}"

    return "${has_failed}"
}
