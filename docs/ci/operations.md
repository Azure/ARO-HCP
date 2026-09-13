# CI Operations

This document is the operator and maintainer view of ARO HCP CI. Use it when you need to inspect a failing run, change the underlying CI configuration, or troubleshoot.

For DEV CI PagerDuty and Slack alerts, start with [DEV CI Monitoring and Alert Response](dev-ci-monitoring.md). For the execution model and cross-tenant request flow, start with [CI Execution](execution.md). For contributor-facing E2E usage including how to trigger jobs, see [E2E Testing In CI](e2e-testing.md).

For a DEV regional provision-health incident, use
[DEV CI Regional Failover And Failback](dev-region-failover.md).

## Inspecting Runs

The normal observation path is:

1. check the PR's **Checks** tab for presubmit status
2. open the job run in the [Prow dashboard](https://prow.ci.openshift.org/?repo=Azure%2FARO-HCP)
3. inspect logs and artifacts for the failing step
4. correlate the failure with the target environment and execution mode

Useful signals include:

- job history in the Prow dashboard
- artifacts from the `aro-hcp-tests` run
- GitHub checks status on the PR
- Slack notifications in [`#forum-ocp-testplatform`](https://redhat.enterprise.slack.com/archives/CBN38N3MW) for build-farm-side failures and repeated issues

### Post-Job Observability Artifacts

For DEV E2E runs, open the artifacts for the
`aro-hcp-gather-observability` post-step before looking for a separate Grafana
dashboard. The step runs before environment deletion and queries the run's
bounded time window from the regional Azure Monitor workspaces. The frontend
sample profile also requests up to 30 minutes of preceding history.

- `observability-summary.html` contains fired-alert details and the selected
  Frontend, Backend, Fleet, Maestro, service-container, and management-cluster
  charts. Use the service-workload charts to identify the highest CPU and
  memory consumers and the running pod count during the failure window, then
  correlate their cluster, namespace, pod, and container labels with the test
  logs.
- `alerts.json` contains the alert data used by the summary.
- `junit_alerts.xml` records unexpected fired alerts as test failures for Prow.
- `evidence/manifest.json` indexes compressed native query responses. Entries
  distinguish `query-result` (existing chart queries) from `stored-samples`
  (the frontend profile), and record the endpoint, allowlisted request
  parameters, request/completion times, HTTP status, and artifact path.
  Artifact paths are relative to the gather output directory. Failed, skipped,
  and limited entries explain missing evidence; `complete` means the native
  response was captured, not that all historical samples were available.
- `evidence/*.json.gz` preserves native Prometheus and Azure Monitor metric
  response bodies before chart conversion, including unfamiliar fields,
  warnings, infos, fractional timestamps, and string-encoded special values.
  HTTP error JSON is retained when available. Request/response headers,
  credentials, and non-allowlisted URL parameters are not exported.

The always-on frontend profile collects the HTTP duration histogram's buckets,
count and sum, the request counter, p99/p95 SLI series, and frontend target
health (`up`, scrape duration, and scraped sample count) from the service
workspace. It uses instant queries with range-vector selectors, not
`query_range` resampling, and preserves all returned labels and stored sample
timestamps without aggregation or replica deduplication.

Profile requests visit the newest 15-minute chunks first, with all metric
families per chunk. They cover the run plus 30 minutes of lookback, capped at
the most recent six hours and at collection start (future end-grace is not
data). Range bounds are `(start,end]`; the oldest uncapped chunk includes 1 ms
of padding to retain a sample exactly at the requested lower boundary. Actual
bounds are recorded in the manifest. Extra collection stops after two minutes
or exhaustion of the shared 128 MiB uncompressed capture budget. Individual
responses are capped at 16 MiB; oversized or incomplete responses are not
published as valid JSON. Limits and failures leave explicit coverage notes
without changing the existing alert verdict.

To inspect evidence offline, start with the manifest and decompress only the
relevant response files. Stored samples can be converted to timestamped
OpenMetrics and imported into an isolated Prometheus using
`promtool tsdb create-blocks-from openmetrics`. This supports recalculating
queries over captured classic histogram/counter samples, not exact replay of
the managed alert evaluator: query exports omit staleness markers, OpenMetrics
backfill does not support native histograms, and late ingestion or missing
lookback history can change results. This capture does not include deployed
rule definitions.

The uploaded summary and evidence survive ephemeral environment deletion,
subject to OpenShift CI artifact retention. They are bounded per-run extracts,
not a complete telemetry backup or a cross-run metrics store. The manifest
preserves the actual query parameters; chart descriptions explain the plotted
signals. The maintained query catalog is
[`queries.yaml`](../../test/cmd/aro-hcp-tests/gather-observability/queries.yaml).

## Modifying CI Configuration

ARO HCP Prow job definitions are maintained in `openshift/release`, not in this repository. The generated job manifests under `ci-operator/jobs/Azure/ARO-HCP/` are outputs and should not be edited directly.

When you change CI configuration in `openshift/release`, follow the release-repo regeneration workflow rather than hand-editing generated YAML. In practice that means using the repo's documented `make update` flow so ci-operator config, Prow jobs, and related generated artifacts stay in sync.

Also keep the ARO HCP-side wiring in mind:

- `config/config.msft.clouds-overlay.yaml` maps public-cloud environments to `prowJobName`
- `test/e2e-pipeline.yaml` passes `PROW_JOB_NAME` to EV2 gating

If one side changes without the other, the rollout path can drift even when the individual YAML files still look valid.

For the full list of ci-operator config files and step-registry components, see the "Where To Look" sections in [CI Image Lifecycle](image-lifecycle.md#where-to-look), [CI Identity Leasing](identity-leasing.md#where-to-look), and [CI EV2 Integration](ev2-integration.md#where-to-look).

## Troubleshooting

### Job Stuck Pending

- check for general OpenShift CI load or incidents first
- verify the job is landing on the expected build-farm cluster
- if the problem is widespread or unrelated to ARO HCP configuration, escalate through the OpenShift CI team

### Test Failures In E2E Jobs

- first identify which execution mode failed: DEV PR, higher-environment PR, EV2 gating, or periodic
- confirm whether the failure looks like product behavior, environment drift, lease exhaustion, or test flake
- use [CI Execution](execution.md) to confirm what that specific job could realistically validate

### Cleanup Failures

- distinguish strict per-test cleanup from periodic cleanup before interpreting the signal
- review [CI Cleanup](cleanup.md) to understand whether the failure is supposed to fail loudly or be best-effort
- check for deletion locks, deny assignments, or missing owner components before assuming the cleanup code is wrong

### Getting Help

- build-farm or Prow infrastructure issues: [#forum-ocp-testplatform](https://redhat.enterprise.slack.com/archives/CBN38N3MW)
- ARO HCP-specific test failures: work through the ARO HCP development or SRE owners for the affected component
- CI config changes: submit a PR to `openshift/release` and involve the OpenShift CI reviewers as needed

## Key Job Families And Source Of Truth

- **PR build and simulation**: `pull-ci-Azure-ARO-HCP-main-images`, `pull-ci-Azure-ARO-HCP-main-frontend-simulation` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`
- **DEV PR E2E**: `pull-ci-Azure-ARO-HCP-main-e2e-parallel` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml` and `ci-operator/step-registry/aro-hcp/local-e2e/`
- **Higher-environment PR E2E**: `integration-e2e-parallel`, `stage-e2e-parallel`, `prod-e2e-parallel` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`
- **Postsubmit image promotion and CD**: `branch-ci-Azure-ARO-HCP-main-images`, `branch-ci-Azure-ARO-HCP-main-images-push-postsubmit`, `branch-ci-Azure-ARO-HCP-main-cspr-pipeline-postsubmit` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`
- **Postsubmit CI base image**: `branch-ci-Azure-ARO-HCP-main-baseimage-generator-images` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__baseimage-generator.yaml`
- **Postsubmit global infra**: `branch-ci-Azure-ARO-HCP-main-global-pipeline-postsubmit` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml` (runs on changes to `config/config.yaml`, `observability/observability.yaml`, or `dev-infrastructure/`)
- **EV2 gating E2E**: `branch-ci-Azure-ARO-HCP-main-e2e-*` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__e2e.yaml`
- **Periodic cleanup**: `periodic-ci-Azure-ARO-HCP-main-periodic-cleanup-*` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__periodic-cleanup.yaml`
- **Periodic E2E**: `periodic-ci-Azure-ARO-HCP-main-periodic-*-e2e-parallel` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__periodic.yaml`
- **Image-updater tooling**: `periodic-ci-Azure-ARO-HCP-main-image-updater-*` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__image-updater.yaml`

## See Also

- [CI Overview](README.md)
- [CI Execution](execution.md)
- [CI Image Lifecycle](image-lifecycle.md)
- [CI Identity Leasing](identity-leasing.md)
- [CI EV2 Integration](ev2-integration.md)
- [CI Cleanup](cleanup.md)
- [DEV CI Regional Failover And Failback](dev-region-failover.md)
- [E2E Testing In CI](e2e-testing.md)
