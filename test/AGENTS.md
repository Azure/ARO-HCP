# Principles of Good E2E Test Case Design

## Provision HCP cluster

* **Cluster creation:** Cluster creation, which leverages methods from the framework module, offers three main approaches for creating and deploying an HCP cluster: `CreateHCPClusterFromParam`, which handles creation and automatically waits for successful deployment; `BeginCreateHCPCluster`, which initiates the process but requires explicit test logic to wait for provisioning completion; and an alternative using `CreateHCPClusterFromParam` with a 0-second timeout, which executes the creation but immediately bypasses the waiting phase for provisioning to finish.  
* **Cluster Params:** The `NewDefaultClusterParams` method from the framework module should be used to configure the default cluster parameters. Before creating cluster customer resources, the `ClusterName` parameter must be set. Different cluster configurations can be achieved by assigning custom values to the parameters.  
* **Prepare cluster customer resources:** Creating a cluster requires several resources (like an NSG, VNet, subnet, and managed identities). To create these resources and set the cluster's parameters, use the `CreateClusterCustomerResources` method from the framework module.  
* **Nodepool creation:** To create a nodepool, utilize the `CreateNodePoolFromParam` method. Beforehand, the default nodepool parameters should be prepared using the `NewDefaultNodePoolParams` method. Both of these methods are located within the `framework` module. Like cluster parameters, custom configurations can be assigned to the nodepool parameter values.  
* **Timeout of deployment:** To keep the timeout consistent with other test cases, use 45 minutes.

## Resource naming \- Independence and Isolation

* **Self-Contained:** Every test case must be self-contained, ensuring no dependencies on the state or results of other test cases.  
* **Parallel execution:** Tests are executed parallely thus ensuring names are unique across the subscription: customer resource group (handled by method `tc.NewResourceGroup()`), managed resource group, and cluster names within one resource group, if multiple are created. Bicep deployment names must be unique within the resource group.

## HCP SDK client helper

* **HCP SDK:** Currently we are using an unreleased generated ARO HCP Golang SDK. SDK is located under the module test.  
* **HTTPS Requests:** To interact with RP/ARM, use the hcp client.. Use context with timeout to cancel requests which are asynchronous.  
* **Code location:** Such helper functions should be located in the util module `framework` in file `hcp_helper.go`. ([https://github.com/Azure/ARO-HCP/blob/main/test/util/framework/hcp\_helper.go](https://github.com/Azure/ARO-HCP/blob/main/test/util/framework/hcp_helper.go))

## Kubernetes verifiers

* **K8S client-go:** Use this client to communicate with created HCP clusters. Client requires rest Config which is provided by method `GetAdminRESTConfigForHCPCluster` with 10 minutes timeout.  
* **HostedClusterVerifier:** This interface is designed for all verifiers and provides the essential `Name` and `Verify` methods for extension.  
* **Code location:** Verifiers are located in the util module `verifiers`. ([https://github.com/Azure/ARO-HCP/tree/main/test/util/verifiers](https://github.com/Azure/ARO-HCP/tree/main/test/util/verifiers))

## Cleanup of Resources

* **TestContext:** Using [TestContext](https://github.com/Azure/ARO-HCP/blob/main/test/util/framework/per_test_framework.go#L51), to create a resource group, will automatically register it for a cleanup after the test. The cleanup process involves deleting all HCP clusters within the designated resource groups. The resource groups themselves are removed, along with any remaining Azure resources.  
* **Test resources:** Within the test, remove any created resources that are not part of the TestContext resource group or its associated HCP clusters. Ensure all tests start from a known, clean state to avoid flakiness and false positives.

# Best Practices for Writing E2E Test Cases

## Logging

* **Log message structure:** Ensure log messages are direct, including the actual error message if an error occurs.  
* **Preferred Logger:** For logging, use `ginkgo.GinkgoLogr` to generate info or error entries in the output. This is the preferred logger. Alternatively, you can use the `klog` from the `k8s.io` module. To measure the execution time of a specific method, utilize the `RecordTestStep` function as a `defer` call within that method.  
* **Ginkgo Writer:** Use `ginkgo.GinkgoWriter` to create a regular message in output.

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
* **Makefile:** The `Makefile` in the repository's root directory includes an `all-tidy` rule. This rule guarantees the correct implementation of the golang mechanisms and enforces the licence header.

## Azure Location

* When creating a new resource group, use the `tc.Location()` method to retrieve the globally configured location from the environment variable instead of hardcoding Azure location names.

# Tips and Tricks for Effective E2E Testing

* **Randomize strings:** To create unique resource names, use the `rand.String(n int)` function from the `k8s.io/apimachinery/pkg/util/rand` library to generate random strings. A length of 6 characters is typically sufficient.  
* **Descriptive Names:** Use descriptive names for test files, test cases, bicep/arm deployments, and functions.  
* **Test case description:** Maintain descriptions of specifications and tests as informative and comprehensive complete sentences.  
* **Development environment test cases:** Ensure new negative test cases produce the same result in development and higher environments by running them in both. Do not use the label `AroRpApiCompatible` if the test case fails in the development environment.
