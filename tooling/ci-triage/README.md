# ci-triage

CI failure triage tool for ARO-HCP Prow e2e tests. Queries Sippy and GCS for fleet health, failure classification, onset detection, deployment correlation, and root cause analysis across all environments.

Stateless — no database, no local state. Sippy provides fleet data, GCS provides test artifacts (cached locally), GitHub provides PR data, and cross-CI search checks platform scope.

## Quick Start

```bash
make build

./ci-triage summary --since 14d                    # Fleet health across all envs
./ci-triage failures int --since 14d               # Deep failure analysis for one env
./ci-triage diagnose prod --since 14d              # Full automated synthesis with verdict
./ci-triage correlate stg --since 14d              # Map failure onsets to deployments + PRs
./ci-triage pr 4724                                # PR triage with baseline comparison
```

## Commands

| Command | Description |
|---|---|
| `summary` | Fleet-wide health scan: pass rates, infra event counts, flaky tests, fleet-wide failures with classification |
| `failures ENV` | Deep failure analysis: classified failure groups, onset detection, cross-env context, EV2 deployment info, error messages |
| `investigate ENV [--test]` | Deep single-test investigation: GCS artifacts (step timing, Azure API logs) + cross-CI scope check |
| `diagnose ENV [--test]` | Full synthesis: fleet health + investigation + correlation → verdict with confidence and evidence chain |
| `correlate ENV [--test]` | Map failure onsets to EV2 deployment changes and merged PRs with relevance scoring |
| `timeline ENV` | Time-series of job pass/fail with rollout annotations and infra flags |
| `pr NUMBER` | PR failure analysis with periodic baseline comparison (`[baseline]` vs `[NEW]`) and changed files |
| `build-log URL ENV` | Raw build log tail from a specific job run |
| `test-detail URL ENV TEST` | Per-test deep dive: full error, full output, Azure API logs (every ARM call with timestamps) |
| `search QUERY [--cross-ci]` | Search CI artifacts for failure patterns; `--cross-ci` compares ARO vs all OpenShift CI |

## Data Sources

```
Sippy API (sippy.dptools.openshift.org)
  ├─ Job runs: pass/fail, failed test names, infra flags, flake data
  ├─ EV2 annotations: deployment commit, build, region per job run
  ├─ Test outputs: failure messages from Sippy's index
  └─ Health/statistics endpoints

GCS (test-platform-results, public, cached locally)
  ├─ extension_test_result_e2e_*.json — full error + output + timing per test
  ├─ azure.log (per test) — every ARM API call with timestamps
  ├─ build-log.txt — CI step execution log
  ├─ ci-operator-step-graph.json — step dependency graph + timing
  ├─ junit.xml — test pass/fail summary (fallback)
  └─ finished.json — job state + revision

GitHub (via gh CLI)
  ├─ PR metadata: title, author, files, merge time
  └─ Merged PR listing by time window

Cross-CI Search (search.dptools.openshift.org)
  └─ Regex search across ~307K OpenShift CI jobs
```

## Key Capabilities

- **Failure classification**: regression / flaky / infrastructure / fleet_wide with confidence scoring
- **Infrastructure separation**: wipeout jobs (>80% tests failing or Sippy infra flag) excluded from test-level analysis
- **Cross-env context**: each failure shows which other environments have the same failure
- **Deployment correlation**: EV2 rollout annotations identify which deployment commit was running at onset
- **PR relevance scoring**: PRs in onset window scored by file changes, component overlap, keyword matching
- **Cross-CI scope**: distinguishes ARO-specific failures from platform-wide OpenShift issues
- **Azure API log analysis**: per-test ARM call tracing with LRO state transitions and ResponseErrors

## Claude Code Skills

Available as 8 focused skills:

```
/triage health          — Fleet scan, routes to sub-skills
/ci-health              — Fleet-wide health assessment
/ci-investigate int     — Deep environment investigation
/ci-diagnose prod       — Full automated synthesis with verdict
/ci-correlate stg       — Onset → deployment → PR correlation
/ci-triage-pr 4724      — PR regression triage
/ci-build-log URL ENV   — Build log + Azure API log analysis
/ci-search "error"      — Cross-CI failure scope check
```

## Development

```bash
make build    # build binary (no CGO required)
make test     # run unit tests
make clean    # remove binary
```
