#!/usr/bin/env bash

set -euo pipefail

base=${GITHUB_BASE_REF:-main}
repo=${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}
bot='aro-hcp-robot[bot]'
title='chore(gh-aw): [auto] upgrade agentic workflows (AROSLSRE-2267)'

repair_generated_agent() {
  local agent=.github/agents/agentic-workflows.agent.md
  local generated=.github/agents/agentic-workflows.md
  local version version_output commit
  if [[ -f $generated ]]; then
    mv "$generated" "$agent"
  fi
  if [[ ! -f $agent ]]; then
    echo "gh-aw did not generate its dispatcher agent." >&2
    exit 1
  fi

  if ! version_output=$(gh aw version 2>&1); then
    echo "gh aw version failed: $version_output" >&2
    exit 1
  fi
  if [[ ! $version_output =~ v[0-9]+\.[0-9]+\.[0-9]+ ]]; then
    echo "Cannot determine installed gh-aw version." >&2
    exit 1
  fi
  version=${BASH_REMATCH[0]}
  commit=$(gh api "repos/github/gh-aw/commits/$version" --jq '.sha')
  [[ $commit =~ ^[0-9a-f]{40}$ ]] || { echo "Cannot pin gh-aw prompts to a commit." >&2; exit 1; }
  sed -E -i "s@raw\.githubusercontent\.com/github/gh-aw/(main|refs/heads/main|[0-9a-f]{40})/@raw.githubusercontent.com/github/gh-aw/$commit/@g" "$agent"
  if grep -Eo 'raw\.githubusercontent\.com/github/gh-aw/(refs/heads/)?[^/]+/' "$agent" |
    grep -Fvx "raw.githubusercontent.com/github/gh-aw/$commit/"; then
    echo "An unpinned gh-aw prompt reference remains in $agent." >&2
    exit 1
  fi
}

check_upgrade_scope() {
  local committed staged
  committed=$(git diff --name-only "origin/$base"...HEAD)
  staged=$(git diff --cached --name-only)
  if { [[ -n $committed ]] && grep -v '^\.github/' <<<"$committed" >/dev/null; } ||
    { [[ -n $staged ]] && grep -v '^\.github/' <<<"$staged" >/dev/null; }; then
    echo "Upgrade changed files outside .github; refusing to publish." >&2
    exit 1
  fi
}

if [[ ${1:-} == --repair-only ]]; then
  repair_generated_agent
  exit 0
fi

prs=$(gh pr list --repo "$repo" --state open --author app/aro-hcp-robot \
  --limit 500 --json number,headRefName,author)
matches=$(jq -c '[.[] | select(
  (.author.login == "app/aro-hcp-robot" or .author.login == "aro-hcp-robot[bot]") and
  (.headRefName | test("^upgrade-agentic-workflows-[a-zA-Z0-9_-]+$"))
)]' <<<"$prs")
count=$(jq 'length' <<<"$matches")
if [[ -n ${EXPECTED_UPGRADE_PR:-} ]] &&
  { (( count != 1 )) || [[ $(jq -r '.[0].number' <<<"$matches") != "$EXPECTED_UPGRADE_PR" ]]; }; then
  echo "The requested upgrade PR is no longer the unique open target." >&2
  exit 1
fi
if (( count > 1 )); then
  echo "More than one open upgrade PR matches; refusing to choose a branch." >&2
  exit 1
fi

if (( count == 1 )); then
  number=$(jq -r '.[0].number' <<<"$matches")
  branch=$(jq -r '.[0].headRefName' <<<"$matches")
  pr=$(gh api "repos/$repo/pulls/$number")
  jq -e --arg bot "$bot" --arg repo "$repo" --arg base "$base" --arg branch "$branch" --arg title "$title" \
    '.state == "open" and .title == $title and .user.login == $bot and .head.repo.full_name == $repo and .head.ref == $branch and .base.ref == $base' <<<"$pr" >/dev/null ||
    { echo "Upgrade PR identity, repository, title, or base does not match." >&2; exit 1; }
  expected_head=${EXPECTED_UPGRADE_HEAD:-$(jq -r '.head.sha' <<<"$pr")}
  if [[ ${1:-} == --select-only ]]; then
    printf '%s %s\n' "$number" "$branch"
    exit 0
  fi
else
  number=''
  branch="upgrade-agentic-workflows-${GITHUB_RUN_ID:?GITHUB_RUN_ID is required}"
  if [[ ${1:-} == --select-only ]]; then
    printf 'new %s\n' "$branch"
    exit 0
  fi
fi

# Use the default-branch helper even after switching to an untrusted PR tree.
trusted_guard=$(mktemp)
trap 'rm -f "$trusted_guard"' EXIT
cp "$(dirname "${BASH_SOURCE[0]}")/../.github/scripts/agentic_pr_maintenance.py" "$trusted_guard"
target_version=${TARGET_GH_AW_VERSION:-}
if [[ -z $target_version ]]; then
  version_output=$(gh aw version 2>&1)
  [[ $version_output =~ v[0-9]+\.[0-9]+\.[0-9]+ ]] ||
    { echo "Cannot determine installed gh-aw version." >&2; exit 1; }
  target_version=${BASH_REMATCH[0]}
