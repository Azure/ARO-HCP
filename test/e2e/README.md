# E2E Testing - ARO HCP E2E Test Suite
The E2E test suite will work in every environment of the ARO-HCP project. Its main purpose is to ensure specific functionality based on the environment and its usage.

For more information about ARO HCP environments, see the [ARO HCP Environments documentation](https://github.com/Azure/ARO-HCP/blob/main/docs/environments.md).

## Writting and running new E2E Test cases

### Resource Naming

> **Important:** These tests are running in parallel so it is **VITAL** that we avoid naming collisions with other tests that may be running in CI at the same time. This may break CI runs until the duplicate resources are removed! 
- The customer resource group name must be unique across the subscription. Using NewResourceGroup() from the framework will provide a unique resource group name as well as handle the cleanup.  
- The combination of the cluster name and the managed resource group name must be unique across the subscription. If you use the provided bicep templates they will handle this by appending -rg to the customer resource group name.
- If the test creates more than one cluster at a time, the names of the clusters must be unique.
- Bicep deployment names must be unique within the same resource group.
- When using your own customized bicep templates or creating resources via other means such as direct API calls be sure to follow the above rules, appending a 6 character random string to the cluster and managed resource group names is likely sufficient.

### Test cases with per-test cluster (**main focus**)
When writing E2E test cases that provision their own cluster (i.e., the test case is responsible for creating and deleting the cluster within its `Context` or `It` block), follow these guidelines:

- **Label Requirement:** You **MUST** add the `RequireNothing` label to these test cases. This label ensures the test is always considered for CI execution.
- **Triggering in CI:** These tests will automatically run in the CI pipeline when a pull request is created or updated. Specifically, they are executed as part of the `ci/prow/integration...` and `ci/prow/stage...` jobs.

See [`test/e2e/complete_cluster_create.go`](complete_cluster_create.go) for a reference implementation.

> **Note:** Creating per-test cluster test cases is the **main focus** of this test suite. Whenever possible, prefer writing per-test cluster test cases over per-run cluster test cases. Priority may change in future.

**Minimal example:**
TODO: Minimal version 

### Test cases with per-run cluster
> **Important**
> - You can use the `FALLBACK_TO_BICEP` environment variable to populate the e2esetup models and run tests that require e2esetup to be present.
> - Currently, per-run test cases can only be executed locally. Running these tests in CI is not yet supported.

When writing E2E test cases that use a **per-run cluster** (i.e., the cluster is created once for the test run and shared across multiple test cases), follow these guidelines:

- **Setup Requirement (Test Case Structure):** These tests require the e2esetup models to be populated. This can be achieved by either:
  - Setting the `FALLBACK_TO_BICEP` environment variable to the name of a Bicep file to be used for populating e2esetup models, **or**
  - Setting the `SETUP_FILEPATH` environment variable to the path of a valid `e2e-setup.json` file describing your cluster and environment.
- **Label Recommendation:** You **SHOULD NOT** add the `RequireNothing` label to these test cases; instead of `RequireNothing` use `RequireHappyPath`, or other labels that indicate the test's requirements (e.g. `Create-Cluster`, `Setup-Validation`, `Teardown Validation`, etc.) so the test runner can select appropriate tests based on the environment and setup.

For more details on the setup file and fallback logic, see the [Setup File](#setup-file) and [Fallback to Bicep Setup](#fallback-to-bicep-setup) sections below.

> **Note:**
> - The cluster and its resource group will be deleted after running the tests.
> - Fallback to Bicep setup is only supported when running against the integration or higher environment.
> - Test case files should be named with the `_test.go` suffix. For example, see [`test/e2e/cluster_list_test.go`](cluster_list_test.go) for a reference.
> - When using `FALLBACK_TO_BICEP`, you must run the `bicep-build` Makefile rule before running the E2E Test Suite to ensure the Bicep file is properly built and available.
> - If `FALLBACK_TO_BICEP` is set, the `SETUP_FILEPATH` variable must either be unset or set to a non-existent `e2e-setup.json` file. This ensures the test suite will trigger the fallback logic and use the Bicep file for setup.

**Minimal example:**
TODO: Minimal version

#### Build Tag (per-run only)
To distinguish E2E test suite from unit tests, initial ginkgo file *e2e_test.go* has a build tag **E2Etests**. The build tag has to be explicitly set when running (or building) the E2E test suite.

#### Setup File
To run the E2E Test Suite against a pre-created cluster, set the environment variable **SETUP_FILEPATH** to the path of the `setup.json` file that describes your cluster and environment. This allows the test suite to use the provided configuration instead of provisioning new resources.

The structure and models for the `setup.json` file are defined in */test/util/integration/setup_models.go*. These models consist of:
- Test profile name and tags
- Customer environment
- Cluster and its nodepools

#### Fallback to Bicep Setup

If you want the E2E Test Suite to use a Bicep file as a fallback for setup, set the environment variable **FALLBACK_TO_BICEP** to the name of the Bicep file (without the `.bicep` extension) you wish to use (e.g., `demo` for `demo.bicep`).

#### Running per-run test cases locally against integration environment using Bicep fallback

1. Login with AZ CLI
2. Export environment variables CUSTOMER_SUBSCRIPTION, and FALLBACK_TO_BICEP (do not set SETUP_FILEPATH, or set it to a non-existent file):

```bash
export CUSTOMER_SUBSCRIPTION=<subscriptionName>
export LOCATION=uksouth
export FALLBACK_TO_BICEP=demo  # for demo.bicep
unset SETUP_FILEPATH  # or: export SETUP_FILEPATH=nonexistent-e2e-setup.json
```

3. Build the Bicep file before running tests:

```bash
make bicep-build
```

4. Run test suite with the `RequireHappyPath` label:

Run all test cases: `ginkgo --tags E2Etests --label-filter='PreLaunchSetup:HappyPathInfra' ./`

Run specific test case: `ginkgo --tags E2Etests --label-filter='PreLaunchSetup:HappyPathInfra' --focus "<regex>" ./`

Run in debug mode: `ginkgo --tags E2Etests --label-filter='PreLaunchSetup:HappyPathInfra' --vv ./`

## E2E Test Suite Configuration
To run the test suite, you must configure the following environment variables.

### Customer Subscription (CUSTOMER_SUBSCRIPTION)
Set the **CUSTOMER_SUBSCRIPTION** environment variable to the **name** of the Azure subscription you want the client to use. The specified subscription must already be registered.

### Artifact Directory (ARTIFACT_DIR)
Set the **ARTIFACT_DIR** environment variable to specify the directory where test artifacts and logs will be saved. This is especially useful in CI environments to collect and persist test outputs.

### Shared Directory (SHARED_DIR)
Set the **SHARED_DIR** environment variable to specify a directory for sharing files between different CI steps or test invocations. This directory is used for storing files that need to be accessed across multiple test runs or scripts.

### Azure Location (LOCATION)
Set the **LOCATION** environment variable to the Azure region (e.g., "uksouth") where resources should be provisioned and tests should run. This allows you to control the geographic location of your test resources.

### *Optional:* Development Environment
To run the E2E test suite against the development environment, set the environment variable **AROHCP_ENV** to `development`. This environment requires port-forwarding to be set up before running the tests.

## General guidance to write E2E test with ginkgo

Keep description of specs and tests informational and comprehensive so that it can be read and understood as a complete sentence, e.g. "Get HCPOpenShiftCluster: it fails to get a nonexistent cluster with a Not Found error by preparing an HCP clusters client (and) by sending a GET request for the nonexistent cluster".

Wondering which labels to use and where to write your test? See the section on [Labels](#labels) and [Files Structure](#files-structure). Optionally refer to this [document](https://docs.google.com/document/d/1v7Xe-BVactmt79Fa5GKxd-r2Q9QuYoOpCIL-m46wp7M/edit?usp=sharing).

[Ginkgo documentation](https://onsi.github.io/ginkgo/)

[Gomega documentation](https://onsi.github.io/gomega/)

### Writing specs

Ginkgo consist of specs nodes structure which can look like:

- Describe -> It
- Describe -> Context -> It
- Describe -> Describe -> ...

Every node consist of arguments:
- description
- labels (optional, but very important)
- function.

Node *It* is the last node and contains the test itself. To describe useful test steps use function **By(message)**. Decorator **defer** is used to call functions after test finish (cleanup). To skip a test use function **Skip(message)** with appropriate message.

In higher level nodes, **BeforeEach** and **AfterEach** functions can be used to run the same code before and after every test.

### Labels
Labels are located in file *test/util/labels/labels.go*. 

Test case environments labels:
- RequireNothing: This test case creates its own cluster (per-test cluster)
- RequireHappyPath: This test case expects populated e2esetup models (per-run cluster)

Importance labels include four levels:
- Critical: blockers for rollout
- High: significant problems affecting a feature
- Medium: less frequent scenarios
- Low: very specific scenarios or enhancements to user experience

Labels based on use cases:
- Core-Infra-Service: use for gating a rollout of ARO-HCP components
- Create-Cluster: applied to test cases related to cluster creation
- Setup-Validation/Teardown-validation: used for validation test cases run before and after tests

Positivity labels:
- Positive/Negative: indicates positive/negative test scenarios

### Files structure
Test code is organized by grouping test cases into specs within files.

Basic test cases for HTTP methods of clusters and nodepools are separated into individual files, like 'cluster_get_test', 'nodepool_create_test' or 'nodepool_update_test'.

Features requiring higher visibility or a large number of test cases have their own dedicated file, e.g. 'cluster_upgrade_test' or 'nodepool_upgrade_test'.

Validation test cases go into the 'validation_test' file.

Admin test cases have files with the prefix 'adminapi' followed by the name of specific group of actions or tool, such as 'adminapi_breakglass_kubeconfig_test'.

### Assertions

The GOMEGA module is used for asserting values. The following example shows the recommended notation for making assertions.

Example:
**Expect(variable).To/ToNot(BeNil(), BeEmpty(), BeTrue(), BeNumerically, ContainString ...)**
