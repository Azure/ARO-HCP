# ARO HCP CI Daily Digest — Task Instructions

> This file is read by the Chai Bot scheduled task at runtime.
> Internal configuration (Slack channels, Jira projects, notification groups) is provided
> by the task configuration in the Chai Bot repo and should NOT be duplicated here.

## Jobs to Monitor

### STAGE jobs (review every day)

- `periodic-ci-Azure-ARO-HCP-main-periodic-stage-e2e-parallel`
- `periodic-ci-Azure-ARO-HCP-main-periodic-stage-e2e-parallel-slow`
- `periodic-ci-Azure-ARO-HCP-main-periodic-stage-e2e-parallel-ocp-nightly`

### PROD jobs (review every day)

- `periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel`
- `periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel-slow`
- `periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel-ocp-nightly`

### PROD weekly jobs (review on Monday only)

- `periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel-ocp-candidate-4-20`
- `periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel-ocp-candidate-4-22`
- `periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel-ocp-candidate-5-0`

## Schedule Logic

This task runs daily Monday through Friday. On **Monday**, review all three groups (STAGE + PROD daily + PROD weekly). On **Tuesday through Friday**, review only STAGE and PROD daily jobs. Skip PROD weekly jobs on non-Monday runs.

## Procedure

Complete all steps across every job in scope before filing any bugs. This ensures failures with a shared root cause are grouped into a single ticket.

### 1. Check Status of All Jobs

For every job in scope for today's run:

- Retrieve all completed runs since the last digest check (not just the most recent run). This task runs Monday through Friday, so on Monday it must cover Saturday, Sunday, and Monday runs to ensure no weekend runs are skipped.
- Record whether each run passed or failed, and note the Prow job URL.
- If the most recent run is still in progress, skip it and include only completed runs.
- **Staleness check:** If a job has no completed runs in the expected period (more than 26 hours since the last run for daily jobs, or more than 8 days for weekly jobs), flag it as stale/missing in the report. This prevents a job that stopped being scheduled from silently disappearing from the digest.

### 2. Analyze Failures for All Failed Jobs

For every job that had at least one failed run:

- Use `fetch_aro_test_analysis` to pull the AI-generated test failure analysis from the ARO releases dashboard (available for Azure-ARO-HCP jobs).
- Download and search the build log for error patterns, panics, and timeout messages.
- Identify the root cause category:
  - **Infrastructure failure** — CI platform issue, cloud provider issue, resource provisioning failure
  - **Test flake** — Intermittent failure not related to a code change
  - **Product regression** — Failure caused by a recent code change
  - **Configuration issue** — Test environment misconfiguration
- Note which specific tests failed and the failure signatures.

### 3. Check for Infrastructure Outages

Query the SHIP Status Dashboard for any active or recent outages on CI infrastructure (Prow, build farms) that overlap with any of the failed job run times. Note any relevant outages — these may explain multiple failures across different jobs.

### 4. Search for Known Issues

For all failures identified in steps 2 and 3, follow the bug searching process described in [bug-searching.md](../qe/bug-searching.md).

### 5. File New Bugs

Now that all jobs have been analyzed, group failures by root cause and file one ticket per root cause covering all affected jobs. Follow the bug reporting process described in [bug-reporting.md](../qe/bug-reporting.md).

> **Note:** When multiple jobs fail with the same root cause (e.g., an infrastructure outage or a shared regression), file a single Jira ticket covering all affected jobs. List all affected job names in the description.

## Report Format

Structure the report as follows:

```
## ARO HCP CI Daily Digest — <date>

### Summary
<One-line overall status: e.g., "3/6 daily jobs passing, 1 new issue filed">

### STAGE Jobs
For each job:
- ✅ or ❌ <job short name> — <status>
  - If failed: root cause, linked Jira/PR, or "NEW: <JIRA-KEY>"

### PROD Daily Jobs
(Same format as STAGE)

### PROD Weekly Jobs (Monday only)
(Same format, included only on Monday)

### New Issues Filed
- <JIRA-KEY>: <summary> — <Prow job link>

### Known Issues Referenced
- <JIRA-KEY>: <summary> — <status>
- <PR link>: <title> — <state>

### Infrastructure Notes
- Any relevant SHIP Status outages or CI infrastructure issues observed
```

## Notifications

If any new bugs were filed, mention the configured notification group in the report.

## Display Guidelines

- Use short job names in the report for readability (e.g., "stage-e2e-parallel" instead of the full `periodic-ci-Azure-ARO-HCP-main-periodic-…` name). Always link to the full Prow job URL.
- Group failures with the same root cause together.
- Link all referenced Jira issues and GitHub PRs.
