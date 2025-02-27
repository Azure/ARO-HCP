# Testing

## E2E test suite

The E2E test suite will work in every environment of the ARO-HCP project. Its main purpose is to ensure specific functionality based on the environment and its usage. You can find more details about the test suite design in [ARO-12804](https://issues.redhat.com/browse/ARO-12804).

- The test suite client connects to the RP frontend in the development environment using port forwarding to localhost.
- In the integration environment, the client will connect to the RP frontend using a public IP (when available) but only within the MSFT corporate network.
- For stage and production environments, the client will connect through ARM once they are set up.

The client expects a subscription to be already registered. To assign the client to a specific subscription, set its ID in the environment variable **SUBSCRIPTION_ID**. If not set, the default all-zero subscription will be used.

Test cases expect resource group name, where cluster resources (vnet, managed identity, ...) are located, to be set in the environment variable **CUSTOMER_RG_NAME**.

### Run E2E tests locally against development environment

1. Login with AZ CLI
2. Port-forward RP running on SC: `kubectl port-forward -n aro-hcp svc/aro-hcp-frontend 8443:8443`
3. Export environment variables LOCAL_DEVELOPMENT, SUBSCRIPTION_ID and CUSTOMER_RG_NAME

```bash
export LOCAL_DEVELOPMENT=true
export SUBSCRIPTION_ID=<subscriptionId>
export CUSTOMER_RG_NAME=<resourceGroupName>
```

4. Run test suite with command

Run all test cases: `ginkgo ./e2e`

Run specific test case: `ginkgo --focus "<regex>" ./e2e`

Run in debug mode: `ginkgo ./e2e --vv`

### Writing E2E test with ginkgo

[Ginkgo documentation](https://onsi.github.io/ginkgo/)

[Gomega documentation](https://onsi.github.io/gomega/)

#### Writing specs

Ginkgo consist of specs nodes structure which can look like:

- Describe -> It
- Describe -> Context -> It
- Describe -> Describe -> ...

Every node consist of arguments: 
- description
- labels (optional, but very important)
- function. 

Node *It* is last node and contains test itself. To describe useful test steps use function **By(message)**. Decorator **defer** is used to call funtions after test finish (cleanup). To skip a test use function **Skip(message)** with appropriate message.

In higher level nodes, **BeforeEach** and **AfterEach** functions can be used to run same code before and after every test.

#### Labels
Create a label with function **Label(name)** in file *util/labels/labels.go*.

To run tests with specified labels use ginkgo with option --label-filter. Example: `ginkgo --label-filter=QUERY`

#### Assertions 

To assert values GOMEGA module is used.

Example:
**Expect(variable).To/ToNot(BeNil(), BeEmpty(), BeTrue(), BeNumerically, ContainString ...)**

