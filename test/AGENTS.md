# Principles of Good E2E Test Case Design

## Provision HCP cluster

* **Cluster creation:** Cluster creation, which leverages methods from the framework module, offers three main approaches for creating and deploying an HCP cluster: `CreateHCPClusterFromParam20240610`, which handles creation and automatically waits for successful deployment; `BeginCreateHCPCluster20240610`, which initiates the process but requires explicit test logic to wait for provisioning completion; and an alternative using `CreateHCPClusterFromParam20240610` with a 0-second timeout, which executes the creation but immediately bypasses the waiting phase for provisioning to finish.
* **Cluster Params:** The `NewDefaultClusterParams20240610` method from the framework module should be used to configure the default cluster parameters. Before creating cluster customer resources, the `ClusterName` parameter must be set. Different cluster configurations can be achieved by assigning custom values to the parameters.
* **Prepare cluster customer resources:** Creating a cluster requires several
  resources (like an NSG, VNet, subnet, and managed identities). To create
  these resources and set the cluster's parameters, use the
  `CreateClusterCustomerResources20240610` method from the framework module.
  Use `RBACScopeResourceGroup` as RBACScope argument of
  `CreateClusterCustomerResources20240610` function, but make sure that
  `framework.RBACScopeResource` is used in at least one test case in E2E
  test suite.
* **Nodepool creation:** To create a nodepool, utilize the `CreateNodePoolFromParam20240610` method. Beforehand, the default nodepool parameters should be prepared using the `NewDefaultNodePoolParams20240610` method. Both of these methods are located within the `framework` module. Like cluster parameters, custom configurations can be assigned to the nodepool parameter values.
* **API version suffix convention:** Framework helpers that call the ARM SDK must use explicit API version suffixes in their names. Prefer `...20240610` helpers for the stable test path (for example `GetHCPCluster20240610`, `UpdateHCPCluster20240610`, `CreateNodePoolAndWait20240610`) and `...20251223` or any future versions helpers only when testing preview-specific behavior (for example `BuildHCPClusterFromParams20251223`, `CreateHCPClusterAndWait20251223`).
* **Timeouts:** Add named constants in [`test/util/framework/constants.go`](util/framework/constants.go) only for durations **shared across multiple test cases** (same ARM operation, framework helper, or verifier pattern). A timeout that is unique to one test and used once can stay as a local literal (e.g. in an `Eventually` block). When several tests need the same budget, use the matching constant instead of repeating a magic number:
  * **Provisioning:** `ClusterCreationTimeout`, `NodePoolCreationTimeout`, `ExternalAuthCreationTimeout`
  * **Access Cluster:** `GetAdminRESTConfigTimeout` (for `GetAdminRESTConfigForHCPClusterYYYYMMDD` and similar credential fetches)
  * **Deletion:** `HCPClusterDeletionTimeout` (for `DeleteHCPClusterYYYYMMDD`, inline delete pollers, and per-cluster deletes in `DeleteAllHCPClusters`)
  * **Updates:** `UpdateHCPClusterTimeout` (PATCH/update cluster properties), `HCPClusterVersionUpgradeTimeout` (control plane version upgrades), `NodePoolVersionUpgradeTimeout` (node pool version upgrades), `NodePoolScalingTimeout` (replica changes and autoscaling updates)
  Add a new constant only when a second (or later) test needs the same duration. See [`test/e2e/README.md`](e2e/README.md#updating-e2e-timeouts) for the constant table and how to tune shared values from telemetry.

> [!NOTE]
All cluster and node pool operations are tied to a specific API version. This means that the actual method names have the form `CreateHCPClusterFromParamYYYYMMDD`, `CreateNodePoolFromParamYYYYMMDD`, etc. New methods for new API versions should be added as needed.

## Resource naming \- Independence and Isolation

* **Self-Contained:** Every test case must be self-contained, ensuring no dependencies on the state or results of other test cases.
* **Parallel execution:** Tests are executed parallely thus ensuring names are unique across the subscription: customer resource group (handled by method `tc.NewResourceGroup()`), managed resource group, and cluster names within one resource group, if multiple are created. Bicep deployment names must be unique within the resource group.

## HCP SDK client helper

* **HCP SDK:** Currently we are using an unreleased generated ARO HCP Golang SDK. SDK is located under the module test.
* **HTTPS Requests:** To interact with RP/ARM, use the hcp client.. Use context with timeout to cancel requests which are asynchronous.
* **Code location:** Such helper functions should be located in the util module `framework` in file `hcp_helper.go`. ([https://github.com/Azure/ARO-HCP/blob/main/test/util/framework/hcp\_helper.go](https://github.com/Azure/ARO-HCP/blob/main/test/util/framework/hcp_helper.go))

## Kubernetes verifiers

* **K8S client-go:** Use this client to communicate with created HCP clusters. Client requires rest Config which is provided by method `GetAdminRESTConfigForHCPCluster20240610` with 10 minutes timeout.
* **HostedClusterVerifier:** This interface is designed for all verifiers and provides the essential `Name` and `Verify` methods for extension.
* **Parallel checks:** Use `VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyFoo(), verifiers.VerifyBar(), ...)` to run independent verifiers in parallel. Each verifier is responsible for its own polling, diagnostics, and delta-only logging when it needs to wait.
* **Polling:** Verifiers that poll require a timeout parameter (e.g. `verifiers.VerifyDaemonSetReady(ns, name, 10*time.Minute)`). The timeout must be > 0; passing zero is a runtime error. Verifiers that are inherently single-shot (e.g. `VerifyPullSecretAuthData`) do not accept a timeout. Polling runs inside each verifier's `Verify` method via shared helpers in `poll.go` using `verifiers.DefaultPollInterval`. Reuse `verifiers.VerifyDaemonSetReady(namespace, name, timeout)` for any DaemonSet readiness check; `VerifyGlobalPullSecretSyncer` is a thin alias for the syncer in kube-system.
* **Phased checks:** When a verifier depends on resources established by earlier steps (e.g. Cilium running before a web app test), run `VerifyHCPCluster` for the independent batch first, then call dependent verifiers with `Expect(verifier.Verify(...)).NotTo(HaveOccurred(), "...")` in a later `By` step. See `cluster_create_cni_cilium.go`.
* **Code location:** Verifiers are located in the util module `verifiers`. ([https://github.com/Azure/ARO-HCP/tree/main/test/util/verifiers](https://github.com/Azure/ARO-HCP/tree/main/test/util/verifiers))

## Cleanup of Resources

* **TestContext:** Using [TestContext](https://github.com/Azure/ARO-HCP/blob/main/test/util/framework/per_test_framework.go#L51), to create a resource group, will automatically register it for a cleanup after the test. The cleanup process involves deleting all HCP clusters within the designated resource groups (via `DeleteAllHCPClusters20240610`). The resource groups themselves are removed, along with any remaining Azure resources.
* **Test resources:** Within the test, remove any created resources that are not part of the TestContext resource group or its associated HCP clusters. Ensure all tests start from a known, clean state to avoid flakiness and false positives.

# Best Practices for Writing E2E Test Cases

## Assertion Messages

* **Descriptive nil checks:** Every `Expect(x).NotTo(BeNil())` or `Expect(x).ToNot(BeNil())` call **must** include an annotation string describing which property is being checked. Bare `BeNil()` assertions produce unhelpful failure messages like `Expected <*string | 0x0>: nil not to be nil` that require mapping back to source code to diagnose. Use Gomega's annotation argument to add context:
  * `Expect(resp.Properties).NotTo(BeNil(), "cluster response Properties was nil")`
  * `Expect(resp.Properties.API.URL).NotTo(BeNil(), "cluster Properties.API.URL was nil")`
  * Format strings are supported: `Expect(x).NotTo(BeNil(), "property %s was nil for cluster %s", propName, clusterName)`
* **Descriptive error checks:** Every `Expect(err).NotTo(HaveOccurred())` or `Expect(err).To(HaveOccurred())` call **must** include an annotation string describing what operation was being attempted. Bare `HaveOccurred()` assertions produce failure messages like `Unexpected error: <raw error>` that give no indication of what the test was trying to do when it failed. A reader should be able to understand the failure without consulting the source code. Use Gomega's annotation argument to add context:
  * `Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster")`
  * `Expect(err).NotTo(HaveOccurred(), "failed to verify a simple web app can run on the cluster")`
  * `Expect(err).To(HaveOccurred(), "expected error when requesting serial console logs for non-existent VM")`
  * Format strings are supported: `Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q", nodePoolName)`
  * The message should describe the **intent** (what we were trying to achieve), not merely restate the function name. For example, prefer `"failed to verify the cluster is healthy"` over `"VerifyHCPCluster returned error"`.
  * When a helper function returns an error, context should exist at **both layers**: the error itself should describe the proximal failure (e.g. `"route never returned a successful response: TLS error..."`), and the gomega annotation should describe the higher-level intent (e.g. `"failed to verify a simple web app can run on the cluster"`).
* **General assertion clarity:** For any assertion where the failure message would be ambiguous (e.g. checking pointer values, map entries, or deeply nested fields), include an annotation string that identifies the value being checked and the expected condition.

## Logging

* **Log message structure:** Ensure log messages are direct, including the actual error message if an error occurs.
* **Preferred Logger:** For logging, use `ginkgo.GinkgoLogr` to generate info or error entries in the output. This is the preferred logger. Alternatively, you can use the `klog` from the `k8s.io` module. To measure the execution time of a specific method, utilize the `RecordTestStep` function as a `defer` call within that method.
* **Ginkgo Writer:** Use `ginkgo.GinkgoWriter` to create a regular message in output.

### Logging in Eventually/Polling Tests

When writing tests that poll until a condition is met (e.g. `Eventually(...)`, `wait.PollUntilContextTimeout`, or any retry loop), follow these rules to produce output that is actionable when failures occur:

1. **Delta-only logging:** Only emit a log line when the observed state *changes* between poll iterations. Logging the same message every 15 seconds creates noise and hides real transitions. Track the previous error/state string and compare before logging.

2. **Minimal state representation:** Find the most compact way to represent what you observed. Prefer a single structured line over multi-line dumps during polling. For example:
   ```
   found 2/3 nodes upgraded to release image v4.16.1; 1 node still on v4.15.8: [worker-2]
   ```

3. **Expected vs. observed:** Every log line during polling and every failure message must make it clear what was expected and what was actually seen. A reader who has never seen the test code should be able to understand the problem from the output alone.

4. **Consider dumping targeted state on failure:** When a polling loop times out, it is strongly recommended to dump the status of the specific resources you were directly polling — e.g. if you were waiting for Machines to upgrade, dump those Machine statuses. Keep this narrowly scoped to avoid log clutter; broad cluster diagnostics should be left to `oc adm inspect` or equivalent artifact collection. The goal is that the most immediately relevant context appears inline next to the failure message.

5. **Polling inside verifiers:** Verifiers that poll require a timeout parameter (e.g. `verifiers.VerifyDaemonSetReady(ns, name, 10*time.Minute)`); the timeout must be > 0. Verifiers that are inherently single-shot (e.g. `VerifyPullSecretAuthData`) do not accept a timeout. Each verifier runs polling inside its own `Verify` method using shared helpers in `test/util/verifiers/poll.go` (delta-only logging, elapsed-time reporting, optional diagnostics). When polling completes, verifiers log actual wall-clock duration (success and timeout) via `GinkgoLogr` (`elapsed` field) and `GinkgoWriter` (`[VerifierName] succeeded after 1m23s`). Tests should call `Expect(verifier.Verify(ctx, cfg)).NotTo(HaveOccurred(), "intent message")` — the annotation describes what the test was trying to do; the returned error carries the proximal failure (see **Descriptive error checks** above). Do not add test-level `EventuallyVerify`-style wrappers or wrapper verifiers that rerun another verifier's `Verify`. Verifiers with bespoke polling needs (e.g. exponential backoff or multi-phase waits) implement that logic directly in `Verify`, as `VerifyAllClusterOperatorsAvailable` does.

6. **Think about failure before writing the test:** Test authors must consider what happens when the test fails. Before submitting a test, intentionally trigger a failure and verify that the error output answers: *what went wrong, what was expected, and what information does someone need to debug it?* CI log readers and agentic debug flows should not need to open test source to understand intent.

## Labels

* **Importance:**
  * *Critical*: blockers for rollout
  * *High*: significant problems affecting a feature
  * *Medium*: less frequent scenarios
  * *Low*: very specific scenarios or enhancements to user experience
* **Positivity:** Positive/negative test scenarios.
  * *Positive*
  * *Negative*
* **ARO HCP environment:** Test cases for one environment.
  * *IntegrationOnly*
  * *StageOnly*
* **API compatibility:**
  * *AroRpApiCompatible*: This label indicates tests that are compatible with both the development environment (directly against ARO HCP RP without ARM) and higher environments (with ARM).

## File Structure

* **File naming convention:** When naming new test files, avoid using the \_test suffix. The Ginkgo OpenShift test extension specifically excludes files with this suffix during direct test imports.

## ARO HCP Documentation

* Test developers should refer to the ARO HCP Documentation to understand their test cases.

## Linting and go workspace

* **Go Workspace:** Module tests are managed within a Go workspace, which means that the `go.sum` and `go.mod` files for all modules in the workspace maintain synchronized versions of their shared imported modules. The modules included in the Go Workspace are specified in the `go.work` file.
* **Linting:** The ARO HCP project utilizes `golangci-lint` for linting. This is executed for Pull Requests via the `ci-go` GitHub workflow's `lint` step. To perform local linting, run make lint from the repository's root directory.
* **Makefile:** The `Makefile` in the repository's root directory includes an `all-tidy` rule. This rule guarantees the correct implementation of the golang mechanisms and enforces the license header.

## Azure Location

* When creating a new resource group, use the `tc.Location()` method to retrieve the globally configured location from the environment variable instead of hardcoding Azure location names.

# Tips and Tricks for Effective E2E Testing

* **Randomize strings:** To create unique resource names, use the `rand.String(n int)` function from the `k8s.io/apimachinery/pkg/util/rand` library to generate random strings. A length of 6 characters is typically sufficient.
* **Descriptive Names:** Use descriptive names for test files, test cases, bicep/arm deployments, and functions.
* **Test case description:** Maintain descriptions of specifications and tests as informative and comprehensive complete sentences.
* **Development environment test cases:** Ensure new negative test cases produce the same result in development and higher environments by running them in both. Do not use the label `AroRpApiCompatible` if the test case fails in the development environment.

## Running or Filtering Specific E2E Tests

This project does NOT use Ginkgo's Focus (`FDescribe`, `FIt`, `FEntry`) to select tests.

### Local: CLI-based filtering

Use the `aro-hcp-tests` binary to run individual tests or suites locally.
Build it first with `make -C test`, then:

```bash
# List available tests
./test/aro-hcp-tests list | jq '.[].name'

# Run a single test by name
./test/aro-hcp-tests run-test "Customer should be able to create an HCP cluster using bicep templates"

# Run a test suite
./test/aro-hcp-tests run-suite "integration/parallel"
```

See `test/e2e/README.md` for full details on environment setup and available suites.

### CI / PR validation: MustFilter in main.go

To run specific tests in a PR against Int, Stage, or Prod environments, use
`specs.MustFilter()` in `test/cmd/aro-hcp-tests/main.go` with CEL expressions.
This is the only way to select tests for CI runs in higher environments.

Locate the commented line and uncomment/modify it:

```go
// specs = specs.MustFilter([]string{`name.contains("filter")`})
```

Filter examples:

```go
// Filter by name
specs = specs.MustFilter([]string{`name.contains("z-stream upgrade")`})

// Filter by label
specs = specs.MustFilter([]string{`labels.exists(l, l=="Positivity:Positive")`})

// Combine filters (name AND label)
specs = specs.MustFilter([]string{`name.contains("Cluster") && labels.exists(l, l=="Importance:Critical")`})
```

**WARNING**: Always revert `MustFilter` edits before merging. Leaving a filter
in place silently skips tests in CI.

---

# E2E Test Code Review Standards

This section defines the standards for reviewing E2E test code changes. These rules supplement the general project [PR standards](../CONTRIBUTING.md#pull-request-standards) with E2E-specific requirements.

## Build Tags and File Naming

- **Build tag requirement**: Only the main entry point file (`test/e2e/e2e_test.go`) should have the `//go:build E2Etests` build tag
- **File naming**: E2E test files should NOT use the `_test.go` suffix (except for framework unit tests in `test/util/framework/*_test.go` and the entry point `test/e2e/e2e_test.go`)
  - ✅ Good: `cluster_create.go`, `admin_api.go`, `cluster_pullsecret.go`
  - ❌ Bad: `cluster_create_test.go` (Ginkgo OpenShift extension excludes these during imports)
  - Exception: Framework helper unit tests like `per_test_framework_test.go` should use `_test.go` suffix

## Test Structure (Ginkgo/Gomega)

- **Spec organization**: Use clear hierarchies:
  - `Describe` → `It` (simple tests)
  - `Describe` → `Context` → `It` (grouped scenarios)
  - Multiple nested `Describe` blocks for complex organization
- **Descriptive test names**: Write complete, readable sentences that form a narrative when concatenated
  - Example: "Get HCPOpenShiftCluster" → "Fails to get a nonexistent cluster with a Not Found error"
  - Reads as: "Get HCPOpenShiftCluster fails to get a nonexistent cluster with a Not Found error"
- **Use `By()` for test steps**: Document critical steps within `It` blocks using `By("step description")`
- **Context parameter**: All `It` blocks must accept `context.Context` as their first parameter
  - ✅ Good: `It("should create cluster", func(ctx context.Context) { ... })`
  - ❌ Bad: `It("should create cluster", func() { ctx := context.Background(); ... })`

## Required Test Labels

Every test MUST include appropriate labels from these categories:

### Test Environment Labels (MANDATORY - exactly one):
- `labels.RequireNothing`: Per-test cluster tests (creates own cluster) — **preferred approach**
- `labels.RequireHappyPathInfra`: Per-run cluster tests (uses pre-created cluster)

### Importance Labels (MANDATORY - exactly one):
- `labels.Critical`: Blockers for rollout
- `labels.High`: Significant problems affecting a feature
- `labels.Medium`: Less frequent scenarios
- `labels.Low`: Very specific scenarios or enhancements

### Positivity Labels (MANDATORY - exactly one):
- `labels.Positive`: Happy path test scenarios
- `labels.Negative`: Error/failure test scenarios

### Optional Usage Labels:
- `labels.CreateCluster`: Cluster creation related tests
- `labels.SetupValidation`: Pre-test validation
- `labels.TeardownValidation`: Post-test validation
- `labels.CoreInfraService`: Gates rollout of ARO-HCP components
- `labels.AroRpApiCompatible`: Can run against both ARO HCP RP and ARM endpoint (dev environment compatible)

### Optional Environment Labels:
- `labels.DevelopmentOnly`
- `labels.IntegrationOnly`
- `labels.StageAndProdOnly`

### Resource Demand Labels (when applicable):
- `labels.MIDemandHigh`: Needs multiple managed identity containers
- `labels.MIDemandMedium`: Needs more than one container

### Speed Labels (when applicable):
- `labels.Slow`: For tests that take significantly longer than average

**Example**:
```go
It("should create cluster successfully", 
    labels.RequireNothing, 
    labels.Critical, 
    labels.Positive, 
    labels.CreateCluster,
    func(ctx context.Context) {
        // test code
    })
```

## Client Usage Patterns

### HCP SDK Client:
- Use `tc.Get20240610ClientFactoryOrDie(ctx)` to get client factory
- Chain to specific clients: `.NewHcpOpenShiftClustersClient()`, `.NewHcpOpenShiftClusterNodePoolsClient()`
- Always use context with timeout for async operations

### Kubernetes Client:
- Use `framework.GetAdminRESTConfigForHCPCluster20240610()` to get REST config (10-minute timeout)
- Use standard `client-go` libraries for K8s operations
- Verifiers are in `test/util/verifiers/` and implement `HostedClusterVerifier` interface

## Verifier Patterns

- **Interface**: Implement `HostedClusterVerifier` interface with `Name()` and `Verify()` methods
- **Location**: All verifiers belong in `test/util/verifiers/`

## Context and Cancellation

- **Always use context**: Every test should accept and use `context.Context`
- **Propagate context**: Pass context to all SDK calls, framework helpers, and verifiers
- **Respect cancellation**: Don't ignore context cancellation in long-running operations

## Pooled Identities

- **Check if enabled**: Use `tc.UsePooledIdentities()` to check if pooled identities are enabled
- **Assign containers**: Call `tc.AssignIdentityContainers(ctx, count, timeout)` before resource creation
  - Example: `tc.AssignIdentityContainers(ctx, 1, 60*time.Second)`
- **Error handling**: Expect assignment to succeed: `Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")`

## Error Handling in Negative Tests

- **Explicit error expectations**: Negative tests must assert on specific error messages
  - ✅ Good: `Expect(err.Error()).To(ContainSubstring("The location property is required"))`
  - ❌ Bad: `Expect(err).ToNot(BeNil())` (too vague)
- **Case-insensitive matching**: Use `strings.ToLower()` for error message comparisons when case might vary

## Code Organization

Helper functions should be placed in appropriate `test/util/` modules:
- `test/util/framework/`: HCP client helpers, cluster operations
- `test/util/verifiers/`: Kubernetes verifiers
- `test/util/labels/`: Label definitions
- `test/util/timing/`: Timing utilities
- `test/util/cleanup/`: Cleanup helpers

Shared test artifacts should use `//go:embed test-artifacts` pattern and `embed.FS` for embedded resources.

## Test Fixtures

After adding tests with environment-specific behavior:
- Run `make update-go-fixtures` or `make update` in `test/` directory
- Required to ensure test environment detection works correctly

## Development Environment Compatibility

- **AroRpApiCompatible label**: Only use if test produces same results in dev AND higher environments
- **Test in both environments**: Run negative tests in both dev and integration/higher to verify behavior matches
- **Do NOT label if dev-incompatible**: Remove label if test fails in development environment

## Common Anti-Patterns to Reject

The following patterns should be rejected in code review:

❌ **Missing assertion messages**: Bare `Expect().NotTo(BeNil())` or `Expect(err).To(HaveOccurred())`
❌ **Hardcoded timeouts**: Using literals instead of named constants from `constants.go` (except for test-specific timeouts used only once)
❌ **Missing labels**: Tests without environment, importance, or positivity labels
❌ **Hardcoded locations**: `"eastus"` instead of `tc.Location()`
❌ **Non-unique names**: Static resource names that will collide in parallel runs
❌ **Focused tests**: Using `FIt`, `FDescribe`, or `FEntry`
❌ **Noisy polling**: Logging same message every iteration of `Eventually()`
❌ **Vague error assertions**: Checking `err != nil` without validating error content in negative tests
❌ **Missing context**: `It()` blocks without `context.Context` parameter
❌ **Wrong file suffix**: Using `_test.go` for E2E test files (except framework unit tests)
❌ **Missing `By()` steps**: Complex tests without documented steps
❌ **Abandoned resources**: Creating resources outside TestContext without explicit cleanup

## Code Review Checklist

When reviewing E2E test PRs, verify:

- [ ] All base PR standards from [`../CONTRIBUTING.md`](../CONTRIBUTING.md#pull-request-standards) are met
- [ ] Test files use correct naming convention (no `_test.go` suffix for E2E tests)
- [ ] All tests have required labels (environment, importance, positivity)
- [ ] All assertions include descriptive messages
- [ ] Timeouts use named constants from `constants.go` (for shared durations) or are local literals (for test-specific timeouts)
- [ ] Resource names are unique (using `rand.String()` or framework helpers)
- [ ] TestContext is used for resource group creation (auto-cleanup)
- [ ] No hardcoded Azure locations (use `tc.Location()`)
- [ ] API version suffixes are explicit in framework helper names
- [ ] Context is properly propagated through all operations
- [ ] Polling/Eventually blocks use delta-only logging
- [ ] Negative tests assert on specific error messages
- [ ] No Focus helpers (`FIt`, `FDescribe`, etc.) are used
- [ ] Test fixtures updated if needed (`make update-go-fixtures`)
- [ ] Tests are self-contained and don't depend on other test state
- [ ] `By()` statements document critical test steps
- [ ] Cleanup is properly handled (automatic via TestContext or explicit)
