// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package framework

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onsi/ginkgo/v2/types"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

type perBinaryInvocationTestContext struct {
	artifactDir              string
	sharedDir                string
	subscriptionName         string
	tenantID                 string
	testUserClientID         string
	location                 string
	isDevelopmentEnvironment bool
	skipCleanup              bool
	pooledIdentities         bool
	leasedIdentityContainers []string

	contextLock      sync.RWMutex
	subscriptionID   string
	azureCredentials azcore.TokenCredential
}

type CleanupFunc func(ctx context.Context) error

var (
	invocationContextInstance *perBinaryInvocationTestContext
	initializeOnce            sync.Once
)

const (
	StandardPollInterval            = 10 * time.Second
	StandardResourceGroupExpiration = 4 * time.Hour
)

// InvocationContext requires the following env vars
// CUSTOMER_SUBSCRIPTION
// AZURE_TENANT_ID
// AZURE_CLIENT_ID
// AZURE_CLIENT_SECRET
func invocationContext() *perBinaryInvocationTestContext {
	initializeOnce.Do(func() {
		invocationContextInstance = &perBinaryInvocationTestContext{
			artifactDir:              artifactDir(),
			sharedDir:                sharedDir(),
			subscriptionName:         subscriptionName(),
			tenantID:                 tenantID(),
			testUserClientID:         testUserClientID(),
			location:                 location(),
			isDevelopmentEnvironment: IsDevelopmentEnvironment(),
			skipCleanup:              skipCleanup(),
			pooledIdentities:         pooledIdentities(),
			leasedIdentityContainers: leasedIdentityContainers(),
		}
	})
	return invocationContextInstance
}

func (tc *perBinaryInvocationTestContext) getAzureCredentials() (azcore.TokenCredential, error) {
	tc.contextLock.RLock()
	if tc.azureCredentials != nil {
		defer tc.contextLock.RUnlock()
		return tc.azureCredentials, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	if tc.azureCredentials != nil {
		return tc.azureCredentials, nil
	}

	if tc.isDevelopmentEnvironment {
		azureCredentials, err := azidentity.NewAzureCLICredential(nil)
		if err != nil {
			return nil, fmt.Errorf("failed building development environment CLI credential: %w", err)
		}
		tc.azureCredentials = azureCredentials

		return tc.azureCredentials, nil
	}

	// if we find a desire to use the zero-dep e2e testing everywhere, we can extend this credential creation to include
	// other options for non-Azure endpoints.  It's worth remembering that the value-add using the same library isn't in the
	// ten lines of creation, it's in using a common credential library for golang compatibility.
	azureCredentials, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed building environment credential: %w", err)
	}
	tc.azureCredentials = azureCredentials

	return tc.azureCredentials, nil
}

// armSystemDataPolicy adds ARM system data headers for localhost requests
type armSystemDataPolicy struct{}

func (p *armSystemDataPolicy) Do(req *policy.Request) (*http.Response, error) {
	if req.Raw().URL.Host == "localhost:8443" {
		systemData := fmt.Sprintf(`{"createdBy": "e2e-test", "createdByType": "Application", "createdAt": "%s"}`, time.Now().UTC().Format(time.RFC3339))
		req.Raw().Header.Set("X-Ms-Arm-Resource-System-Data", systemData)
		req.Raw().Header.Set("X-Ms-Identity-Url", "https://dummyhost.identity.azure.net")
	}
	return req.Next()
}

func (tc *perBinaryInvocationTestContext) getClientFactoryOptions() *azcorearm.ClientOptions {
	return nil
}

func (tc *perBinaryInvocationTestContext) getHCPClientFactoryOptions() *azcorearm.ClientOptions {
	if tc.isDevelopmentEnvironment {
		return &azcorearm.ClientOptions{
			ClientOptions: azcore.ClientOptions{
				Cloud: cloud.Configuration{
					ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
					Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
						cloud.ResourceManager: {
							Audience: "https://management.core.windows.net/",
							Endpoint: "http://localhost:8443",
						},
					},
				},
				InsecureAllowCredentialWithHTTP: true,
				PerCallPolicies: []policy.Policy{
					&armSystemDataPolicy{},
				},
			},
		}
	}
	return nil
}

