---
# Autonomous, go.work-aware Dependabot remediation for Azure/ARO-HCP.
# Reads open Dependabot alerts, groups them by package/cascade family, runs the
# `make all-tidy` workspace ritual, and opens one dependency-only PR per group.
#
# The Copilot engine runs under a secrecy/DIFC sandbox that filters private-scoped
# security data, so the agent itself cannot read Dependabot alerts through the GitHub
# MCP `dependabot` toolset (the response comes back empty and taints the agent's
# integrity label). We therefore fetch the alerts and the open PR list in ordinary
# Actions steps with an aro-hcp-robot App token (which has vulnerability-alerts: read)
# and hand the agent two JSON files in the workspace. The agent is org-billed via
# copilot-requests. A GitHub App is also used for the write side: PR creation goes
# through the safe-outputs job with an App installation token, never a PAT.

on:
  workflow_dispatch:            # manual run
  schedule: daily               # fuzzy daily sweep (scattered)

# The agent only needs to check out the repo and reach the org-billed engine. The
# alert/PR reads happen in the steps below with a scoped App token, and PR creation
# happens in the safe-outputs job with its own scoped App token.
permissions:
  contents: read
  checks: read               # read agentic PRs' check-runs for the CI rollup (default GITHUB_TOKEN, not the App)
  statuses: read             # read agentic PRs' commit statuses (Prow reports here) for the CI rollup (default GITHUB_TOKEN, not the App)
  copilot-requests: write    # org-billed engine inference via GITHUB_TOKEN, no PAT

engine: copilot

# Pin to the "sonnet" model alias instead of a specific Claude version. gh-aw/Copilot
# resolve aliases to whatever concrete sonnet-family model is currently available for
# the "agentic-workflows" integrator, so the workflow does not break every time a
# model gets retired (as happened with the hardcoded claude-sonnet-4.6 default).
model: sonnet

# Give the agent up to 45 minutes: it runs the go.work tidy ritual (module
# download + `make all-tidy` + license regeneration) across the ARO-HCP workspace
# (~30 modules plus npm manifests), which does not fit the default 20-minute budget.
timeout-minutes: 45

# The agent runs behind the AWF egress firewall. `defaults` covers base infra but
# NOT the Go module proxy or the npm registry, so `make all-tidy` cannot download the
# bumped versions and the ritual fails. The `go` and `node` ecosystem presets
# allowlist proxy.golang.org, sum.golang.org, go.dev and registry.npmjs.org. GitHub
# domains (for the workspace's own internal modules) are always allowed by default.
network:
  allowed:
    - defaults
    - go
    - node

