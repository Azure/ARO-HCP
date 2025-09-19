#!/bin/bash
set -euo pipefail
# Features:
# - Create PR can be toggled for local testing
# - Opens a PR
# - Closes previous PRs
# - Creates a branch named image-update-<date>
# - Runs `make update`, greps for the commit message output
# - Runs make -C ../../config materialize
# - commits changes with commit message output
# - pushes branch
# - creates PR
#   - title of PR is the first line of the commit message
#   - body is the remaining lines of the commit message
#   - last line in the body says "Automatically updated with [Image Digest Updater](link to workflow)"
#
# - PR doesnt get created unless `make update` returns "commit message" output


CREATE_PR=false

if [ -n "${GITHUB_SERVER_URL:-}" ] && [ -n "${GITHUB_REPOSITORY:-}" ] && [ -n "${GITHUB_RUN_ID:-}" ]; then
    WORKFLOW_URL="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}"
    AUTOMATION_CREDIT="Automatically updated with [Image Digest Updater](${WORKFLOW_URL})"
else
    # Fallback for local runs or when GitHub env vars are not available
    AUTOMATION_CREDIT="Automatically updated with Image Digest Updater"
fi

log() {
    echo "[$(date +'%H:%M:%S')] $1"
}

usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Bulk update all component image digests and create a single PR.

OPTIONS:
    --no-pr     Skip PR creation (useful for local testing)
    -h, --help  Show this help message

EXAMPLES:
    $0                  # Update all components and create PR
    $0 --no-pr         # Update all components but don't create PR
EOF
}

parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
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

    # Find PRs with "updated image components" in the title (created by this script)
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
    local branch_name="image-update-$(get_current_date)"
    local current_branch
    current_branch=$(git branch --show-current)

    log "🌱 Creating branch: $branch_name from current branch ($current_branch)"

    # Delete branch if it exists locally
    if git show-ref --verify --quiet "refs/heads/$branch_name"; then
        log "   Deleting existing local branch: $branch_name"
        git branch -D "$branch_name" >/dev/null 2>&1
    fi

    # Create and switch to new branch from current HEAD
    git checkout -b "$branch_name" >/dev/null 2>&1 || {
        log "❌ Failed to create branch: $branch_name"
        exit 1
    }

    echo "$branch_name"
}

run_image_update() {
    log "🔄 Running image update..."

    # Capture both stdout and stderr, and the exit code
    local output
    local exit_code

    output=$(make update 2>&1)
    exit_code=$?

    if [[ $exit_code -ne 0 ]]; then
        log "❌ make update failed with exit code $exit_code"
        echo "$output"
        exit 1
    fi

    # Check if output contains commit message
    if echo "$output" | grep -q "=== COMMIT MESSAGE ==="; then
        log "✅ Image updates found"
        echo "$output" | sed -n '/=== COMMIT MESSAGE ===/,$ p' | tail -n +2
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

    # Add all changes
    git add . >/dev/null 2>&1

    # Check if there are any changes to commit
    if git diff --cached --quiet; then
        log "ℹ️  No changes to commit"
        return 1
    fi

    # Commit with the provided message
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

    # Extract title (first line) and body (remaining lines)
    local pr_title
    local pr_body

    pr_title=$(echo "$commit_message" | head -n 1)
    pr_body=$(echo "$commit_message" | tail -n +2)

    # Append automation credit to body
    if [[ -n "$pr_body" ]]; then
        pr_body="$pr_body

$AUTOMATION_CREDIT"
    else
        pr_body="$AUTOMATION_CREDIT"
    fi

    # Create the PR
    local pr_url
    pr_url=$(gh pr create --title "$pr_title" --body "$pr_body" 2>/dev/null) || {
        log "❌ Failed to create pull request"
        exit 1
    }

    log "✅ Pull request created: $pr_url"
    echo "$pr_url"
}

main() {
    # Parse command line arguments first
    parse_args "$@"

    log "🚀 Starting image update process..."

    # Only cleanup previous PRs if we're going to create a new one
    if [[ "$CREATE_PR" == "true" ]]; then
        cleanup_previous_prs
    fi

    # Create update branch
    local branch_name
    branch_name=$(create_update_branch)

    # Run image update and capture commit message
    local commit_message
    if ! commit_message=$(run_image_update); then
        log "ℹ️  No updates needed, cleaning up..."
        git checkout auto-image-bump >/dev/null 2>&1
        git branch -D "$branch_name" >/dev/null 2>&1
        log "✅ Process completed - no updates were necessary"
        exit 0
    fi

    # Run config materialization
    run_config_materialize

    # Commit changes
    if ! commit_changes "$commit_message"; then
        log "ℹ️  No changes to commit after materialization, cleaning up..."
        git checkout auto-image-bump >/dev/null 2>&1
        git branch -D "$branch_name" >/dev/null 2>&1
        log "✅ Process completed - no changes to commit"
        exit 0
    fi

    # Push branch
    push_branch "$branch_name"

    # Create PR if requested
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

# Only run main if script is executed directly (not sourced)
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi