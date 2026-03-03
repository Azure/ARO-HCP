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
	"crypto/tls"
	"fmt"
	"hash/fnv"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onsi/ginkgo/v2/types"
	"golang.org/x/net/http2"

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
	pullSecretPath           string
	frontendAddress          string
	adminAPIAddress          string
	skipCertVerification     bool
	isDevelopmentEnvironment bool
	skipCleanup              bool
	pooledIdentities         bool
	compressTimingMetadata   bool

	contextLock       sync.RWMutex
	subscriptionID    string
	azureCredentials  azcore.TokenCredential
	identityPoolState *leasedIdentityPoolState
	defaultTransport  *http.Transport
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
			sharedDir:                SharedDir(),
			subscriptionName:         subscriptionName(),
			tenantID:                 tenantID(),
			testUserClientID:         testUserClientID(),
			location:                 location(),
			pullSecretPath:           pullSecretPath(),
			frontendAddress:          frontendAddress(),
			adminAPIAddress:          adminAPIAddress(),
			skipCertVerification:     skipCertVerification(),
			isDevelopmentEnvironment: IsDevelopmentEnvironment(),
			skipCleanup:              skipCleanup(),
			pooledIdentities:         pooledIdentities(),
			compressTimingMetadata:   compressTimingMetadata(),
			defaultTransport:         defaultHTTPTransport(),
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

func (tc *perBinaryInvocationTestContext) getClientFactoryOptions() *azcorearm.ClientOptions {
	if tc.isDevelopmentEnvironment {
		return &azcorearm.ClientOptions{
			ClientOptions: azcore.ClientOptions{
				Transport: &proxiedConnectionTransporter{
					delegate: tc.defaultTransport,
				},
				PerCallPolicies: []policy.Policy{
					NewLROPollerRetryDeploymentNotFoundPolicy(),
					&sanitizeAuthHeaderPolicy{},
				},
			},
		}
	}
	return &azcorearm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			PerCallPolicies: []policy.Policy{
				NewLROPollerRetryDeploymentNotFoundPolicy(),
				&sanitizeAuthHeaderPolicy{},
			},
		},
	}
}

func (tc *perBinaryInvocationTestContext) getHCPClientFactoryOptions() *azcorearm.ClientOptions {
	if tc.isDevelopmentEnvironment {
		transport := tc.defaultTransport
		if tc.skipCertVerification {
			transport.TLSClientConfig.InsecureSkipVerify = true
		}
		return &azcorearm.ClientOptions{
			ClientOptions: azcore.ClientOptions{
				Cloud: cloud.Configuration{
					ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
					Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
						cloud.ResourceManager: {
							Audience: "https://management.core.windows.net/",
							Endpoint: tc.frontendAddress,
						},
					},
				},
				Transport: &proxiedConnectionTransporter{
					delegate: transport,
				},
				InsecureAllowCredentialWithHTTP: true,
				PerCallPolicies: []policy.Policy{
					&armSystemDataPolicy{},
					&sanitizeAuthHeaderPolicy{},
				},
			},
		}
	}
	return &azcorearm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			PerCallPolicies: []policy.Policy{
				&sanitizeAuthHeaderPolicy{},
			},
		},
	}
}

// default transport taken judiciously from azcore library to mimick their behavior when no transporter is provided
func defaultHTTPTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	defaultTransport := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion:    tls.VersionTLS12,
			Renegotiation: tls.RenegotiateFreelyAsClient,
		},
	}
	// TODO: evaluate removing this once https://github.com/golang/go/issues/59690 has been fixed
	if http2Transport, err := http2.ConfigureTransports(defaultTransport); err == nil {
		// if the connection has been idle for 10 seconds, send a ping frame for a health check
		http2Transport.ReadIdleTimeout = 10 * time.Second
		// if there's no response to the ping within the timeout, the connection will be closed
		http2Transport.PingTimeout = 5 * time.Second
	}
	return defaultTransport
}

// proxiedConnectionTransporter retries connections done across the proxy path to a local RP,
// in order to paper over transient errors in the proxied connection
type proxiedConnectionTransporter struct {
	delegate *http.Transport
}

func (t *proxiedConnectionTransporter) Do(req *http.Request) (*http.Response, error) {
	return t.delegate.RoundTrip(req)
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

func (tc *perBinaryInvocationTestContext) getLeasedIdentityPoolState() (*leasedIdentityPoolState, error) {
	tc.contextLock.RLock()
	if tc.identityPoolState != nil {
		defer tc.contextLock.RUnlock()
		return tc.identityPoolState, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	if tc.identityPoolState != nil {
		return tc.identityPoolState, nil
	}

	state, err := newLeasedIdentityPoolState(msiPoolStateFilePath())
	if err != nil {
		return nil, fmt.Errorf("failed to get managed identities pool state: %w", err)
	}
	tc.identityPoolState = state
	return tc.identityPoolState, nil
}

func msiPoolStateFilePath() string {
	return filepath.Join(artifactDir(), "identities-pool-state.yaml")
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

func compressTimingMetadata() bool {
	ret, _ := strconv.ParseBool(os.Getenv("COMPRESS_TIMING_METADATA"))
	return ret
}

// SharedDir is SHARED_DIR.  It is a spot to store *files only* that can be shared between ci-operator steps.
// We can use this for anything, but currently we have a backup cleanup and collection scripts that use files
// here to cleanup and debug testing resources.
func SharedDir() string {
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

// pullSecretPath returns the value of ARO_HCP_QE_PULL_SECRET_PATH environment variable
// If not set, defaults to /var/run/aro-hcp-qe-pull-secret
func pullSecretPath() string {
	path := os.Getenv("ARO_HCP_QE_PULL_SECRET_PATH")
	if path == "" {
		return "/var/run/aro-hcp-qe-pull-secret"
	}
	return path
}

// frontendAddress returns the value of FRONTEND_ADDRESS environment variable
func frontendAddress() string {
	address := os.Getenv("FRONTEND_ADDRESS")
	if address == "" {
		return "http://localhost:8443"
	}
	return address
}

// adminAPIAddress returns the value of ADMIN_API_ADDRESS environment variable
func adminAPIAddress() string {
	address := os.Getenv("ADMIN_API_ADDRESS")
	if address == "" {
		return "http://localhost:8444"
	}
	return address
}

// skipCertVerification returns the value of SKIP_CERT_VERIFICATION environment variable
func skipCertVerification() bool {
	b, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("SKIP_CERT_VERIFICATION")))
	if err != nil {
		return false
	}
	return b
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
