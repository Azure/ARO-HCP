---
on:
  workflow_dispatch:
  schedule: daily

permissions:
  contents: read
  pull-requests: read
  copilot-requests: write

engine: copilot
model: sonnet
timeout-minutes: 120

env:
  GOTOOLCHAIN: local

concurrency:
  group: agentic-tool-version-updates
  cancel-in-progress: false

network:
  allowed:
    - defaults
    - go
    - api.github.com
    - github.com
    - release-assets.githubusercontent.com

steps:
  - name: Checkout
    uses: actions/checkout@v7.0.1
    with:
      persist-credentials: false
  - name: Set up Go from go.work
    uses: actions/setup-go@v7.0.0
    with:
      go-version-file: go.work
      check-latest: true
  - name: Bootstrap the repository-pinned Bingo
    env:
      GOTOOLCHAIN: local
    run: |
      set -euo pipefail
      version="$(GOWORK=off go -C .bingo list -m -modfile=bingo.mod -f '{{.Version}}' github.com/bwplotka/bingo)"
      GOWORK=off go install "github.com/bwplotka/bingo@$version"
  - name: Read open pull requests
    env:
      GH_TOKEN: ${{ github.token }}
      REPO: ${{ github.repository }}
    run: |
      set -euo pipefail
      printf '%s\n' tool-version-open-prs.json >> "$(git rev-parse --git-path info/exclude)"
      gh api --paginate "repos/$REPO/pulls?state=open&per_page=100" \
        --jq '.[] | {number, title, body, author: .user.login, base: .base.ref, head: .head.ref, labels: [.labels[].name]}' \
        | jq -s '.' > tool-version-open-prs.json

safe-outputs:
  github-app:
    client-id: ${{ secrets.DEPENDABOT_APP_CLIENT_ID }}
    private-key: ${{ secrets.DEPENDABOT_APP_PRIVATE_KEY }}
  noop:
    report-as-issue: false
  steps:
    - name: Serialize tooling proposals before creation
      if: contains(needs.agent.outputs.output_types, 'create_pull_request')
      env:
        GH_TOKEN: ${{ steps.safe-outputs-app-token.outputs.token }}
        REPO: ${{ github.repository }}
        AGENT_OUTPUT: ${{ steps.setup-agent-output-env.outputs.GH_AW_AGENT_OUTPUT }}
      run: python3 .github/scripts/tool_version_guard.py "$AGENT_OUTPUT" "$REPO"
  create-pull-request:
    max: 1
    draft: false
    fallback-as-issue: false
    auto-close-issue: false
    title-prefix: "chore(tools): "
    labels: [dependencies, agentic-tool-upgrade]
    allowed-files:
      - .bingo/*.mod
      - .bingo/*.sum
      - .bingo/Variables.mk
      - .bingo/variables.env
      - dev-infrastructure/openshift-ci/versions.mk
      - dev-infrastructure/openshift-ci/Dockerfile
    protected-files:
      policy: blocked
      exclude:
        - .bingo/
        - .bingo/*.mod
        - .bingo/*.sum
        - .bingo/Variables.mk
        - .bingo/variables.env
        - dev-infrastructure/openshift-ci/versions.mk
        - dev-infrastructure/openshift-ci/Dockerfile
---

# Agentic CI and development tool updates

Keep the existing pinned CI and development tools current. Use one upstream
family per PR and no more than one open `agentic-tool-upgrade` PR. Bingo's
shared generated helpers and the CI image's shared version file must not grow
competing update branches. PRs are ready for the repository's existing CI/Tide
auto-merge path; do not request reviewers, add approvals, bypass checks, or
change merge policy.

Immediately before publication, the trusted guard derives changed paths from
the proposal's patch and bundle artifacts and rechecks every open PR targeting
`main`, regardless of author or labels. It defers overlapping proposals, including
shared Bingo helpers and the CI image version/Dockerfile pair, and fails closed
when an artifact or PR file inventory is incomplete.

## Inspect every pin before selecting work

1. Read `tool-version-open-prs.json`. If an open PR by `aro-hcp-robot[bot]` on
   `main` has `agentic-tool-upgrade`, emit `noop` identifying that PR and stop.
   Waiting for CI or an existing PR is not missing data and does not need an issue.
2. Inspect both tools in `dev-infrastructure/openshift-ci/versions.mk` and every
   `.bingo/*.mod` tool pin. `.bingo/go.mod` is only Bingo's fake root, not a tool.
   Use `bingo list` and the module files; do not maintain a second hardcoded list.
3. Resolve official stable releases: GitHub releases for bicep/promtool and
   `GOWORK=off GOTOOLCHAIN=local go -C .bingo list -m -json <module>@latest`
   for Bingo's upstream modules. Record an explicit target version before edits.
   Never downgrade, select prereleases, cross an import-path major version, or
   change the Go toolchain to make an otherwise unsupported release build.
4. Group Bingo tools by their required upstream module. In particular, all five
   `k8s.io/code-generator` tools belong to one version-coherent update. The Bingo
   updater itself is a separate family. Prefer the oldest pinned release among
   eligible families so frequently released tools do not starve other tools.
5. Inspect actual changed files of relevant open bot and human PRs using the
   read-only GitHub tools. Do not take over, replace, or compete with existing
   updates. Shared Bingo helper changes overlap other Bingo updates; CI image
   version changes overlap other changes to the same version/Dockerfile pair.
   Defer an overlapping family and continue inspecting independent families.

Try no more than two eligible candidate families in one run, prioritizing the
oldest pinned release. For each family, create a temporary detached worktree at
`origin/main` and run its selected gate there first with the currently pinned
executable. Then run the same gate on the candidate in the same runner
environment with the proposed executable; keep the baseline worktree separate
from candidate scratch changes. Use the repository-pinned jq for both Bicep
fixture comparisons. If the baseline gate fails, classify the family as blocked
by the runner/environment. If the baseline passes and the candidate fails,
classify it as blocked by a regression. Restore scratch changes in both cases
and try the next eligible family within the two-family limit.

## Prepare one verified family

Use a clean branch from `main`. Keep `GOTOOLCHAIN=local` while updating and
building tools, so a new upstream compiler requirement is a blocked family,
not an implicit toolchain upgrade. If a family cannot pass its real checks,
restore its scratch changes and try the next eligible family; never publish an
unverified bump or suppress a genuine infrastructure/authentication error.

- **Bicep or promtool:** run
  `dev-infrastructure/openshift-ci/update-tool-versions.sh BICEP_VERSION` or
  `dev-infrastructure/openshift-ci/update-tool-versions.sh PROMTOOL_VERSION`.
  This keeps only the selected variable and Dockerfile ARG in sync. Confirm the
  result equals the resolved stable target, then run
  `make -C dev-infrastructure/openshift-ci verify`.
  Download the exact proposed release's Linux amd64 binary to a temporary
  directory and verify its reported version. For Bicep, run
  `BICEP=/absolute/path/to/pinned/bicep make verify-bicep-fixtures` on the
  unmodified baseline, then run it with the proposed executable on the candidate.
  Both runs use the repository-pinned jq executable. For promtool, prepend the
  current pinned binary's directory to PATH for the baseline, then the proposed
  binary's directory for the candidate, and run
  `go -C tooling/prometheus-rules test -count=1 ./...`.
  These consumer tests exercise PrometheusRule conversion and rule tests with
  the proposed binary; raw Kubernetes CRDs are not direct promtool inputs.
  Do not rewrite golden fixtures or rule tests to accept a regression. These
  Dockerfile changes also select the existing Prow baseimage-generator image
  build and Go smoke test before merge.
  Official release API requests and binary redirects are restricted to the
  declared GitHub release hosts; retain network isolation when changing them.
- **Bingo:** use the currently pinned Bingo to run
  `bingo get <existing-tool>@<resolved-version>` for every tool in the selected
  upstream family. Do not hand-edit generated module, checksum or helper files.
  Native Bingo regenerates `Variables.mk` and `variables.env`; retain equivalent
  host-platform GOHOSTOS/GOHOSTARCH/GOHOSTARM settings and their use in builds.
  Leave `Symlinks.mk` unchanged. For a Bingo self-update, use the new pinned
  executable to regenerate its own helpers as well. Build each changed tool
  using its generated Make target. Commit the candidate before the clean-tree
  checks, then run `make verify` and `make lint` with the proposed tools.
  Existing `.bingo/` path selectors also select the CI image build; the standard
  Prow verify/lint jobs consume the repository's other pinned tools before merge.

Before publishing, confirm the diff contains only the selected family's pins,
checksums and generated helpers, or the selected CI tool's variable/ARG pair.
Do not change builder images, production dependencies, workflow files, source,
fixtures, or unrelated pins. Commit the candidate before running
`make verify-yamlfmt` and `make -C config detect-change`, and exclude all scratch
files and binaries. A check that fails only because the intended proposal is
uncommitted is not a tool regression; keep the proposal committed for those gates.

Create one PR titled `bump <upstream family> to <version>`, using the repository
PR template with the actual old/new versions and exercised regression checks.
Record the family with `<!-- agentic-tool-family: <upstream module or repo> -->`
in the body. Do not claim local checks prove the later Prow jobs succeeded.

If candidate checks are blocked, report one aggregated `report_incomplete` with
each family, gate, baseline/candidate classification, and the concise failure
signature. Keep already-current or already-covered families silent. If all
families are current or already covered and there are no blocked candidates,
call `noop` with a concise inspection summary. Do not create routine reports.
