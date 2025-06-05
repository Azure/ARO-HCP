# E2E Testing

The E2E test suite will work in every environment of the ARO-HCP project. Its main purpose is to ensure specific functionality based on the environment and its usage. You can find more details about the test suite design in [ARO-12804](https://issues.redhat.com/browse/ARO-12804).

- The test suite client connects to the RP frontend in the development environment using port forwarding to localhost.
- In the integration environment, the client will connect to the ARM using a public IP but only within the MSFT corporate network.
- For stage and production environments, the client will connect through ARM once they are set up.

The client expects a subscription to be already registered. To assign the client to a specific subscription, set its ID in the environment variable **CUSTOMER_SUBSCRIPTION**. If not set, the default all-zero subscription will be used.

The E2E Test Suite requires a JSON setup file with a deterministic structure. The suite expects the path to this JSON file in the **SETUP_FILEPATH** environment variable. This JSON file is created by the ARO HCP E2E Setup code and contains all the necessary values for testing.

To distinguish E2E test suite from unit tests, initial ginkgo file *e2e_test.go* has a build tag **E2Etests**. The build tag has to be explicitly set when running (or building) the E2E test suite.

## Run E2E tests locally against development environment

1. Login with AZ CLI
2. Port-forward RP running on SC: `kubectl port-forward -n aro-hcp svc/aro-hcp-frontend 8443:8443`
3. Export environment variables LOCAL_DEVELOPMENT, CUSTOMER_SUBSCRIPTION and SETUP_FILEPATH

```bash
export LOCAL_DEVELOPMENT=true
export CUSTOMER_SUBSCRIPTION=<subscriptionId>
export SETUP_FILEPATH=<filepath>
```

4. Run test suite with command

Run all test cases: `ginkgo --tags E2Etests ./`

Run specific test case: `ginkgo --tags E2Etests --focus "<regex>" ./`

Run in debug mode: `ginkgo --tags E2Etests --vv ./`

## Writing E2E test with ginkgo

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

Importance labels include four levels:
- Critical: blockers for rollout
- High: significant problems affecting a feature
- Medium: less frequent scenarios
- Low: very specific scenarios or enhancements to user experience

Labels based on use cases:
- Core-Infra-Service: use for gating a rollout of ARO-HCP componets
- Create-Cluster: applied to test cases related to cluster creation
- Setup-Validation/Teardown-validation: used for validation test cases run before and after tests

Positivity labels:
- Positive/Negative: indicates positive/negative test scenarios

To run tests with specified labels use ginkgo with option --label-filter. Example: `ginkgo --tags E2Etests --label-filter=QUERY`

### Files structure
Test code is organized by grouping test cases into specs within files.

Basic test cases for HTTP methods of clusters and nodepools are seperated into individual files, like 'cluster_get_test', 'nodepool_create_test' or 'nodepool_update_test'.

Features requiring higher visibility or a large number of test cases have their own dedicated file, e.g. 'cluster_upgrade_test' or 'nodepool_upgrade_test'.

Validation test cases go into the 'validation_test' file.

Admin test cases have files with the prefix 'adminapi' followed by the name of specific group of actions or tool, such as 'adminapi_breakglass_kubeconfig_test'.

### Assertions

The GOMEGA module is used for asserting values. The following example shows the recommended notation for making assertions.

Example:
**Expect(variable).To/ToNot(BeNil(), BeEmpty(), BeTrue(), BeNumerically, ContainString ...)**
