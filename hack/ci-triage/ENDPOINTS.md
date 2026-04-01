# CI Triage — Data Sources and Artifact Reference

## Primary: Sippy API (bulk job + test data)

`prow.py` uses the Sippy API (`sippy.dptools.openshift.org/api/jobs/runs`) as its primary data source. A single request returns all job runs with pass/fail status and failed test names — no per-job artifact fetches needed.

Sippy releases: `aro-integration` (int), `aro-stage` (stg), `aro-production` (prod), `Presubmits` (all presubmit jobs).

Sippy does **not** have error messages or stack traces — only test names. For error details, drill into GCS artifacts below.

## GCS Artifacts (per-job drill-down)

All paths relative to `base_url` (from `prow.py env-health` output).

### Job-level

- `artifacts/junit_operator.xml` (XML) — Step-level pass/fail. Each `<testcase name="...">` is a CI step. `<failure>` text has the error. Used as fallback when per-test junit.xml is unavailable.
- `artifacts/ci-operator.log` (text) — Full ci-operator log. Large — grep only (`error|fail`).
- `artifacts/build-logs/` (dir) — One log file per image build. Use `fetch-artifact` or WebFetch to access.

### Step-level

Test step name is resolved automatically by `build-log`. For manual artifact access, `TEST_STEPS` in `prow.py` has the mapping.

- `artifacts/{TEST_STEP}/aro-hcp-test-persistent/build-log.txt` (text) — Test runner stdout/stderr (Ginkgo output). **Command:** `prow.py build-log BASE_URL ENV`.
- `artifacts/{TEST_STEP}/aro-hcp-provision-environment/build-log.txt` (text) — Provisioning output — ARM deployment commands and errors. **Command:** `prow.py build-log BASE_URL ENV --step provision`.
- Regex search across build logs: **Command:** `prow.py build-log BASE_URL ENV --grep PAT`.

### Test artifacts (`artifacts/{TEST_STEP}/aro-hcp-test-persistent/artifacts/`)

**Primary:** `prow.py failure-summary` gets test names via Sippy. For error messages, use `prow.py fetch-failures BASE_URL ENV` (per-test from junit.xml) or `prow.py build-log BASE_URL ENV`.

- `junit.xml` (XML) — Per-test junit (Ginkgo). Parsed automatically by `env-health` and `fetch-failures`.
- `extension_test_result*.json` (JSON array) — Per-test results with timing. Use `fetch-artifact` or WebFetch.
- `{TestName}/` (dir) — Per-test artifacts (typically `azure.log`). Use `fetch-artifact` or WebFetch.

### Deep-dive artifacts (via `fetch-artifact` or WebFetch)

Not covered by dedicated prow.py commands — use `prow.py fetch-artifact BASE_URL PATH` or WebFetch.

- `artifacts/ci-operator-metrics.json` — Step-level durations and resource usage. Helps distinguish timeout vs. fast-fail.
- `podinfo.json` — Pod lifecycle events, node placement, OOM kills. Check when infra-level failure suspected.
- `artifacts/{TEST_STEP}/aro-hcp-test-persistent/artifacts/resourcegroups/{TestName}/deployments.yaml` — ARM deployment status per test. Check when Azure resource errors. Use `fetch-artifact` to access.
- `artifacts/{TEST_STEP}/aro-hcp-test-persistent/artifacts/identities-pool-state.yaml` — MSI resource pool contention. Check when identity/auth failures cluster.
- `artifacts/build-resources/events.json` — K8s events from CI build namespace. Check when image build or source clone fails.

### Prow URL to job ID

Extract the 19-digit number from any Prow URL. Determine env/type from the job name:
- `periodic-*-integration-e2e-parallel` → int/periodic
- `periodic-*-stage-e2e-parallel` → stg/periodic
- `periodic-*-prod-e2e-parallel` → prod/periodic
- `pull-*-integration-e2e-parallel` → int/presubmit
- `pull-*-stage-e2e-parallel` → stg/presubmit
- `pull-*-prod-e2e-parallel` → prod/presubmit
- `pull-*-e2e-parallel` (no env prefix) → dev/presubmit
