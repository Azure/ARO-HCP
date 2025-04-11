# E2E Testing

The E2E test suite will work in every environment of the ARO-HCP project. Its main purpose is to ensure specific functionality based on the environment and its usage. You can find more details about the test suite design in [ARO-12804](https://issues.redhat.com/browse/ARO-12804).

- The test suite client connects to the RP frontend in the development environment using port forwarding to localhost.
- In the integration environment, the client will connect to the ARM using a public IP but only within the MSFT corporate network.
- For stage and production environments, the client will connect through ARM once they are set up.

The client expects a subscription to be already registered. To assign the client to a specific subscription, set its ID in the environment variable **CUSTOMER_SUBSCRIPTION**. If not set, the default all-zero subscription will be used.

Test cases expect resource group name, where cluster resources (vnet, managed identity, ...) are located, to be set in the environment variable **CUSTOMER_RG_NAME**.

To distinguish E2E test suite from unit tests, initial ginkgo file *e2e_test.go* has a build tag **E2Etests**. The build tag has to be explicitly set when running (or building) the E2E test suite.

## Run E2E tests locally against development environment

1. Login with AZ CLI
2. Port-forward RP running on SC: `kubectl port-forward -n aro-hcp svc/aro-hcp-frontend 8443:8443`
3. Export environment variables LOCAL_DEVELOPMENT, CUSTOMER_SUBSCRIPTION and CUSTOMER_RG_NAME

```bash
export LOCAL_DEVELOPMENT=true
export CUSTOMER_SUBSCRIPTION=<subscriptionId>
export CUSTOMER_RG_NAME=<resourceGroupName>
```

4. Run test suite with command

Run all test cases: `ginkgo --tags E2Etests ./`

Run specific test case: `ginkgo --tags E2Etests --focus "<regex>" ./`

Run in debug mode: `ginkgo --tags E2Etests --vv ./`

## Writing E2E test with ginkgo

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
Create a label with function **Label(name)** in file *util/labels/labels.go*.

To run tests with specified labels use ginkgo with option --label-filter. Example: `ginkgo --label-filter=QUERY`

### Assertions

The GOMEGA module is used for asserting values. The following example shows the recommended notation for making assertions.

Example:
**Expect(variable).To/ToNot(BeNil(), BeEmpty(), BeTrue(), BeNumerically, ContainString ...)**