func (tc *perBinaryInvocationTestContext) getSubscriptionID(ctx context.Context, subscriptionClient *armsubscriptions.Client) (string, error) {
	tc.contextLock.RLock()
	if len(tc.subscriptionID) > 0 {
		defer tc.contextLock.RUnlock()
		return tc.subscriptionID, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	if len(tc.subscriptionID) > 0 {
		return tc.subscriptionID, nil
	}
	subscriptionID, err := GetSubscriptionID(ctx, subscriptionClient, tc.subscriptionName)
	if err != nil {
		return "", fmt.Errorf("failed to get subscription ID: %w", err)
	}

	tc.subscriptionID = subscriptionID
	return tc.subscriptionID, nil
}

func (tc *perBinaryInvocationTestContext) Location() string {
	return tc.location
}

func (tc *perBinaryInvocationTestContext) UsePooledIdentities() bool {
	return tc.pooledIdentities
}

func (tc *perBinaryInvocationTestContext) LeasedIdentityContainers() []string {
	return tc.leasedIdentityContainers
}

func skipCleanup() bool {
	ret, _ := strconv.ParseBool(os.Getenv("ARO_E2E_SKIP_CLEANUP"))
	return ret
}

// artifactDir returns the value of ARTIFACT_DIR environment variable, which is spot to save info in CI
func artifactDir() string {
	// can't use gomega in this method since it is used outside of It()
	return os.Getenv("ARTIFACT_DIR")
}

func pooledIdentities() bool {
	b, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv(UsePooledIdentitiesEnvvar)))
	return b
}

func leasedIdentityContainers() []string {
	leased := strings.Fields(strings.TrimSpace(os.Getenv(LeasedMSIContainersEnvvar)))
	return leased
}

// sharedDir is SHARED_DIR.  It is a spot to store *files only* that can be shared between ci-operator steps.
// We can use this for anything, but currently we have a backup cleanup and collection scripts that use files
// here to cleanup and debug testing resources.
func sharedDir() string {
	// can't use gomega in this method since it is used outside of It()
	return os.Getenv("SHARED_DIR")
}

// subscriptionName returns the value of CUSTOMER_SUBSCRIPTION environment variable
func subscriptionName() string {
	// can't use gomega in this method since it is used outside of It()
	return os.Getenv("CUSTOMER_SUBSCRIPTION")
}

// location returns the Azure location to use, like "uksouth"
func location() string {
	// can't use gomega in this method since it is used outside of It()
	return os.Getenv("LOCATION")
}

// testUserClientID returns the value of AZURE_CLIENT_ID environment variable
func testUserClientID() string {
	// can't use gomega in this method since it is used outside of It()
	return os.Getenv("AZURE_CLIENT_ID")
}

// tenantID returns the value of AZURE_TENANT_ID environment variable
func tenantID() string {
	// can't use gomega in this method since it is used outside of It()
	return os.Getenv("AZURE_TENANT_ID")
}

// IsDevelopmentEnvironment indicates when this environment is development.  This controls client endpoints and disables security
// when set to development.
func IsDevelopmentEnvironment() bool {
	return strings.ToLower(os.Getenv("AROHCP_ENV")) == "development"
}

// Must is a generic function that takes a value of type T and an error.
// If the error is not nil, it panics with the error.
// Otherwise, it returns the value of type T.
func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// SuffixName returns a name given a base ("deployment-5") and a suffix ("deploy")
// It will first attempt to join them with a dash. If the resulting name is longer
// than a valid pod name, it will truncate the base name and add an 8-character hash
// of the [base]-[suffix] string.
func SuffixName(base, suffix string, maxLen int) string {
	name := fmt.Sprintf("%s-%s", base, suffix)
	if len(name) > maxLen {
		prefix := base[0:min(len(base), maxLen-9)]
		// Calculate hash on initial base-suffix string
		name = fmt.Sprintf("%s-%s", prefix, hash(name))
	}
	return name
}

// min returns the lesser of its 2 inputs
func min(a, b int) int {
	if b < a {
		return b
	}
	return a
}

// hash calculates the hexadecimal representation (8-chars)
// of the hash of the passed in string using the FNV-a algorithm
func hash(s string) string {
	hash := fnv.New32a()
	hash.Write([]byte(s))
	intHash := hash.Sum32()
	result := fmt.Sprintf("%08x", intHash)
	return result
}

// AnnotatedLocation can be used to provide more informative source code
// locations by passing the result as additional parameter to a
// BeforeEach/AfterEach/DeferCleanup/It/etc.
func AnnotatedLocation(annotation string) types.CodeLocation {
	return AnnotatedLocationWithOffset(annotation, 1)
}

// AnnotatedLocationWithOffset skips additional call stack levels. With 0 as offset
// it is identical to [AnnotatedLocation].
func AnnotatedLocationWithOffset(annotation string, offset int) types.CodeLocation {
	codeLocation := types.NewCodeLocation(offset + 1)
	codeLocation.FileName = path.Base(codeLocation.FileName)
	codeLocation = types.NewCustomCodeLocation(annotation + " | " + codeLocation.String())
	return codeLocation
}