fi
[[ $target_version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
  { echo "Invalid target compiler release." >&2; exit 1; }
ref="origin/$base"
if [[ -n $number ]]; then
  git fetch origin "refs/heads/$branch:refs/remotes/origin/$branch"
  ref="origin/$branch"
  [[ $(git rev-parse "$ref") == "$expected_head" ]] ||
    { echo "Upgrade branch no longer matches the expected head." >&2; exit 1; }
  changed=$(git diff --name-only "origin/$base...$ref")
  if [[ -n $changed ]] && grep -v '^\.github/' <<<"$changed" >/dev/null; then
    echo "Upgrade changed files outside .github; refusing to publish." >&2
    exit 1
  fi
fi
versions=$(python3 "$trusted_guard" compiler-versions "$ref")
if jq -e --arg version "$target_version" 'length == 1 and .[0] == $version' <<<"$versions" >/dev/null; then
  echo "Compiler $target_version is current; no regeneration or PR mutation."
  exit 0
fi

if [[ -n $number ]]; then
  python3 "$trusted_guard" verify-upgrade "$repo" "$number" "$expected_head"
  git switch --detach "$ref"
  git switch -c "$branch"
  check_upgrade_scope
  if ! git merge --no-edit "origin/$base"; then
    git merge --abort
    # Retain the reviewed PR history while regenerating from trusted main.
    git merge --no-commit --no-ff -s ours "origin/$base"
    git restore --source="origin/$base" --staged --worktree -- .
  fi
else
  git switch -c "$branch"
fi

gh aw upgrade --yes
repair_generated_agent
gh aw compile --strict

make yamlfmt
git add -A
check_upgrade_scope
if git diff --cached --quiet; then
  echo "No upgrade changes."
  # A real merge may already have produced the new compiler content.
  if [[ -z $number || $(git rev-parse HEAD) == "$expected_head" ]]; then
    exit 0
  fi
fi
if ! git diff --cached --quiet; then
  git commit -m "$title"
fi

make verify-yamlfmt
make -C config detect-change
if [[ -n $(git status --porcelain) ]]; then
  echo "Verification left uncommitted changes; refusing to publish." >&2
  exit 1
fi

cat > /tmp/gh-aw-upgrade-pr.md <<'EOF'
[AROSLSRE-2267](https://redhat.atlassian.net/browse/AROSLSRE-2267)

### What

Upgrade the agentic workflow compiler, refresh its generated workflows and action pins, and keep them pinned to the reviewed release. The recompiler reuses only a uniquely identified aro-hcp-robot upgrade PR, repairs the dispatcher agent and its prompt references, and rejects ambiguous or unrelated PRs.

### Why

The gh-aw runtime can reject workflows compiled by a blocked release, leaving scheduled automation unable to run.

### Testing

The recompiler runs strict workflow compilation, `make verify-yamlfmt` and `make -C config detect-change` before publishing. Required CI and the repository's existing bot merge policy still gate merge.

### Special notes for your reviewer

This PR is generated by aro-hcp-robot and opens ready for review after local validation. Review workflow permissions and the generated safe-output protected-file list, including CHANGELOG.md. It does not bypass repository merge gates.

### PR Checklist

- [x] PR is scoped to a single task (no mixed concerns)
- [x] Title follows Conventional Commits format
- [x] Summary explains the why behind the change
- [x] Linked to relevant ticket/issue
- [ ] Self-reviewed the diff
- [x] Local generated-content checks gate publication
- [ ] CI/CD checks are passing (ignore Tide)
- [ ] All comment threads resolved before merge
EOF

if [[ -n $number ]]; then
  python3 "$trusted_guard" verify-upgrade "$repo" "$number" "$expected_head"
fi
git push origin "HEAD:refs/heads/$branch"
if [[ $(gh api "repos/$repo/git/ref/heads/$branch" --jq '.object.sha') != "$(git rev-parse HEAD)" ]]; then
  echo "Published upgrade head does not match the verified commit." >&2
  exit 1
fi
if [[ -n $number ]]; then
  gh pr edit "$number" --repo "$repo" --title "$title" --body-file /tmp/gh-aw-upgrade-pr.md
  new_head=$(git rev-parse HEAD)
  # New compiler content gets fresh CI without recycling an unchanged-head budget.
  python3 "$trusted_guard" release-regenerated "$repo" "$number" "$new_head" "$expected_head"
else
  gh pr create --repo "$repo" --base "$base" --head "$branch" \
    --title "$title" --body-file /tmp/gh-aw-upgrade-pr.md
fi
