# Testing

## Run E2E tests locally

1. Login with AZCLI
2. Port-forward RP running on SC: `kubectl port-forward -n aro-hcp svc/aro-hcp-frontend 8443:8443`
3. Export environment variables LOCAL_DEVELOPMENT and SUBSCRIPTION_ID

```bash
export LOCAL_DEVELOPMENT=true
export SUBSCRIPTION_ID=<subscriptionId>
```

4. Run test suite with commands:

```bash
ginkgo ./e2e
```
Run specific test:
```bash
ginkgo -focus "<regex>" ./e2e
```
Run in debug mode:
```bash
ginkgo ./e2e --vv
```

## Writing E2E test with ginkgo

### Writing specs

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

### Labels
Create a label with function **Label(name)**.

To run tests with specified labels use ginkgo with option --label-filter.

Example:
**ginkgo --label-filter=QUERY**

### Assertions 

To assert values GOMEGA module is used.

Example:
**Expect(variable).To/ToNot(BeNil(), BeEmpty(), BeTrue(), BeNumerically, ContainString ...)**

