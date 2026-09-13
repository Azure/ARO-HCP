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

for tool in az curl jq git grep sed tr head tail mktemp sha1sum; do
    if ! command -v "${tool}" &>/dev/null; then
        echo "ERROR: ${tool} is required but not found" >&2
        exit 1
    fi
done

if ! az extension show --name amg &>/dev/null; then
    echo "ERROR: Azure Managed Grafana CLI extension (amg) is not installed." >&2
    echo "Install it with: az extension add --upgrade --name amg" >&2
    exit 1
fi

GRAFANA_APP_ID="ce34e7e5-485f-4d76-964f-b3d2b16d1e4f"
DASHBOARD_DIR="observability/grafana-dashboards"
GITHUB_API="https://api.github.com"

: "${PULL_NUMBER:?PULL_NUMBER must be set}"
: "${PULL_BASE_SHA:?PULL_BASE_SHA must be set}"
: "${GRAFANA_NAME:?GRAFANA_NAME must be set}"
: "${GRAFANA_RESOURCE_GROUP:?GRAFANA_RESOURCE_GROUP must be set}"

PULL_PULL_SHA="${PULL_PULL_SHA:-$(git rev-parse HEAD)}"
REPO_OWNER="${REPO_OWNER:-Azure}"
REPO_NAME="${REPO_NAME:-ARO-HCP}"

detect_changed_dashboards() {
    if ! git cat-file -e "${PULL_BASE_SHA}^{commit}" 2>/dev/null; then
        echo "ERROR: PULL_BASE_SHA=${PULL_BASE_SHA} not found in git history (is the repo fetched deeply enough?)" >&2
        return 1
    fi

    git diff --name-only --diff-filter=ACMR "${PULL_BASE_SHA}" "${PULL_PULL_SHA}" -- "${DASHBOARD_DIR}" \
        | grep -E '\.json$' || true
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

grafana_api() {
    local method="$1"
    local path="$2"
    shift 2

    curl --silent --fail-with-body \
        -X "${method}" \
        -H "Authorization: Bearer ${GRAFANA_TOKEN}" \
        -H "Content-Type: application/json" \
        "${GRAFANA_ENDPOINT}${path}" \
        "$@"
}

create_or_get_folder() {
    local folder_uid="pr-${PULL_NUMBER}"
    local folder_title="PR-${PULL_NUMBER}"

    local response
    local http_code
    http_code=$(curl --silent --output /dev/null --write-out "%{http_code}" \
        -X GET \
        -H "Authorization: Bearer ${GRAFANA_TOKEN}" \
        "${GRAFANA_ENDPOINT}/api/folders/${folder_uid}")

    if [[ "${http_code}" == "200" ]]; then
        echo "Skipped creating folder '${folder_title}' (already exists)" >&2
        echo "${folder_uid}"
        return 0
    fi

    response=$(grafana_api POST "/api/folders" \
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
    response=$(grafana_api POST "/api/dashboards/db" -d "${payload}")

    local status
    status=$(echo "${response}" | jq -r '.status')
    if [[ "${status}" != "success" ]]; then
        echo "ERROR: Failed to upload dashboard from ${file}: ${response}" >&2
        return 1
    fi
}

github_request() {
    local method="$1" url="$2"
    shift 2
    case "${url}" in
        http*) : ;;
        *) url="${GITHUB_API}${url}" ;;
    esac
    curl --silent --fail-with-body \
        -X "${method}" \
        -H "Authorization: Bearer ${GITHUB_TOKEN}" \
        -H "Accept: application/vnd.github+json" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        "${url}" "$@"
}

# Fetch every issue comment, following the "Link: next" header across all pages.
list_all_issue_comments() {
    local url="${GITHUB_API}/repos/${REPO_OWNER}/${REPO_NAME}/issues/${PULL_NUMBER}/comments?per_page=100"
    local headers body next
    while [[ -n "${url}" ]]; do
        headers=$(mktemp)
        body=$(curl --silent --fail-with-body \
            -H "Authorization: Bearer ${GITHUB_TOKEN}" \
            -H "Accept: application/vnd.github+json" \
            -H "X-GitHub-Api-Version: 2022-11-28" \
            -D "${headers}" \
            "${url}")
        printf '%s\n' "${body}"
        next=$({ grep -i '^link:' "${headers}" || true; } | tr ',' '\n' \
            | sed -n 's/.*<\([^>]*\)>;[[:space:]]*rel="next".*/\1/p' | tr -d '\r' | head -1)
        rm -f "${headers}"
        url="${next}"
    done | jq -s 'add // []'
}

