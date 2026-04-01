# CI Triage — Artifact Reference

All paths relative to `base_url` (from `prow.py env-health` output).

## Job-level

- `prowjob.json` (JSON) — Job metadata: `status.{state,startTime,completionTime,url}`, `spec.refs.pulls[0].{number,title,author}` (presubmit).
- `artifacts/junit_operator.xml` (XML) — Step-level pass/fail. Each `<testcase name="...">` is a CI step. `<failure>` text has the error. Used as fallback when per-test junit.xml is unavailable.
- `artifacts/ci-operator.log` (text) — Full ci-operator log. Large — grep only (`error|fail`).
- `artifacts/build-logs/` (dir) — One log file per image build. Use `fetch-artifact` or WebFetch to access.

## Step-level

Test step name is resolved automatically by `fetch-build-log`. For manual artifact access, `TEST_STEPS` in `prow.py` has the mapping.

- `artifacts/{TEST_STEP}/aro-hcp-test-persistent/build-log.txt` (text) — Test runner stdout/stderr (Ginkgo output). **Command:** `prow.py fetch-build-log BASE_URL ENV`.
- `artifacts/{TEST_STEP}/aro-hcp-provision-environment/build-log.txt` (text) — Provisioning output — ARM deployment commands and errors. **Command:** `prow.py fetch-build-log BASE_URL ENV --step provision`.
- Regex search across build logs: **Command:** `prow.py grep-build-log BASE_URL ENV --pattern PAT`.

## Test artifacts (`artifacts/{TEST_STEP}/aro-hcp-test-persistent/artifacts/`)

**Primary:** `prow.py env-health` fetches junit.xml automatically. For per-job deep-dive, use `prow.py fetch-failures BASE_URL ENV` (per-test with fingerprints) or `prow.py fetch-build-log BASE_URL ENV`.

- `junit.xml` (XML) — Per-test junit (Ginkgo). Parsed automatically by `env-health` and `fetch-failures`.
- `extension_test_result*.json` (JSON array) — Per-test results with timing. Use `fetch-artifact` or WebFetch.
- `{TestName}/` (dir) — Per-test artifacts (typically `azure.log`). Use `fetch-artifact` or WebFetch.

## Deep-dive artifacts (via `fetch-artifact` or WebFetch)

Not covered by dedicated prow.py commands — use `prow.py fetch-artifact BASE_URL PATH` or WebFetch.

- `artifacts/ci-operator-metrics.json` — Step-level durations and resource usage. Helps distinguish timeout vs. fast-fail.
- `podinfo.json` — Pod lifecycle events, node placement, OOM kills. Check when infra-level failure suspected.
- `artifacts/{TEST_STEP}/aro-hcp-test-persistent/artifacts/resourcegroups/{TestName}/deployments.yaml` — ARM deployment status per test. Check when Azure resource errors. Use `fetch-artifact` to access.
- `artifacts/{TEST_STEP}/aro-hcp-test-persistent/artifacts/identities-pool-state.yaml` — MSI resource pool contention. Check when identity/auth failures cluster.
- `artifacts/build-resources/events.json` — K8s events from CI build namespace. Check when image build or source clone fails.

## Prow URL to job ID

Extract the 19-digit number from any Prow URL. Determine env/type from the job name:
- `periodic-*-integration-e2e-parallel` → int/periodic
- `periodic-*-stage-e2e-parallel` → stg/periodic
- `periodic-*-prod-e2e-parallel` → prod/periodic
- `pull-*-integration-e2e-parallel` → int/presubmit
- `pull-*-stage-e2e-parallel` → stg/presubmit
- `pull-*-prod-e2e-parallel` → prod/presubmit
- `pull-*-e2e-parallel` (no env prefix) → dev/presubmit
