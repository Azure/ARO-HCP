# Running E2E Tests In CI

This guide explains how to run ARO HCP E2E tests against different environments, how to run specific tests, and why manual testing against Integration, Stage, and Production is not allowed.

For the CI execution model behind these jobs, see [CI Execution](execution.md). For how to inspect or modify the jobs themselves, see [CI Operations](operations.md).

## Why No Manual Testing Against INT, STG, Or PROD

Integration, Stage, and Production environments have constrained management and service-cluster capacity. In the past, manual testing such as creating clusters via `az` or the Azure portal has blocked rollout progress, because manually created clusters consume the same limited capacity that automated E2E tests and real deployments need.

Additionally, the Test Test Azure Red Hat OpenShift tenant subscriptions used for E2E testing are reserved exclusively for automated Prow jobs. Direct access to those subscriptions has been disabled to prevent accidental resource creation that could interfere with CI.

The rule is simple: all E2E testing in those environments must go through Prow jobs, triggered via PRs.

If a scenario is not covered by an existing E2E test, the correct approach is to write a new test and validate it through the Prow CI system. If we need to manually test something that is not caught by E2E, then we have a gap in test coverage that should be addressed.

## Running Tests Via PR

All E2E tests run through Prow. For manual triggers or reruns, use PR comments on the [Azure/ARO-HCP](https://github.com/Azure/ARO-HCP) repository.

### Available Test Commands

| Command | Environment | Runs Automatically | Notes |
|---------|-------------|-------------------|-------|
| `/test e2e-parallel` | DEV (centralus) | Yes, on every PR | Required presubmit. Can validate unmerged RP and shared deployment-artifact changes because the job provisions DEV service footprint on demand. |
| `/test integration-e2e-parallel` | INT (uksouth) | No | Must be triggered manually. Useful for validating test behavior against the shared INT environment. |
| `/test stage-e2e-parallel` | STG (uksouth) | No | Must be triggered manually. Useful for validating test behavior against the shared STG environment. |
| `/test prod-e2e-parallel` | PROD (uksouth) | No | Use with caution. Useful for validating test behavior against the shared PROD environment. |

To rerun all failed jobs:

```text
/retest
```

### What These Jobs Test

Environment-specific E2E jobs such as `integration-e2e-parallel`, `stage-e2e-parallel`, and `prod-e2e-parallel` only validate E2E test code changes before merge. They cannot validate changes to RP code or infrastructure, because those changes are not deployed to the target environment until after merge.

The DEV `e2e-parallel` job is different. Because it provisions the DEV RP footprint as part of the CI run, it can validate undeployed RP and infrastructure changes end to end.

## Running Only Specific Tests

When validating a single test or a small subset of tests, you can temporarily filter which tests run by modifying `test/cmd/aro-hcp-tests/main.go`.

### Example: Filter by Test Name

In [`test/cmd/aro-hcp-tests/main.go`](../../test/cmd/aro-hcp-tests/main.go), uncomment and modify the `MustFilter` line to match only the tests you want:

```go
// Specs can be globally filtered...
// TODO: remove after PR validation
specs = specs.MustFilter([]string{`name.contains("ImageDigestMirrors")`})
```

This uses [CEL expressions](https://github.com/google/cel-spec) to filter test specs by name. The `name.contains("...")` function matches tests whose full name contains the given substring.

### Step-By-Step Process

1. Create a branch for your test validation:

   ```bash
   git checkout -b test/validate-my-test
   ```

2. Edit `test/cmd/aro-hcp-tests/main.go` and add a filter:

   ```go
   specs = specs.MustFilter([]string{`name.contains("MyTestName")`})
   ```

3. Commit and push, then open a PR.
4. Trigger the appropriate test job:

   ```text
   /test stage-e2e-parallel
   ```

5. After validation, either close the PR or revert the filter before merging.

> [!IMPORTANT]
> Always revert test filters before merging. Leaving a filter in place would silently skip tests in CI.

For a real-world example of this workflow, see [PR #4597](https://github.com/Azure/ARO-HCP/pull/4597).

### Other Filter Examples

Filter by label:

```go
specs = specs.MustFilter([]string{`labels.exists(l, l=="Positivity:Positive")`})
```

Combine filters:

```go
specs = specs.MustFilter([]string{`name.contains("Cluster") && labels.exists(l, l=="Importance:Critical")`})
```

## Test Suites And Labels

Tests are organized into suites with label-based filtering. The available labels are defined in [`test/util/labels/labels.go`](../../test/util/labels/labels.go):

| Label | Purpose |
|-------|---------|
| `Positivity:Positive` / `Positivity:Negative` | Positive vs negative test cases |
| `Speed:Slow` | Long-running tests that run in separate `/slow` suites |
| `Importance:{Low,Medium,High,Critical}` | Test importance level |
| `Development-Only` | Tests that only run in DEV environments |
| `Integration-Only` | Tests that only run in integration |
| `PreLaunchSetup:None` | Tests that require no pre-existing infrastructure |
| `ARO-HCP-RP-API-Compatible` | Tests compatible with both ARM and ARO HCP RP APIs |

## Periodic Tests

In addition to PR-triggered tests, periodic Prow jobs run E2E tests on a schedule:

- **Integration**: on-demand or placeholder-scheduled depending on the current config
- **Stage**: daily
- **Production**: daily

See [CI Execution](execution.md#periodic-jobs) for why these jobs exist and how they differ from EV2 gating. See [CI Operations](operations.md#inspecting-runs) for the operator view of how to inspect them.

## Related Documentation

- [CI Overview](README.md)
- [CI Execution](execution.md)
- [CI Operations](operations.md)
- [Test Test Tenant Access](../sops/test-test-tenant-access.md)
- [E2E Test Code](../../test/e2e/)
