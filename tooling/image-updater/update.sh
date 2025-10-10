#!/bin/bash
set -euo pipefail

CREATE_PR=true
COMPONENTS=""
EXCLUDE_COMPONENTS=""

if [ -n "${GITHUB_SERVER_URL:-}" ] && [ -n "${GITHUB_REPOSITORY:-}" ] && [ -n "${GITHUB_RUN_ID:-}" ]; then
    WORKFLOW_URL="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}"
    AUTOMATION_CREDIT="PR created automatically with [Image Digest Updater](${WORKFLOW_URL})"
else
    AUTOMATION_CREDIT="Automatically updated with Image Digest Updater"
fi

log() {
    echo "[$(date +'%H:%M:%S')] $1" >&2
}

usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Bulk update all component image digests and create a single PR.

OPTIONS:
    --components NAMES          Update only specified components (comma-separated)
    --exclude-components NAMES  Exclude specified components from update (comma-separated)
    --no-pr                     Skip PR creation (useful for local testing)
    -h, --help                  Show this help message

EXAMPLES:
    $0                                              # Update all components and create PR
    $0 --no-pr                                     # Update all components but don't create PR
    $0 --components arohcpfrontend,arohcpbackend   # Update only frontend and backend
    $0 --exclude-components arohcpfrontend,arohcpbackend  # Update all except frontend and backend
EOF
}

parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --components)
                COMPONENTS="$2"
                shift 2
                ;;
            --exclude-components)
                EXCLUDE_COMPONENTS="$2"
                shift 2
                ;;
            --no-pr)
                CREATE_PR=false
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                log "❌ Unknown option: $1"
                usage
                exit 1
                ;;
        esac
    done
}

get_current_date() {
    date '+%Y-%m-%d'
}

cleanup_previous_prs() {
    log "🔍 Checking for previous image update PRs to close..."

    local existing_prs
    existing_prs=$(gh pr list --state open --search "updated image components in:title" --json number,title --jq '.[].number' 2>/dev/null || echo "")

    if [[ -n "$existing_prs" ]]; then
        log "📝 Found existing image update PRs, closing them..."
        echo "$existing_prs" | while read -r pr_number; do
            if [[ -n "$pr_number" ]]; then
                log "   Closing PR #$pr_number"
                gh pr close "$pr_number" --comment "Superseded by new image update" 2>/dev/null || log "   ⚠️  Failed to close PR #$pr_number"
            fi
        done
    else
        log "   No previous image update PRs found"
    fi
}

create_update_branch() {
    local job_suffix="${JOB_NAME:-default}"
    local branch_name="image-update-${job_suffix}-$(get_current_date)"
    local current_branch
    current_branch=$(git branch --show-current)

    log "🌱 Creating branch: $branch_name from current branch ($current_branch)"

    if git show-ref --verify --quiet "refs/heads/$branch_name"; then
        log "   Deleting existing local branch: $branch_name"
        git branch -D "$branch_name" >/dev/null 2>&1
    fi

    git checkout -b "$branch_name" >/dev/null 2>&1 || {
        log "❌ Failed to create branch: $branch_name"
        exit 1
    }

    echo "$branch_name"
}

run_image_update() {
    log "🔄 Running image update..."

    local commit_message
    local exit_code
    local make_args=""

    # Build make arguments
    if [[ -n "$COMPONENTS" ]]; then
        make_args="COMPONENTS=\"$COMPONENTS\""
    fi
    if [[ -n "$EXCLUDE_COMPONENTS" ]]; then
        make_args="$make_args EXCLUDE_COMPONENTS=\"$EXCLUDE_COMPONENTS\""
    fi

    # Run make update:
    # - stdout is captured (contains commit message if updates made)
    # - stderr is left alone (contains logger output from Go)
    # - We capture the exit code
    set +e
    if [[ -n "$make_args" ]]; then
        commit_message=$(eval make update $make_args 2>&2)
    else
        commit_message=$(make update 2>&2)
    fi
    exit_code=$?
    set -e

    if [[ $exit_code -ne 0 ]]; then
        log "❌ make update failed with exit code $exit_code"
        exit 1
    fi

    # Check if commit message is non-empty
    if [[ -n "$commit_message" ]]; then
        log "✅ Image updates found"
        echo "$commit_message"
        return 0
    else
        log "ℹ️  No image updates needed"
        return 1
    fi
}

run_config_materialize() {
    log "🔧 Running config materialization..."

    if ! make -C ../../config materialize >/dev/null 2>&1; then
        log "❌ Failed to materialize config"
        exit 1
    fi

    log "✅ Config materialization completed"
}

commit_changes() {
    local commit_message="$1"
    log "💾 Committing changes..."

    git add ../../config >/dev/null 2>&1

    if git diff --cached --quiet; then
        log "ℹ️  No changes to commit"
        return 1
    fi

    git commit -m "$commit_message" >/dev/null 2>&1 || {
        log "❌ Failed to commit changes"
        exit 1
    }

    log "✅ Changes committed successfully"
    return 0
}

push_branch() {
    local branch_name="$1"
    log "📤 Pushing branch: $branch_name"

    git push -u origin "$branch_name" >/dev/null 2>&1 || {
        log "❌ Failed to push branch: $branch_name"
        exit 1
    }

    log "✅ Branch pushed successfully"
}

create_pull_request() {
    local commit_message="$1"
    log "🔀 Creating pull request..."

    local pr_title
    local pr_body

    pr_title=$(echo "$commit_message" | head -n 1)
    pr_body=$(echo "$commit_message" | tail -n +2)

    if [[ -n "$pr_body" ]]; then
        pr_body="$pr_body

$AUTOMATION_CREDIT"
    else
        pr_body="$AUTOMATION_CREDIT"
    fi

    local pr_url
    pr_url=$(gh pr create --title "$pr_title" --body "$pr_body" 2>/dev/null) || {
        log "❌ Failed to create pull request"
        exit 1
    }

    log "✅ Pull request created: $pr_url"
    echo "$pr_url"
}

main() {
    parse_args "$@"

    log "🚀 Starting image update process..."

    if [[ "$CREATE_PR" == "true" ]]; then
        cleanup_previous_prs
    fi

    local branch_name
    branch_name=$(create_update_branch)

    local commit_message
    if ! commit_message=$(run_image_update); then
        log "ℹ️  No updates needed, cleaning up..."
        git checkout auto-image-bump >/dev/null 2>&1
        git branch -D "$branch_name" >/dev/null 2>&1
        log "✅ Process completed - no updates were necessary"
        exit 0
    fi

    run_config_materialize

    if ! commit_changes "$commit_message"; then
        log "ℹ️  No changes to commit after materialization, cleaning up..."
        git checkout auto-image-bump >/dev/null 2>&1
        git branch -D "$branch_name" >/dev/null 2>&1
        log "✅ Process completed - no changes to commit"
        exit 0
    fi

    push_branch "$branch_name"

    if [[ "$CREATE_PR" == "true" ]]; then
        local pr_url
        pr_url=$(create_pull_request "$commit_message")
        log "🎉 Image update process completed successfully!"
        log "   PR: $pr_url"
    else
        log "🎉 Image update process completed successfully!"
        log "   Branch: $branch_name (PR creation skipped)"
    fi
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi