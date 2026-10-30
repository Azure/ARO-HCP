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
  - name: Collect deterministic tool-family inventory
    env:
      GH_TOKEN: ${{ github.token }}
    run: |
      set -euo pipefail
      printf '%s\n' tool-version-inventory.json >> "$(git rev-parse --git-path info/exclude)"
      python3 .github/scripts/tool_version_inventory.py > tool-version-inventory.json
  - name: Bootstrap the repository-pinned Bingo
    env:
      GOTOOLCHAIN: local
    run: |
      set -euo pipefail
      version="$(GOWORK=off go -C .bingo list -m -modfile=bingo.mod -f '{{.Version}}' github.com/bwplotka/bingo)"
      GOWORK=off go install "github.com/bwplotka/bingo@$version"
  - name: Read agent-owned open tool-update candidates
    env:
      GH_TOKEN: ${{ github.token }}
      REPO: ${{ github.repository }}
    run: |
      set -euo pipefail
      printf '%s\n' tool-version-open-prs.json >> "$(git rev-parse --git-path info/exclude)"
      gh api --paginate \
        "repos/$REPO/issues?state=open&creator=aro-hcp-robot%5Bbot%5D&per_page=100" \
        --jq '.[] | if .user.login != "aro-hcp-robot[bot]" or .state != "open" or (.labels | type) != "array" then error("Invalid owned PR metadata") else select(.pull_request != null) | {number, title, body, author: .user.login, labels: [.labels[].name]} end' \
        | jq -s '.' > tool-version-open-prs.json

safe-outputs:
  # Keep failure diagnostics in Actions logs and artifacts, not GitHub issues.
  # External alerting can monitor failed runs without expanding the App's permissions.
  report-failure-as-issue: false
  report-failed-jobs: false
  report-missing-tool:
    create-issue: false
  report-incomplete:
    create-issue: false
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
family per PR and no more than one open agent-owned tool-update PR targeting
`main`. Serialize these proposals so shared generated helpers and CI image pins
do not grow competing agent branches. PRs are ready for the repository's existing CI/Tide
auto-merge path; do not request reviewers, add approvals, bypass checks, or
change merge policy.

Immediately before publication, the trusted guard rechecks open PRs targeting
`main`, filtered at the API to `aro-hcp-robot[bot]`. Only that owner's tool-update
PRs participate: identify them by `agentic-tool-upgrade`, or by the tool-update
title and `<!-- agentic-tool-family: ... -->` body marker when the label is absent.
An existing owned tool-update PR defers every new family until it closes.
Other workflows and Dependabot PRs do not block solely because they share the
owner. Fail closed when API pagination or ownership metadata is incomplete.
Do not scan PR file inventories, patches, bundles or file trees for coordination.
Human and unrelated PR conflicts are left to normal development; they must not
block this daily automation.

## Inspect every pin before selecting work

1. Read `tool-version-open-prs.json`, containing only open PR candidates by
   `aro-hcp-robot[bot]`. Validate the owner and apply the same label or
   title-and-family-marker ownership rule as the publication guard. Fetch only
   matching tool-update PR metadata with the read-only GitHub tools to confirm
   the PR is still open and targets `main`. If one exists, defer all new work
   regardless of family, but still read the inventory below before reporting.
   Ignore unrelated bot PRs; do not fetch human PRs or any PR filenames. Waiting
   for CI or an owned tool-update PR alone is not missing data and needs no issue.
2. Read `tool-version-inventory.json`, produced by the read-only preflight from
   the updater's CI tool mapping and every `.bingo/*.mod` tool pin except the fake
   `go.mod` root. Its `families` rows group shared upstream modules, with
   `toolNames`, per-tool `pins`, `sourceURL`, `current`, `latest`, `status` and
   `reason`. Do not maintain another tool list or redo version classification
   from memory, string ordering, or an individual lookup.
3. Use this machine output as the authority for every version classification in
   selection, PR bodies and final summaries: only `status: current` means
   current; `outdated` means an update exists; `ahead` means never downgrade.
   `error`, missing rows/data, or `complete: false` are missing-data failures,
   never evidence that tools are current. Report their explicit reasons using
   `report_incomplete`; do not hide them behind a successful update or `noop`.
   `blocked` rows are ineligible and must retain their reason. An outdated
   family not attempted because of single-flight, the two-family limit, or a
   failed gate is deferred/blocked, not current. Keep version status separate
   from execution outcomes.
4. Select only `outdated` rows and use their exact `latest` target versions.
   Never select prereleases, cross an import-path major version, or change the
   Go toolchain to make an otherwise unsupported release build. All tools in
   one row, including the `k8s.io/code-generator` family, update together.
   Prefer the oldest pinned release among eligible families so frequently
   released tools do not starve other tools.

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

If inventory data or candidate checks are blocked, report one aggregated
`report_incomplete` with each family, gate, baseline/candidate classification,
and concise failure signature. A required gate not run is unverified, not a
pass; runner OOMs and failed baseline checks remain explicit blocked outcomes.
Keep successfully inspected current or covered families silent. Use `noop`
only when there is no missing data or blocked candidate; summarize versions
from the inventory and identify waiting/deferred work separately. Do not
create routine reports for single-flight waiting alone.
