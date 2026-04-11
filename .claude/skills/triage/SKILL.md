---
name: triage
description: Triage CI failures for ARO-HCP — wide to deep with evidence-based reasoning
argument-hint: <env> | pr <number> | health | diagnose <env>
user-invocable: true
---

Investigate CI failures. Synthesize evidence across data sources to find root causes.

`$ARGUMENTS` determines entry point:
- `health` or empty → `/ci-health`
- `ENV` (int, stg, prod) → `/ci-investigate ENV`
- `pr NUMBER` → `/ci-triage-pr NUMBER`
- `diagnose ENV` → `/ci-diagnose ENV` (full automated synthesis)

Build first if needed: `cd tooling/ci-triage && make build && cd -`

## Commands — What Each Answers

| Question | Command | Data Source |
|----------|---------|------------|
| What's broken fleet-wide? | `summary --since 14d` | Sippy |
| How bad is one env? | `failures ENV --since 14d` | Sippy + GCS |
| What's the full story for one test? | `investigate ENV --test "Name"` | Sippy + GCS + cross-CI |
| Give me a verdict | `diagnose ENV [--test "Name"]` | All sources → synthesized verdict |
| When did it start? Who caused it? | `correlate ENV --since 14d` | Sippy + GitHub |
| Is this our bug or platform-wide? | `search "error msg" --cross-ci` | search.dptools |
| What happened in this job run? | `build-log URL ENV --lines 200` | GCS build-log |
| What happened for this test? | `test-detail URL ENV "Name"` | GCS extension results + azure.log |
| Pass/fail over time? | `timeline ENV --since 14d` | Sippy |
| Did this PR break something? | `pr NUMBER` | GCS + Sippy + GitHub |

## Reasoning Stages

**WHAT** → `summary` + `failures`: classification (regression/flaky/infra/fleet_wide), infra events separated, meta-tests filtered, error messages with file:line

**WHEN** → `failures` + `correlate`: onset detection (last_passed → first_failed), onset_rollout (EV2 deployment at onset), timeline patterns

**WHY** → `correlate` + `investigate` + `test-detail` + `search`:
- Deployment correlation: commit_range between last-good and first-bad deployment
- PR correlation: merged PRs in onset window with relevance scores
- Azure API logs: which ARM calls were slow/failed/rate-limited
- Cross-CI scope: is this ARO-specific or happening across OpenShift?
- Build log timestamps: where time was actually spent

## Key Output Fields (new since initial design)

- `classification`: `{class, confidence, reason}` — on every failure group and fleet failure
- `other_envs`: which other environments have the same failure
- `onset_rollout`: EV2 deployment (commit, build, region) running at onset
- `infra_jobs`: infrastructure events separated from real test regressions
- `fleet_context`: pass rates of other envs for comparison
- `deployment`: `{last_good_rollout, first_bad_rollout, commit_range}` on correlations
- `confidence`: high/medium/low with reason on correlations

## GCS Artifacts Available Per Job Run

These files exist in GCS for every completed job and can be fetched on demand:

| File | Contains | Use When |
|------|----------|----------|
| `extension_test_result_e2e_*.json` | Full error + output + timing per test | Primary message source (richer than JUnit) |
| `azure.log` (per test dir) | Every ARM API call with timestamps | Investigating Azure/ARM issues |
| `build-log.txt` | CI step execution log | Provisioning failures, job timeouts |
| `junit.xml` | Test pass/fail summary | Fallback if extension results unavailable |
| `finished.json` | Job state + revision | PR triage, revision tracking |
| `ci-operator-step-graph.json` | Step dependency graph + timing | Step-level timing analysis |
| `timing-metadata-*.yaml` | Per-deployment ARM operation durations | Deep ARM operation analysis (compressed) |
| `identities-pool-state.yaml` | MSI container lease state | Parallel test resource contention |
| `ci-operator.log` | CI orchestration output | CI infra issues |

## What You Cannot See

Backend logs, Azure resource health, cluster-service internal state, Kusto. For the hard 30% — race conditions, transient Azure failures, multi-component interaction bugs — state what evidence points toward and what data would confirm it. Steve's triage tool (`tooling/triage/`) has full Kusto integration for this layer when available.
