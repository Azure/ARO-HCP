---
name: triage
description: Triage Prow CI e2e test failures for ARO-HCP environments
argument-hint: <env> [type]  |  pr <number>
user-invocable: true
---

Triage Prow CI failures for ARO-HCP. You are an SRE investigating CI failures — your job is to find root causes, not just report what failed.

## Arguments

`$ARGUMENTS` is `ENV [TYPE]` or `pr NUMBER` (e.g., `int`, `stg periodic`, `dev presubmit`, `pr 4618`). When TYPE is omitted, run both periodic and presubmit (in parallel). Exception: dev has no periodic jobs.

## How to triage

### 1. Get the overview

Start with `--since` for today's date to see **all** jobs, not just the last N. Using `--history` caps results and can hide the true failure rate (e.g. dev presubmit can run 70+ jobs/day — `--history 20` would miss most of them).

```bash
# All envs at once — use today's date to see everything
python3 hack/ci-triage/prow.py overview --since $(date -u +%Y-%m-%d)

# Single env — use --since for today, or a date range for investigation
python3 hack/ci-triage/prow.py failure-summary ENV TYPE --since $(date -u +%Y-%m-%d)
python3 hack/ci-triage/prow.py failure-summary ENV TYPE --since 2026-03-25

# Is this getting better or worse over the past week?
python3 hack/ci-triage/prow.py trending ENV TYPE --days 7

# For PR triage: get check status first
gh pr checks PR_NUMBER
```

Use `--history N` only for quick spot checks or when you want a fixed sample size regardless of time range.

From `failure-summary`, note:
- **pass_rate** — Is this environment healthy (>0.8) or broken (<0.5)?
- **failure_groups** — Which tests fail, how often (`count`), across how many jobs (`jobs_hit`). High `jobs_hit` = systemic issue.
- **jobs_analyzed** — How many failed jobs had test data.

If you only need pass/fail counts without test-level details, use `env-health` (also Sippy-backed, returns `failed_jobs` with URLs for drill-down).

### 2. Drill into specific failures

