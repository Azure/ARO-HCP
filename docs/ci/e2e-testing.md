# Running E2E Tests In CI

This guide covers how to run the ARO HCP E2E tests from a pull request, how to narrow a run to a single test, and why manual testing against the shared INT, STG and PROD environments is not allowed. For what each kind of job validates and how it is wired, see [CI Execution](execution.md); for inspecting and changing the jobs, see [CI Operations](operations.md).

## Why No Manual Testing Against INT, STG, Or PROD

The shared Integration, Stage and Production environments have limited management and service cluster capacity. Clusters created by hand with `az` or the portal consume the same capacity that automated E2E runs and real rollouts need, and in the past they have blocked rollouts. The subscriptions used for E2E clusters are also reserved for Prow, and direct access to them has been disabled so that nobody creates resources there by accident.

So all E2E testing in those environments goes through Prow jobs triggered from PRs. If a scenario you care about is not covered by an existing test, write a test and validate it through CI; needing a manual check is a sign of a coverage gap.

> [!NOTE]
> The rule is about creating clusters by hand. Triggering the gate job itself, for example re-running a failed EV2 `regionalGating` job or running the suite from a branch, is a documented operational procedure and still executes the tests in Prow. See [Manually Trigger an E2E Gate Run](../sops/manual-e2e-gate-run.md).

## Running Tests Via PR

