# Running E2E Tests

This guide explains how to run ARO HCP E2E tests against different environments, how to run specific tests, and why manual testing against Integration and Stage environments is not allowed.

## Why No Manual Testing Against INT/Stage/Prod

Integration, Stage and Production environments have **constrained management and service cluster capacity**. In the past, manual testing (e.g. creating clusters via `az` or the Azure portal) has **blocked the ability to progress rollouts**, because manually-created clusters consume the same limited capacity that automated E2E tests and real deployments need.

Additionally, the Test Test Azure Red Hat OpenShift tenant subscriptions used for E2E testing are **reserved exclusively for automated Prow jobs**. Direct access to those subscriptions has been disabled to prevent accidental resource creation that could interfere with CI.

**The rule is simple: all E2E testing must go through Prow jobs, triggered via PRs.**

If a scenario is not covered by an existing E2E test, the correct approach is to write a new test and validate it through the Prow CI system. If we need to manually test something that is not caught by E2E, then we have a gap in test coverage that should be addressed.

## Running Tests via PR

All E2E tests are triggered through Prow by commenting on a pull request in the [Azure/ARO-HCP](https://github.com/Azure/ARO-HCP) repository.

### Available Test Commands

| Command | Environment | Runs Automatically | Notes |
|---------|-------------|-------------------|-------|
| `/test e2e-parallel` | Dev (centralus) | Yes (on every PR) | Optional, does not block merge. Deploys to dedicated subscription. |
| `/test integration-e2e-parallel` | Int (uksouth) | No | Must be triggered manually |
| `/test integration-e2e-parallel-ocp-fast` | Int (uksouth) | No | Must be triggered manually |
| `/test integration-e2e-parallel-ocp-stable` | Int (uksouth) | No | Must be triggered manually |
| `/test integration-e2e-parallel-ocp-nightly` | Int (uksouth) | No | Must be triggered manually |
| `/test stage-e2e-parallel` | Stage (uksouth) | No | Must be triggered manually |
| `/test stage-e2e-parallel-ocp-fast` | Stage (uksouth) | No | Must be triggered manually |
| `/test stage-e2e-parallel-ocp-stable` | Stage (uksouth) | No | Must be triggered manually |
| `/test stage-e2e-parallel-ocp-nightly` | Stage (uksouth) | No | Must be triggered manually |
| `/test prod-e2e-parallel` | Prod (uksouth) | No | Use with caution |
| `/test prod-e2e-parallel-ocp-fast` | Prod (uksouth) | No | Use with caution |
| `/test prod-e2e-parallel-ocp-stable` | Prod (uksouth) | No | Use with caution |
| `/test prod-e2e-parallel-ocp-nightly` | Prod (uksouth) | No | Use with caution |

To re-run all failed jobs:
```
/retest
```

### What These Jobs Test

Environment-specific E2E jobs (`integration-e2e-parallel`, `stage-e2e-parallel`, `prod-e2e-parallel`) **only validate E2E test code changes**. They cannot validate changes to RP code or infrastructure, because those changes are not deployed to the target environment until after merge.

## Running Only Specific Tests

When validating a single test or a small subset of tests, you can temporarily filter which tests run by modifying `test/cmd/aro-hcp-tests/main.go`.

### Example: Filter by Test Name

In [`test/cmd/aro-hcp-tests/main.go`](../test/cmd/aro-hcp-tests/main.go), uncomment and modify the `MustFilter` line to match only the tests you want:

```go
// Specs can be globally filtered...
// TODO: remove after PR validation
specs = specs.MustFilter([]string{`name.contains("ImageDigestMirrors")`})
```

This uses [CEL expressions](https://github.com/google/cel-spec) to filter test specs by name. The `name.contains("...")` function matches tests whose full name contains the given substring.

### Step-by-Step Process

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
   ```
   /test stage-e2e-parallel
   ```

5. After validation, either close the PR or revert the filter before merging.

> **Important**: Always revert test filters before merging. Leaving a filter in place would silently skip tests in CI.

For a real-world example of this workflow, see [PR #4597](https://github.com/Azure/ARO-HCP/pull/4597).

### Other Filter Examples

Filter by label:
```go
specs = specs.MustFilter([]string{`labels.exists(l, l=="Positivity:Positive")`})
```

Combine filters (name AND label):
```go
specs = specs.MustFilter([]string{`name.contains("Cluster") && labels.exists(l, l=="Importance:Critical")`})
```

## Test Suites and Labels

Tests are organized into suites with label-based filtering. The available labels are defined in [`test/util/labels/labels.go`](../test/util/labels/labels.go):

| Label | Purpose |
|-------|---------|
| `Positivity:Positive` / `Positivity:Negative` | Positive vs negative test cases |
| `Speed:Slow` | Long-running tests (run in separate `/slow` suites) |
| `Importance:{Low,Medium,High,Critical}` | Test importance level |
| `Development-Only` | Tests that only run in dev environments |
| `Integration-Only` | Tests that only run in integration |
| `PreLaunchSetup:None` | Tests that require no pre-existing infrastructure |
| `ARO-HCP-RP-API-Compatible` | Tests compatible with both ARM and ARO HCP RP APIs |

## Periodic Tests

In addition to PR-triggered tests, periodic Prow jobs run E2E tests on a schedule:

- **Integration**: After each EV2 promotion (gates promotion to Stage)
- **Stage**: Daily at 2:00 AM UTC; at 11:00 PM UTC for OCP nightly
- **Production**: Daily at 2:00 AM UTC; at 11:00 PM UTC for OCP nightly

These are documented in detail in [Prow Jobs](prow.md#periodic-e2e-tests).

## Related Documentation

- [Prow Jobs](prow.md) - Full Prow job reference
- [Test Test Tenant Access](sops/test-test-tenant-access.md) - Access to Microsoft test tenant (for CI infrastructure management only)
- [E2E Test Code](../test/e2e/) - Test source code