`failure-summary` gives test names but not error messages (Sippy doesn't store them). For error details, drill into specific failed jobs with `fetch-failures` and `build-log`:

```bash
# Get error messages from a specific failed job's junit.xml
python3 hack/ci-triage/prow.py fetch-failures BASE_URL ENV

# Get BASE_URL from env-health output's failed_jobs list
python3 hack/ci-triage/prow.py env-health ENV TYPE --since $(date -u +%Y-%m-%d)
```

Pick representative jobs from `failure-summary`'s groups — you don't need to check every failed job, just one or two per distinct failure pattern.

### 3. Dig into build logs for the real cause

Junit failure messages are often wrappers around the real error. The build log has the full story.

```bash
# Test runner output — read this for test-level failures
python3 hack/ci-triage/prow.py build-log BASE_URL ENV

# Provisioning output — read this for infra/setup failures
python3 hack/ci-triage/prow.py build-log BASE_URL ENV --step provision

# Search for specific error patterns
python3 hack/ci-triage/prow.py build-log BASE_URL ENV --grep "error|fatal|panic"
```

**Read build logs from the top.** Ginkgo and CI failures cascade — the first error is the cause, later errors are consequences. When you see 15 tests fail with "Interrupted by User" and 1 test fail with a timeout, the timeout is the cause.

### 4. Determine scope and blast radius

Once you understand a failure, figure out how widespread it is:

- **Same failure in periodic jobs?** Then it's infrastructure, not a PR's fault.
- **Same failure across environments?** Run `env-health` for each env and compare root causes — cross-env failures are almost certainly infrastructure.
- **Hitting multiple PRs?** The failure is pre-existing, not caused by any single PR.

```bash
# Compare across environments — overview does this in one shot
python3 hack/ci-triage/prow.py overview --since $(date -u +%Y-%m-%d)

# Or compare specific envs
python3 hack/ci-triage/prow.py failure-summary int periodic --since $(date -u +%Y-%m-%d)
python3 hack/ci-triage/prow.py failure-summary stg periodic --since $(date -u +%Y-%m-%d)
```

### 5. Find the cause for new failures

If a failure wasn't present in older jobs:

```bash
# Look at the PR that might have caused it
gh pr view PR_NUMBER --json title,body,changedFiles,additions,deletions,labels,author
gh pr diff PR_NUMBER
```

Use `git log --merges --after=FIRST_SEEN` to find merge commits near the failure's first appearance. Read the suspect PR's diff. Does the change plausibly cause this failure? Don't just list suspects — form a hypothesis.

## PR triage specifically

When triaging a specific PR (`/triage pr 4618`):

1. **`gh pr checks`** — What's failing? Note flakes (failed then passed) vs. persistent failures.
2. **`gh pr view`** — What does this PR change? Read the body and changed files.
3. **Check periodic health** — Run `failure-summary ENV periodic --since $(date -u +%Y-%m-%d)` for each env where the PR has failures. If the same failure appears in periodic jobs, it's not the PR's fault.
4. **Deep-dive the PR's failures** — Use the Prow link from `gh pr checks` to get the `base_url`, then `fetch-failures` and `build-log` to read the actual errors.
5. **Compare** — Is the failure related to what the PR changes? A PR touching `frontend/` shouldn't cause failures in cluster provisioning.

## Reasoning guidelines

**Don't report grouping keys to the user.** They're normalized internal IDs. Report what the failure *is*: "cluster creation timing out after 45 minutes" not a normalized string.

**Distinguish cause from collateral.** "Interrupted by User" means Prow killed remaining tests after the job timed out. The real problem is whatever made the job slow. If you see 20 interrupted tests and 2 timeout tests, investigate the timeouts.

**Generic errors need follow-up.** When you see `"exit status 1"` or `"failed to execute wrapped command"`, that's a wrapper — the real error is in the build log. Always follow up on generic messages.

**Different timeout operations = different bugs.** A credential timeout and a cluster creation timeout are completely different issues. Don't lump them together.

**Form hypotheses, then verify.** Don't just list what failed — explain *why* you think it failed and what evidence supports that. "Cluster creation is timing out, likely due to X because I see Y in the build log" is useful. "3 tests failed with timeout" is not.

**Cross-reference before blaming.** Before saying a PR caused a failure, check whether the failure exists in periodic jobs. Before calling an environment healthy, check both periodic and presubmit.

**Report what matters for action.** The user wants to know: What's broken? Is it my fault? What should I do? Structure your findings around those questions.

## Command reference

Run from repo root: `python3 hack/ci-triage/prow.py COMMAND ...`. All output is JSON.

### Analysis (Sippy-backed, 1 request each)
- `failure-summary ENV TYPE [--history N] [--since DT]` — Cross-job failure grouping. Groups test failures by name with counts across all failed jobs. Includes sample error messages for top groups. Start here.
- `overview [--history N] [--since DT]` — All envs at once. Runs failure-summary for every env/type combo in parallel. Good for "what's broken right now?" questions.
- `trending ENV TYPE [--days N] [--bucket daily|weekly]` — Pass rate over time with trend detection (improving/degrading/stable). Use to check if things are getting better or worse.
- `env-health ENV TYPE [--history N] [--since DT]` — Pass/fail ratio and failed job list with short URLs for drill-down. Use when you need job URLs but don't need test-level grouping.

### Per-job deep-dive (GCS-backed, 1-2 requests each)
- `fetch-failures BASE_URL [ENV]` — Test failures with error messages from junit.xml (auto-falls back to step-level)
- `build-log BASE_URL [ENV] [--step test|provision] [--lines N] [--grep PAT] [--context N]` — Build log tail or search

### PR
Use `gh pr checks`, `gh pr view`, and `gh pr diff` for PR metadata and check status.

BASE_URL accepts: short paths from env-health output (e.g. `/logs/...`), full GCSWEB URLs, or Prow dashboard URLs. ENV is auto-detected from the URL when omitted.

## Artifact reference

`base_url` (short path or full URL) comes from `env-health` output (in `failed_jobs`) or Prow dashboard URLs.

Test step/container by env: dev=`e2e-parallel/aro-hcp-test-local`, int=`integration-e2e-parallel/aro-hcp-test-persistent`, stg=`stage-e2e-parallel/aro-hcp-test-persistent`, prod=`prod-e2e-parallel/aro-hcp-test-persistent`.

Full artifact path reference: `hack/ci-triage/ENDPOINTS.md`
