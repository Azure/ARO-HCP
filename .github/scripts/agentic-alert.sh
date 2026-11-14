#!/usr/bin/env bash
# Maintain one rolling issue for failing agentic workflows.
#
# A workflow's state is the latest completed run that actually reflects a
# result: cancelled and skipped runs (for example after a force push) do not
# change it, so they can neither create nor clear an alert.
set -euo pipefail

title='[agentic-alert] Agentic workflow failures'
label='agentic-alert'
now=$(date -u +%Y-%m-%dT%H:%MZ)
failing=''
unknown=0
workflows=(tool-version-updates.lock.yml gh-aw-recompile.lock.yml dependabot-remediation.lock.yml)

for workflow in "${workflows[@]}"; do
  # Page the whole completed history: a short window can hide an older failure
  # behind a long run of cancelled or skipped runs.
  runs=$(gh api --paginate --slurp \
    "repos/$REPO/actions/workflows/$workflow/runs?status=completed&per_page=100")
  picked=$(jq -c '[.[][]] | map(select(.conclusion != "cancelled" and .conclusion != "skipped")) | .[0] // empty' <<<"$runs")
  if [ -z "$picked" ]; then
    # No run ever produced a result: its state is unknown, so do not close.
    unknown=1
    continue
  fi
  conclusion=$(jq -r '.conclusion // ""' <<<"$picked")
  case "$conclusion" in
    failure|timed_out|startup_failure|action_required) ;;
    *) continue ;;
  esac
  run_id=$(jq -r '.id' <<<"$picked")
  head=$(jq -r '.head_sha // ""' <<<"$picked")
  name=$(gh api "repos/$REPO/actions/workflows/$workflow" | jq -r '.name')
  failing+="| [$name](https://github.com/$REPO/actions/runs/$run_id) | \`${head:0:7}\` |"$'\n'
done

number=$(gh issue list --repo "$REPO" --label "$label" --state open --limit 1 \
  --json number | jq -r '.[0].number // empty')
if [ -z "$failing" ]; then
  # Unknown history must not be reported as a recovery.
  [ "$unknown" = '0' ] || exit 0
  if [ -n "$number" ]; then
    printf 'All tracked agentic workflows are green as of %s.\n' "$now" \
      | gh issue comment "$number" --repo "$REPO" --body-file -
    gh issue close "$number" --repo "$REPO"
  fi
  exit 0
fi

body=$(printf 'Rolling issue for failing agentic workflows. The body is refreshed on every workflow completion, so it always shows the latest failure per workflow.\n\n| Workflow | Head |\n| --- | --- |\n%s\nUpdated %s.\n' "$failing" "$now")
if [ -n "$number" ]; then
  gh issue edit "$number" --repo "$REPO" --body "$body"
else
  gh issue create --repo "$REPO" --title "$title" --label "$label" --body "$body"
fi