post_or_update_pr_comment() {
    local comment_body="$1"
    local marker="<!-- grafana-preview -->"

    local full_body="${marker}
${comment_body}"

    local payload
    payload=$(jq -n --arg body "${full_body}" '{body: $body}')

    local me
    me=$(github_request GET "/user" 2>/dev/null | jq -r '.login // empty' || true)

    local select_filter
    # $me and $marker below must stay single-quoted so jq receives them literally
    # shellcheck disable=SC2016
    if [[ -n "${me}" ]]; then
        select_filter='select(.user.login == $me and (.body | contains($marker)))'
    else
        select_filter='select(.body | contains($marker))'
    fi

    local marker_ids
    marker_ids=$(list_all_issue_comments \
        | jq -r --arg marker "${marker}" --arg me "${me}" \
            "[.[] | ${select_filter}] | sort_by(.id) | .[].id")

    if [[ -z "${marker_ids}" ]]; then
        github_request POST "/repos/${REPO_OWNER}/${REPO_NAME}/issues/${PULL_NUMBER}/comments" \
            -H "Content-Type: application/json" \
            -d "${payload}" > /dev/null
        echo "Posted new PR comment" >&2
        return 0
    fi

    # Keep and edit the newest match; delete older duplicates.
    local latest_id old_id
    latest_id=$(printf '%s\n' "${marker_ids}" | tail -1)

    while IFS= read -r old_id; do
        [[ -z "${old_id}" || "${old_id}" == "${latest_id}" ]] && continue
        github_request DELETE "/repos/${REPO_OWNER}/${REPO_NAME}/issues/comments/${old_id}" > /dev/null
        echo "Deleted stale preview comment ${old_id}" >&2
    done <<< "${marker_ids}"

    github_request PATCH "/repos/${REPO_OWNER}/${REPO_NAME}/issues/comments/${latest_id}" \
        -H "Content-Type: application/json" \
        -d "${payload}" > /dev/null
    echo "Updated existing PR comment ${latest_id}" >&2
}

main() {
    local changed_files
    changed_files=$(detect_changed_dashboards)

    if [[ -z "${changed_files}" ]]; then
        echo "No Grafana dashboard changes detected, skipping preview."
        exit 0
    fi

    echo "Changed dashboards:"
    echo "${changed_files}"
    echo ""

    GRAFANA_ENDPOINT=$(get_grafana_endpoint)
    if [[ -z "${GRAFANA_ENDPOINT}" ]]; then
        echo "ERROR: Failed to resolve Grafana endpoint (check GRAFANA_NAME/GRAFANA_RESOURCE_GROUP and Azure auth)" >&2
        exit 1
    fi
    GRAFANA_TOKEN=$(get_grafana_token)
    if [[ -z "${GRAFANA_TOKEN}" ]]; then
        echo "ERROR: Failed to obtain Grafana access token (check Azure auth and GRAFANA_APP_ID)" >&2
        exit 1
    fi

    local folder_uid
    folder_uid=$(create_or_get_folder)

    local dashboard_links=()
    local file original_uid title preview_uid link

    while IFS= read -r file; do
        original_uid=$(jq -r 'if .dashboard then .dashboard.uid else .uid end' "${file}")
        title=$(jq -r 'if .dashboard then .dashboard.title else .title end' "${file}")

        if [[ -z "${original_uid}" || "${original_uid}" == "null" ]]; then
            echo "WARNING: Skipping ${file} — no uid found" >&2
            continue
        fi

        # Grafana limits dashboard UIDs to 40 characters.
        # If the new UID is longer, replace the end with hash.
        preview_uid="pr-${PULL_NUMBER}-${original_uid}"
        if (( ${#preview_uid} > 40 )); then
            local uid_hash
            uid_hash=$(printf '%s' "${preview_uid}" | sha1sum | head -c 6)
            preview_uid="${preview_uid:0:33}-${uid_hash}"
        fi

        echo "Uploading: ${title} (${file})"
        upload_dashboard "${file}" "${folder_uid}" "${preview_uid}" "${title}"

        link="${GRAFANA_ENDPOINT}/d/${preview_uid}"
        local title_md
        title_md=$(printf '%s' "${title}" | tr '\n' ' ' | sed 's/[][]/\\&/g') # prevent PR title from escaping markdown
        dashboard_links+=("- [${title_md}](${link})")
    done <<< "${changed_files}"

    echo ""
    echo "============================================"
    echo "Grafana Dashboard Preview Links"
    echo "============================================"
    echo "Folder: ${GRAFANA_ENDPOINT}/dashboards/f/${folder_uid}"
    for entry in "${dashboard_links[@]}"; do
        echo "${entry}"
    done
    echo "============================================"

    if [[ -n "${GITHUB_TOKEN:-}" ]]; then
        local links_md
        links_md=$(printf '%s\n' "${dashboard_links[@]}")

        local comment_body
        comment_body="## Grafana Dashboard Preview

The following Grafana dashboards have been deployed for preview:

${links_md}

:file_folder: [View all in PR folder](${GRAFANA_ENDPOINT}/dashboards/f/${folder_uid})

*Preview updated for commit ${PULL_PULL_SHA:0:7}*"

        if ! post_or_update_pr_comment "${comment_body}"; then
            echo "WARNING: Failed to post/update PR comment." >&2
        fi
    else
        echo "GITHUB_TOKEN not set, skipping PR comment."
    fi
}

main "$@"
