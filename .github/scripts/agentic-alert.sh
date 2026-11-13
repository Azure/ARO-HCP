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
known=0
workflows=(tool-version-updates.lock.yml gh-aw-recompile.lock.yml dependabot-remediation.lock.yml)

for workflow in "${workflows[@]}"; do
  runs=$(gh run list --repo "$REPO" --workflow "$workflow" --status completed --limit 20 \
    --json databaseId,conclusion,headSha)
  picked=$(jq -c 'map(select(.conclusion != "cancelled" and .conclusion != "skipped"))[0] // empty' <<<"$runs")
  [ -n "$picked" ] || continue
  known=1
  conclusion=$(jq -r '.conclusion // ""' <<<"$picked")
  case "$conclusion" in
    failure|timed_out|startup_failure|action_required) ;;
    *) continue ;;
  esac
  run_id=$(jq -r '.databaseId' <<<"$picked")
  head=$(jq -r '.headSha // ""' <<<"$picked")
  name=$(gh api "repos/$REPO/actions/workflows/$workflow" | jq -r '.name')
  failing+="| [$name](https://github.com/$REPO/actions/runs/$run_id) | \`${head:0:7}\` |"$'\n'
done

# With no meaningful run for any tracked workflow, leave the issue untouched.
[ "$known" = '1' ] || exit 0

number=$(gh issue list --repo "$REPO" --label "$label" --state open --limit 1 \
  --json number | jq -r '.[0].number // empty')
if [ -z "$failing" ]; then
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