# Runner setup before the agent starts:
#  - check out the repo (persist-credentials:false is required by gh-aw strict mode),
#  - install the Go toolchain at the version declared in the project (go.work) and
#    Node (ARO-HCP declares no Node version file, so use current LTS), so the
#    `make all-tidy` ritual matches the workspace (no hardcoded version),
#  - mint a short-lived aro-hcp-robot App token and pre-fetch the open Dependabot alerts
#    and open PRs into workspace JSON files. These steps run on the runner, outside the
#    Copilot secrecy sandbox, so they can read the private-scoped alert data the agent
#    cannot. The files are excluded from git so they never leak into a remediation PR.
steps:
  - name: Checkout
    uses: actions/checkout@v5
    with:
      persist-credentials: false
  - name: Set up Go from go.work
    uses: actions/setup-go@v5
    with:
      go-version-file: go.work
      check-latest: true          # install the newest patch of the go.work-declared Go version instead of a possibly-older cached toolchain, so the agent's `make all-tidy` output is reproducible and the PR passes the go-modules check
  - name: Set up Node
    uses: actions/setup-node@v4
    with:
      node-version: lts/*
  - name: Mint App token to read alerts and PRs
    id: read-token
    uses: actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1 # v3.2.0
    with:
      client-id: ${{ secrets.DEPENDABOT_APP_CLIENT_ID }}
      private-key: ${{ secrets.DEPENDABOT_APP_PRIVATE_KEY }}
      permission-contents: read
      permission-pull-requests: read
      permission-vulnerability-alerts: read
  - name: Pre-fetch open Dependabot alerts and open PRs
    env:
      GH_TOKEN: ${{ steps.read-token.outputs.token }}
      # The aro-hcp-robot App is not granted checks/statuses, and those are not
      # sensitive, so the CI reads use the job's default GITHUB_TOKEN (which the
      # top-level permissions block grants checks:read + statuses:read) instead.
      CI_TOKEN: ${{ github.token }}
    run: |
      set -euo pipefail
      # Keep the scratch files out of git so they never end up in a remediation PR.
      printf '%s\n' dependabot-alerts.json open-pull-requests.json open-pull-requests.base.json >> .git/info/exclude
      gh api --paginate "/repos/${{ github.repository }}/dependabot/alerts?state=open&per_page=100" \
        --jq '.[] | {number, ecosystem: .dependency.package.ecosystem, package: .dependency.package.name, manifest: .dependency.manifest_path, ghsa: .security_advisory.ghsa_id, cve: .security_advisory.cve_id, severity: .security_advisory.severity, vulnerable_range: .security_vulnerability.vulnerable_version_range, first_patched: .security_vulnerability.first_patched_version.identifier}' \
        | jq -s '.' > dependabot-alerts.json
      # Open PRs, enriched so the agent can reconcile against them (section 1b):
      # the list endpoint carries labels + author + head sha but NOT mergeable_state
      # or CI, so for our own agentic PRs we fetch each one's mergeable_state and roll
      # up its check-runs + commit statuses into a single pass/fail/pending signal.
      gh api --paginate "/repos/${{ github.repository }}/pulls?state=open&per_page=100" \
        --jq '.[] | {number, title, head: .head.ref, sha: .head.sha, draft: .draft, author: .user.login, labels: [.labels[].name]}' \
        | jq -s '.' > open-pull-requests.base.json
      jq -c '.[]' open-pull-requests.base.json | while read -r pr; do
        n=$(printf '%s' "$pr" | jq -r .number)
        sha=$(printf '%s' "$pr" | jq -r .sha)
        if printf '%s' "$pr" | jq -e '.labels | index("agentic-dependabot")' >/dev/null; then
          ms=$(gh api "/repos/${{ github.repository }}/pulls/$n" --jq '.mergeable_state' 2>/dev/null || echo unknown)
          checks=$(GH_TOKEN="$CI_TOKEN" gh api "/repos/${{ github.repository }}/commits/$sha/check-runs" --jq '[.check_runs[].conclusion]' 2>/dev/null || echo '[]')
          st=$(GH_TOKEN="$CI_TOKEN" gh api "/repos/${{ github.repository }}/commits/$sha/status" --jq '{state, total_count}' 2>/dev/null || echo '{"state":"unknown","total_count":0}')
          ss=$(printf '%s' "$st" | jq -r .state)
          sc=$(printf '%s' "$st" | jq -r .total_count)
          # Roll check-runs plus commit statuses into one signal. Only trust the
          # combined commit-status state when total_count > 0: repos that run only
          # check-runs return an empty status set that defaults to "pending", which
          # would otherwise mask a passing PR. ARO-HCP Prow reports as commit
          # statuses (total_count > 0), so those are honoured. Err on the safe side:
          # only report "passing" when we actually saw a non-failing check-run or a
          # successful commit status. If both signals are empty (no CI yet, or an API
          # read failed) fall through to "pending", never "passing", so a red PR is
          # never mistaken for healthy. Classify explicitly: a fixed set of bad
          # conclusions (failure/timed_out/cancelled/action_required/startup_failure)
          # is "failing"; only the known-good conclusions (success/neutral/skipped)
          # count toward "passing"; anything else (null/in-progress, "stale", or any
          # unknown conclusion) is treated as "pending" so it is never mistaken for
          # healthy.
          ci=$(printf '%s' "$checks" | jq -r --arg ss "$ss" --arg sc "$sc" 'def bad: (. == "failure" or . == "timed_out" or . == "cancelled" or . == "action_required" or . == "startup_failure"); def good: (. == "success" or . == "neutral" or . == "skipped"); if any(.[]; bad) or ($sc != "0" and $ss == "failure") then "failing" elif any(.[]; . == null or ((good or bad) | not)) or ($sc != "0" and $ss == "pending") then "pending" elif ((length > 0) and all(.[]; good)) or ($sc != "0" and $ss == "success") then "passing" else "pending" end')
        else
          ms="n/a"; ci="n/a"
        fi
        printf '%s' "$pr" | jq --arg ms "$ms" --arg ci "$ci" '. + {mergeable_state: $ms, ci: $ci}'
      done | jq -s '.' > open-pull-requests.json
      rm -f open-pull-requests.base.json
      echo "Fetched $(jq length dependabot-alerts.json) open alerts and $(jq length open-pull-requests.json) open PRs"

# PR creation is scoped here, not by the frontmatter permissions above. These writes are
# NOT performed with the org-billed GITHUB_TOKEN; they use a GitHub App installation token
# minted for the safe-outputs job (see below).
#
# PR creation uses Option B: the Azure org enforces "Allow GitHub Actions to create
# and approve pull requests" = OFF (org-level, the repo checkbox is greyed out and
# needs admin:org). That policy only governs the built-in GITHUB_TOKEN, so instead we
# mint an aro-hcp-robot GitHub App installation token for the safe-outputs job via
# `github-app:` below. App-authored PRs are not subject to the org policy, so no org
# change is needed. Requires two repo secrets for the aro-hcp-robot App (which has
# contents:write + pull_requests:write):
#   DEPENDABOT_APP_CLIENT_ID   = the App's OAuth client ID (not the numeric App ID)
#   DEPENDABOT_APP_PRIVATE_KEY = the App private-key PEM
# fallback-as-issue:false keeps the minted token down to contents:write + pull_requests:write
# (no issues:write), matching what the App installation grants. No PAT.
safe-outputs:
  # gh-aw auto-enables a no-op report with report-as-issue: true, which posts a
  # comment to a rolling "[aw] No-Op Runs" issue on every run that finds nothing to
  # do. That is pure noise on a shared public repo, so turn the issue reporting off.
  # Failure and report-incomplete notifications stay on: a failed run is real signal.
  noop:
    report-as-issue: false
  github-app:
    client-id: ${{ secrets.DEPENDABOT_APP_CLIENT_ID }}
    private-key: ${{ secrets.DEPENDABOT_APP_PRIVATE_KEY }}
  create-pull-request:
    max: 6                              # one PR per vulnerability group
    draft: false                        # open ready-for-review so CI runs and it can merge like the image bumper PRs
    fallback-as-issue: false            # no issues: write on the App token, fail instead of opening an issue
    title-prefix: "fix(deps): "
    labels: [dependencies, security, agentic-dependabot]
    # gh-aw guards package manifests (go.mod/go.sum, package.json, lockfiles) as
    # supply-chain-sensitive by default and refuses to push them. Managing those files
    # IS this bot's whole job, so exclude the Go and npm manifests from the protected
    # set. Everything else (.github/, README, AGENTS.md, security config) keeps the
    # default request_review guard.
    protected-files:
      policy: request_review
      exclude:
        - go.mod
        - go.sum
        - package.json
        - package-lock.json
        - yarn.lock
        - pnpm-lock.yaml
  # Let the workflow tidy up after itself. When it re-does a vulnerability that
  # already had an incomplete open PR (see section 1b), gh-aw's create-pull-request
  # always mints a NEW branch, so it cannot update the old PR in place. Instead the
  # agent opens the corrected PR and closes the stale one via this safe output.
  # required-labels scopes it to only ever close this bot's own PRs, never a human's.
  close-pull-request:
    max: 6                              # may supersede several incomplete PRs in one run
    required-labels: [agentic-dependabot]

---

# Agentic Dependabot remediation for ARO-HCP

You are remediating open Dependabot alerts for the `Azure/ARO-HCP` repository. This is a Go multi-module `go.work` workspace plus several npm manifests. Read the module list from `go.work` and the Go toolchain version from the `go`/`toolchain` directives in `go.work` (or the modules' `go.mod`); do not assume a fixed version or module count, use whatever the project declares. Native Dependabot cannot handle it, because a per-manifest bump skips the workspace sync/tidy/license regeneration and never re-tidies the other modules. Your job is to run that ritual correctly and open one clean, dependency-only pull request per group.

## 1. Read the alerts

The open Dependabot alerts have already been fetched for you into `dependabot-alerts.json` in the repository root (the Copilot secrecy sandbox blocks the agent from reading the Dependabot API directly, so a prior workflow step fetched them with an App token). Read that file. It is a JSON array; each entry has:

- `ecosystem` (package ecosystem, `go` or `npm`)
- `package` (the module path)
- `vulnerable_range` and `first_patched` (the first patched version)
- `severity` and the `ghsa` / `cve` identifiers
- `manifest` (the manifest path where the dependency appears)
- `number` (the alert number)

Every entry in the file is already an `open` alert. If the file is empty (`[]`), there is nothing to do: open no PRs and finish.

## 1b. Reconcile against already-open pull requests

The currently open pull requests have been fetched into `open-pull-requests.json` in the repository root. Read that file. Each entry has `number`, `title`, `head` (branch), `draft`, `author`, `labels`, and, for this bot's own PRs, `mergeable_state` and `ci`. A vulnerability is already covered if an open PR bumps the same package (match on the package name, the `fix(deps): ` title, or a referenced GHSA/CVE in the PR title).

First classify each open PR by who owns it, because that decides what you may do with it:

- **Your own PRs** are the ones whose `labels` include `agentic-dependabot`. Only these carry `mergeable_state` and `ci`, and only these may ever be closed with the close-pull-request output.
- **Native Dependabot PRs** (author `dependabot[bot]`, no `agentic-dependabot` label) and **human PRs** are not yours. Never target them with the close-pull-request output (it is label-guarded and will fail the run). If one of your PRs supersedes a native Dependabot PR for the same package, just reference it with `Closes #NNN` in your PR body so it closes on merge, and otherwise leave it alone.

Then walk **every one of your own** open PRs (the `agentic-dependabot`-labeled ones) and, using its `mergeable_state` and `ci` fields plus whether its package still matches an open alert, put it in exactly one of these buckets:

- **Healthy and still needed** (`ci` is `passing`, `mergeable_state` is `clean`, `unstable`, or `blocked`, no actionable change-request, and its package still matches an open alert): the package is covered, drop that alert and move on. Do not open a duplicate. Note `blocked` here just means the PR is waiting for the required human review before it can merge, which is the normal resting state for these PRs, not a problem to fix.
- **Needs attention** (its package still matches an open alert, but `ci` is `failing`, `mergeable_state` is `dirty` (conflicts with the default branch) or `behind` (out of date with the default branch), or a review left an actionable change-request such as a coordinated sibling module left behind): redo the fix off the latest default branch. Because the create-pull-request output always opens a fresh branch, you cannot update the old PR in place, so open a corrected replacement PR **and close the stale one yourself** via the close-pull-request output (it is your own PR, so the label guard passes), with a one-line comment pointing at the replacement (for example "Superseded by the updated PR, which adds the missing sibling bump."). Do not leave both open for a human to reconcile. Only act on comments that mean the fix is incomplete or wrong (see section 5b); leave scope-expanding suggestions alone.
- **Orphaned / stale** (its package matches **no** open alert any more, meaning the vulnerability was fixed, dismissed, or otherwise resolved on the default branch): do not redo it, there is nothing to remediate. If it is also unhealthy (`mergeable_state` is `dirty` or `behind`, or `ci` is `failing`), close it via the close-pull-request output with a one-line comment explaining it no longer maps to an open alert (for example "Closing: the underlying advisory is no longer an open Dependabot alert, so this bump is no longer needed."). Do not open a replacement. You must actually emit the close-pull-request output for it, not just note it in your summary. If an orphaned PR is still perfectly healthy you may leave it for a human, but a conflicting or failing orphaned PR should be closed.

If, after this reconcile, no alerts need a new PR and none of your open PRs needed an update or a close, do nothing.

## 2. Group the alerts

Produce **one pull request per vulnerability group**. Group by remediation family, not by individual alert:

- Group alerts for the **same package** together (all modules at once).
- **Keep coordinated module families in lockstep.** Some dependencies ship as a set of sibling modules released together under one version, for example OpenTelemetry (`go.opentelemetry.io/otel/...`), AWS SDK v2 (`github.com/aws/aws-sdk-go-v2/...`), Kubernetes (`k8s.io/...`), and gRPC/genproto (`google.golang.org/grpc`, `google.golang.org/genproto`). When an alert hits one member, bump **every** sibling already present in the workspace to the **same** release version, not just the alerted module. Leaving a sibling behind (for example `otel` core at v1.43.0 but `otel/exporters/stdout/stdoutmetric` at v1.40.0) draws reviewer flags and can cause API or type mismatches. Find the siblings with `grep -rho '<family-prefix>[^[:space:]]*' --include=go.mod . | sort -u`, `go get` each to the target version, then run `make all-tidy`.
- Group a **cascade family** together: if bumping one module forces a coordinated bump across many workspace modules after `go work sync` (for example a `golang.org/x/*`, `k8s.io/*`, or `google.golang.org/grpc` bump that ripples through the workspace), that is a single group and a single PR.
- **Never mix ecosystems** in one PR. If any npm alerts show up, they are always separate PRs from Go fixes.

Aim for at most 6 groups. If there are more, prioritise by severity (critical > high > medium > low).

## 3. Remediate each group

Work on a fresh branch per group, off the default branch. For each group:

### Go groups
1. Raise the dependency to the first patched version in the relevant module's `go.mod` (use `go get <module>@<version>` in each affected module directory).
2. Run the workspace ritual so the whole `go.work` stays consistent: `make all-tidy` (this runs `go work sync` + `go mod tidy` across every module and regenerates license metadata). A single bump usually cascades: it will update `go.sum` (and sometimes `go.mod`) in **several** modules, not just the one you bumped. That cascade is the whole point, keep every one of those changes.
3. Match the repository CI gate exactly. The verify workflow runs the tidy ritual and then fails if `git status --short` is not empty. So after `make all-tidy`, run it again followed by `git status --short`; if anything is still modified, stage it and repeat until a second run produces no further changes (a clean fixpoint). Only then is the branch CI-clean. A go.sum that carries a hash line for a module with **no matching `require`** in that module's `go.mod` (or a **missing** `.../go.mod` hash line) means tidy did not fully run there: run `go mod tidy` inside that module and re-run `make all-tidy` until it is a no-op.
4. Validate the change builds and lints with the repository's targets (for example `make test-compile` and `make lint` if present). They must pass. Keep the diff dependency-only (see below).

### npm groups
1. Apply the fix in the relevant `package.json` (use an `overrides` entry when the vulnerable package is transitive) and refresh the lockfile.
2. Keep the change npm-only. Do **not** let a Go bump leak into an npm PR, and never mix the two ecosystems in one PR.

## 4. Keep each PR dependency-only

Every PR must contain **only** dependency-management changes: for Go groups `go.mod`, `go.sum`, `go.work`, `go.work.sum` and any regenerated license metadata; for npm groups `package.json` and the lockfile. This includes the full cascade across every module that the tidy ritual touched, not just the module you bumped. Do **not** revert a `go.mod`/`go.sum`/`go.work.sum` change that `make all-tidy` produced thinking it is "unrelated churn"; those cross-module updates are the workspace sync and CI will fail without them. Only revert actual source-code edits (`.go` files, generated code) or the `dependabot-alerts.json` / `open-pull-requests.json` scratch files, which must never be committed. If in doubt, the rule is simple: running `make all-tidy` on the final branch must produce no diff.

## 5. Open the pull requests

For each group, open one pull request via the create-pull-request safe output. The PR must:

- Title: `<package-or-family> to <version> (<severity>)` (the `fix(deps): ` prefix is added automatically, so give the rest).
- Body: list the alerts fixed (GHSA/CVE, package, from -> to version), the modules or manifests touched, and confirm the workspace is tidy-clean and the build/lint targets pass. State that it is dependency-only.
- Be dependency-only as described above.

Follow the repository conventions: plain, human wording, no em-dashes. Do not add `Co-authored-by: Copilot` trailers. Do not create tracking issues. PRs only.

## 5b. Handling review comments

When you update an already-open PR (section 1b) or a reviewer comments on one of your PRs, sort each comment into act vs decline:

- **Act** on comments that mean the fix is incomplete or wrong, then re-run the ritual and the checks and push to the same branch: a coordinated sibling module left behind (section 2 lockstep), a go.sum/go.mod inconsistency (an incomplete tidy), a vulnerable npm range still flagged by `npm audit`, or a wrong / too-low target version.
- **Decline** scope-expanding suggestions that go beyond clearing the vulnerability, because acting on them would break the dependency-only rule: consolidating transitive major versions that legitimately coexist (for example a graph pulling both `yaml.v2` and `yaml.v3`), refactors, or style changes. These stay out of the PR; the PR is intentionally dependency-only.

## 6. If you cannot fix a group

If a group has no patched version available, or the fix requires source changes beyond dependency management (for example an API break from the bump), do not open a broken PR. Report it via the missing-data / report-incomplete channel instead and move on to the next group.
