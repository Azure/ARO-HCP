#!/usr/bin/env bash

set -euo pipefail

root=$(git rev-parse --show-toplevel)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cat > "$tmp/gh" <<'EOF'
#!/usr/bin/env bash
case "$1 $2" in
  'pr list') printf '%s\n' "$MOCK_PR_LIST" ;;
  'api repos/Azure/ARO-HCP/pulls/7184') printf '%s\n' "$MOCK_PR_DETAIL" ;;
  'aw version') echo 'gh aw version v0.89.21' ;;
  'api repos/github/gh-aw/commits/v0.89.21') echo 'c35393777e5604a63721d09512263b1383301d4f' ;;
  *) echo "Unexpected gh invocation: $*" >&2; exit 1 ;;
esac
EOF
chmod +x "$tmp/gh"
export PATH="$tmp:$PATH" GITHUB_REPOSITORY=Azure/ARO-HCP GITHUB_RUN_ID=42 GITHUB_BASE_REF=main
export MOCK_PR_DETAIL='{"state":"open","user":{"login":"aro-hcp-robot[bot]"},"head":{"repo":{"full_name":"Azure/ARO-HCP"},"ref":"upgrade-agentic-workflows-1971"},"base":{"ref":"main"}}'
export MOCK_PR_LIST='[{"number":7184,"headRefName":"upgrade-agentic-workflows-1971","author":{"login":"app/aro-hcp-robot"}}]'
script="$root/hack/gh-aw-recompile.sh"

[[ $(bash "$script" --select-only) == '7184 upgrade-agentic-workflows-1971' ]]
MOCK_PR_LIST='[]'
export MOCK_PR_LIST
[[ $(bash "$script" --select-only) == 'new upgrade-agentic-workflows-42' ]]

MOCK_PR_LIST='[{"number":7184,"headRefName":"upgrade-agentic-workflows-1971","author":{"login":"someone-else"}},{"number":7183,"headRefName":"fix-something","author":{"login":"app/aro-hcp-robot"}}]'
export MOCK_PR_LIST
[[ $(bash "$script" --select-only) == 'new upgrade-agentic-workflows-42' ]]

MOCK_PR_LIST='[{"number":7184,"headRefName":"upgrade-agentic-workflows-1971","author":{"login":"app/aro-hcp-robot"}},{"number":7183,"headRefName":"upgrade-agentic-workflows-1972","author":{"login":"app/aro-hcp-robot"}}]'
export MOCK_PR_LIST
if bash "$script" --select-only > /dev/null 2>&1; then
  echo "Multiple matching PRs must be rejected." >&2
  exit 1
fi

MOCK_PR_LIST='[{"number":7184,"headRefName":"upgrade-agentic-workflows-1971","author":{"login":"app/aro-hcp-robot"}}]'
MOCK_PR_DETAIL='{"state":"open","user":{"login":"aro-hcp-robot[bot]"},"head":{"repo":{"full_name":"someone-else/ARO-HCP"},"ref":"upgrade-agentic-workflows-1971"},"base":{"ref":"main"}}'
export MOCK_PR_LIST MOCK_PR_DETAIL
if bash "$script" --select-only > /dev/null 2>&1; then
  echo "A fork PR must be rejected." >&2
  exit 1
fi

mkdir -p "$tmp/repo/.github/agents"
printf '%s\n' \
  'https://raw.githubusercontent.com/github/gh-aw/main/.github/aw/create-agentic-workflow.md' \
  'https://raw.githubusercontent.com/github/gh-aw/refs/heads/main/.github/aw/debug-agentic-workflow.md' \
  'https://raw.githubusercontent.com/github/gh-aw/0123456789abcdef0123456789abcdef01234567/.github/aw/update-agentic-workflow.md' \
  > "$tmp/repo/.github/agents/agentic-workflows.md"
(
  cd "$tmp/repo"
  bash "$script" --repair-only
  [[ ! -f .github/agents/agentic-workflows.md ]]
  [[ $(grep -c 'github/gh-aw/c35393777e5604a63721d09512263b1383301d4f/' \
    .github/agents/agentic-workflows.agent.md) == 3 ]]
  if grep -Eq 'github/gh-aw/(main|refs/heads/main|0123456789abcdef0123456789abcdef01234567)/' \
    .github/agents/agentic-workflows.agent.md
  then
    echo "Stale gh-aw prompt reference remains." >&2
    exit 1
  fi
  rm .github/agents/agentic-workflows.agent.md
  if bash "$script" --repair-only >/dev/null 2>&1; then
    echo "Missing agent must be rejected." >&2
    exit 1
  fi
)
echo "gh-aw recompile tests passed"