Jobs are triggered with PR comments on [Azure/ARO-HCP](https://github.com/Azure/ARO-HCP). `/retest` reruns all failed jobs, and `/test <name>` runs a single job. The job names follow a pattern, so the table lists families rather than every variant; the exact set is in the [ci-operator config](https://github.com/openshift/release/tree/main/ci-operator/config/Azure/ARO-HCP) and in the PR's list of available jobs (`/test ?`).

| Job | Environment | Trigger | Purpose |
|-----|-------------|---------|---------|
| `e2e-parallel` | DEV, provisioned per run | Automatic, required | Runs the default E2E suite against a fresh DEV service footprint built from the PR, so it can validate unmerged RP and deployment-artifact changes. |
| `e2e-parallel-inplace-upgrade` | DEV, provisioned per run | Automatic, required | Provisions a footprint from `main`, rolls it forward to the PR, and checks that existing customer clusters survive. See [In-Place Upgrade Presubmit](inplace-upgrade-presubmit.md). |
| `e2e-parallel-slow`, `e2e-parallel-ocp-nightly` | DEV | Manual, optional | The slow test suite, and the default suite against the latest nightly OCP build. |
| `integration-e2e-parallel`, `stage-e2e-parallel`, `prod-e2e-parallel` | INT, STG, PROD | Manual, optional | The environment's fast suite against the existing shared environment. |
| `<env>-e2e-parallel-slow`, and `-all` for INT and STG | INT, STG, PROD | Manual, optional | The slow tests, or fast and slow together. |
| `<env>-e2e-parallel-ocp-{fast,stable,nightly}` | INT, STG, PROD | Manual, optional | The fast suite against a different OCP update channel, to catch compatibility problems with newer OCP builds early. See [update channels](https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html/updating_clusters/understanding-openshift-updates-1#update-availability_understanding-openshift-updates). |
| `<env>-canary-arm-endpoint-e2e-parallel`, and `-slow` for STG and PROD | INT, STG, PROD | Manual, optional | The same suites sent through the ARM canary endpoint (`eastus2euap`). |

The two automatic DEV jobs are scheduled by Prow's pipeline controller once the cheap first-wave checks pass, and are skipped for PRs that only touch documentation and similar files. They are required for merge.

### What These Jobs Test

The jobs against INT, STG and PROD only validate E2E test code and test expectations before merge. The RP and infrastructure in those environments are not updated until after merge, so a PR's service changes cannot be exercised there. The DEV jobs are different because they deploy the PR's own footprint; [CI Execution](execution.md#execution-modes) explains the distinction and its limits.

DEV jobs pick their Azure region per run from a weighted set of regions; see [DEV CI Regional Load Management](dev-region-load-management.md) for how that is configured and changed.

## Running Only Specific Tests

To validate one test or a small group against a shared environment, temporarily filter the suite in [`test/cmd/aro-hcp-tests/cli.go`](../../test/cmd/aro-hcp-tests/cli.go). The file contains a commented-out `MustFilter` example. Enable it with a [CEL expression](https://github.com/google/cel-spec) that matches the tests you want:

```go
specs = specs.MustFilter([]string{`name.contains("ImageDigestMirrors")`})
```

The workflow is:

1. Create a branch, add the filter, push, and open a PR.
2. Trigger the matching job, for example `/test stage-e2e-parallel`.
3. When validation is done, close the PR or remove the filter before merging.

> [!IMPORTANT]
> Never merge a filter. It would silently skip tests in every CI run.

[PR #4597](https://github.com/Azure/ARO-HCP/pull/4597) is a worked example. Filters can also match on labels and be combined:

```go
specs = specs.MustFilter([]string{`labels.exists(l, l=="Positivity:Positive")`})
specs = specs.MustFilter([]string{`name.contains("Cluster") && labels.exists(l, l=="Importance:Critical")`})
```

## Test Suites And Labels

Each CI job runs one named suite, which is a label-based selection of the tests under [`test/e2e`](../../test/e2e/). The suites are registered in [`test/cmd/aro-hcp-tests/suites.go`](../../test/cmd/aro-hcp-tests/suites.go):

| Suite | Used for |
|-------|----------|
| `integration/parallel`, `stage/parallel`, `prod/parallel` | The fast tests for each persistent environment. Each also has a `/slow` variant and an `/all` variant covering both. |
| `rp-api-compat-all/parallel` (and `/slow`) | The default DEV suite: tests that work against both the ARO HCP RP and the ARM endpoint. |
| `upgrade/in-place` | The in-place upgrade test, used only by `e2e-parallel-inplace-upgrade`. |
| `hypershift-presubmit/parallel` | The subset run from HyperShift PRs; see [HyperShift v2 Checks](hypershift-v2-presubmit.md). |
| `expect-failure` | Tests that reproduce a known, tracked bug. They are kept out of every other suite. |
| `dev-cd-check/parallel` | The final check in the DEV continuous-deployment workflow. |

Labels decide suite membership, for example slow versus fast, positive versus negative, and which environments a test may run in. They are defined in [`test/util/labels/labels.go`](../../test/util/labels/labels.go), and [`test/AGENTS.md`](../../test/AGENTS.md) describes which labels every new test must carry.

## DEV Frontend Ingress

The DEV frontend only accepts traffic from addresses that have been allowed through the service cluster's network security group, so a CI job has to allow its own runner's public IP before it can call the RP. The `aro-hcp-tests infra` subcommands `discover-public-ip` and `frontend-grant-ingress` do this, and `make frontend-grant-ingress` combines them using the current Azure CLI subscription unless `INFRA_SUBSCRIPTION_ID` is set. The rule format and its place in the configuration are described in [Ingress and Egress](../ingress-egress.md#additional-service-cluster-ingress-rules).

## Periodic Tests

Besides PR-triggered runs, periodic Prow jobs run the E2E suites on a schedule, defined in the [periodic ci-operator config](https://github.com/openshift/release/blob/main/ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__periodic.yaml):

- Stage and Prod each run a daily full-suite job (`periodic-<env>-e2e-parallel-all`, early morning UTC) and a nightly OCP job (`periodic-<env>-e2e-parallel-ocp-nightly`, evening UTC).
- Prod additionally runs weekly OCP candidate-channel jobs, one per candidate version.
- The Integration and canary-endpoint jobs are present but effectively on demand, because their schedule is a placeholder.

[CI Execution](execution.md#periodic-jobs) explains why periodic jobs exist alongside EV2 gating, and [Daily CI Digest Instructions](periodic-analysis-instructions.md) describes how the Stage and Prod results are reviewed.

## Related Documentation

- [CI Overview](README.md)
- [Manually Trigger an E2E Gate Run](../sops/manual-e2e-gate-run.md)
- [Test Test Tenant Access](../sops/test-test-tenant-access.md)
