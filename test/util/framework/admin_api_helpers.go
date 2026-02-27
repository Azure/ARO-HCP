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
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-cmp/cmp"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
)

const (
	// clientPrincipalNameHeader is the header Geneva Actions sets to identify the calling principal.
	clientPrincipalNameHeader = "X-Ms-Client-Principal-Name"
	// clientAADTypeHeader is the header Geneva Actions sets to identify the calling principal type.
	clientAADTypeHeader = "X-Ms-Client-Principal-Type"

	// adminAPIRequestTimeout is the timeout for individual HTTP requests to the admin API.
	adminAPIRequestTimeout = 30 * time.Second
	// sessionReadyTimeout is the maximum time to wait for a breakglass session to become
	// ready. Session creation involves creating a Session CR, reconciling RBAC bindings,
	// and generating a kubeconfig with a short-lived token.
	sessionReadyTimeout = 1 * time.Minute
	// sessionReadyPollInterval is how frequently to poll the session status endpoint
	// while waiting for readiness.
	sessionReadyPollInterval = 5 * time.Second
)

// PrincipalType represents the type of Azure AD principal.
type PrincipalType string

const (
	PrincipalTypeDSTSUser            PrincipalType = "dstsUser"
	PrincipalTypeAADServicePrincipal PrincipalType = "aadServicePrincipal"
)

type AzureIdentityDetails struct {
	PrincipalName string
	PrincipalType PrincipalType
}

// GetCurrentAzureIdentityDetails extracts the current Azure identity from the
// credentials configured for this test run. Callers should call this once and
// pass the result to CreateSREBreakglassCredentials rather than re-fetching per call.
func (tc *perBinaryInvocationTestContext) GetCurrentAzureIdentityDetails(ctx context.Context) (*AzureIdentityDetails, error) {
	cred, err := tc.getAzureCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get Azure token: %w", err)
	}

	// We use ParseUnverified because Azure AD token signature verification
	// requires fetching JWKS keys from the Azure AD discovery endpoint, which is
	// unnecessary here — we trust the token returned by the Azure SDK's own
	// credential flow. We only need to extract claims for identity details.
	parsed, _, err := jwt.NewParser().ParseUnverified(token.Token, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse JWT token: %w", err)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("unexpected JWT claims type %T", parsed.Claims)
	}

	idType, ok := claims["idtyp"].(string)
	if !ok {
		return nil, fmt.Errorf("idtyp claim missing or not a string in token")
	}
	if idType == "user" {
		upn, ok := claims["upn"].(string)
		if !ok {
			return nil, fmt.Errorf("upn claim missing or not a string for user identity")
		}
		return &AzureIdentityDetails{
			PrincipalName: upn,
			PrincipalType: PrincipalTypeDSTSUser,
		}, nil
	}
	if idType == "app" {
		oid, ok := claims["oid"].(string)
		if !ok {
			return nil, fmt.Errorf("oid claim missing or not a string for app identity")
		}
		return &AzureIdentityDetails{
			PrincipalName: oid,
			PrincipalType: PrincipalTypeAADServicePrincipal,
		}, nil
	}
	return nil, fmt.Errorf("unknown identity type %q in token claims", idType)
}

// clientPrincipalTransport simulates the Geneva Actions gateway by injecting
// client principal headers that identify the authenticated user or service
// principal. In production, these headers are set by Geneva Actions based on
// Azure AD authentication and cannot be set by clients directly. This transport
// is only for testing the admin API's consumption of these headers.
type clientPrincipalTransport struct {
	base            http.RoundTripper
	identityDetails *AzureIdentityDetails
}

func (t *clientPrincipalTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set(clientPrincipalNameHeader, t.identityDetails.PrincipalName)
	req.Header.Set(clientAADTypeHeader, string(t.identityDetails.PrincipalType))
	return t.base.RoundTrip(req)
}

func createSREBreakglassSession(ctx context.Context, httpClient *http.Client, breakglassEndpoint string, accessLevel string, ttl time.Duration) (string, error) {
	requestBody, err := json.Marshal(map[string]string{
		"group": accessLevel,
		"ttl":   ttl.String(),
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, breakglassEndpoint, bytes.NewReader(requestBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return "", fmt.Errorf("expected status 202 Accepted, got %d (failed to read body: %w)", resp.StatusCode, readErr)
		}
		return "", fmt.Errorf("expected status 202 Accepted, got %d: %s", resp.StatusCode, string(body))
	}

	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("no Location header in response")
	}
	return location, nil
}

// pollSessionStatus performs a single poll of the session status endpoint.
// It returns the response body, status code, and Expires header value.
func pollSessionStatus(ctx context.Context, httpClient *http.Client, kubeconfigEndpoint string) (body []byte, statusCode int, expiresAt string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kubeconfigEndpoint, nil)
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to send request: %w", err)
	}

	body, err = io.ReadAll(resp.Body)
	expiresAt = resp.Header.Get("Expires")
	resp.Body.Close()
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to read response body: %w", err)
	}

	return body, resp.StatusCode, expiresAt, nil
}

// parseKubeconfigResponse parses a successful session response into a
// kubeconfig and its expiration time.
func parseKubeconfigResponse(body []byte, expiresAt string) (*clientcmdapi.Config, time.Time, error) {
	config, err := clientcmd.Load(body)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	expiresAtTime, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to parse expiration header: %w", err)
	}
	return config, expiresAtTime, nil
}

func waitForSREBreakglassSessionReady(ctx context.Context, httpClient *http.Client, kubeconfigEndpoint string) (*clientcmdapi.Config, time.Time, error) {
	timeout := time.NewTimer(sessionReadyTimeout)
	defer timeout.Stop()

	ticker := time.NewTicker(sessionReadyPollInterval)
	defer ticker.Stop()

	var previousStatus map[string]any
	for {
		body, statusCode, expiresAt, err := pollSessionStatus(ctx, httpClient, kubeconfigEndpoint)
		if err != nil {
			return nil, time.Time{}, err
		}

		switch statusCode {
		case http.StatusAccepted:
			// Session not ready yet - log status changes (skip the initial observation)
			statusBody := map[string]any{}
			if json.Unmarshal(body, &statusBody) == nil {
				if previousStatus != nil {
					diff := cmp.Diff(previousStatus, statusBody)
					if diff != "" {
						fmt.Fprintf(GinkgoWriter, "Session status changed: %s\n", diff)
					}
				}
				previousStatus = statusBody
			}
		case http.StatusOK:
			return parseKubeconfigResponse(body, expiresAt)
		default:
			return nil, time.Time{}, fmt.Errorf("unexpected status %d from session endpoint: %s", statusCode, string(body))
		}

		select {
		case <-ctx.Done():
			return nil, time.Time{}, ctx.Err()
		case <-timeout.C:
			previousStatusJSON, marshalErr := json.Marshal(previousStatus)
			if marshalErr != nil {
				return nil, time.Time{}, fmt.Errorf("timeout waiting for session to become ready (failed to marshal last status: %w)", marshalErr)
			}
			return nil, time.Time{}, fmt.Errorf("timeout waiting for session to become ready (last status: %s)", string(previousStatusJSON))
		case <-ticker.C:
		}
	}
}

// GetCurrentAzureIdentityDetails returns the identity details for the current
// Azure credentials configured on the per-invocation test context.
func (tc *perItOrDescribeTestContext) GetCurrentAzureIdentityDetails(ctx context.Context) (*AzureIdentityDetails, error) {
	return tc.perBinaryInvocationTestContext.GetCurrentAzureIdentityDetails(ctx)
}

func (tc *perItOrDescribeTestContext) CreateSREBreakglassCredentials(ctx context.Context, resourceID string, ttl time.Duration, accessLevel string, identityDetails *AzureIdentityDetails) (*rest.Config, time.Time, error) {
	tlsConfig := &tls.Config{}
	if IsDevelopmentEnvironment() {
		tlsConfig.InsecureSkipVerify = true
	}
	httpClient := &http.Client{
		Transport: &clientPrincipalTransport{
			base: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
			identityDetails: identityDetails,
		},
		Timeout: adminAPIRequestTimeout,
	}

	adminAPIEndpoint := tc.perBinaryInvocationTestContext.adminAPIAddress

	breakglassEndpoint := fmt.Sprintf("%s/admin/v1/hcp%s/breakglass",
		adminAPIEndpoint,
		resourceID,
	)
	By(fmt.Sprintf("reaching out to the admin API to create a breakglass session for %s with %s permissions: %s", resourceID, accessLevel, breakglassEndpoint))
	kubeconfigReqPath, err := createSREBreakglassSession(ctx, httpClient, breakglassEndpoint, accessLevel, ttl)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to create SRE breakglass session: %w", err)
	}

	By(fmt.Sprintf("waiting for SRE breakglass session to be ready at %s", kubeconfigReqPath))
	kubeconfigEndpoint := fmt.Sprintf("%s%s",
		adminAPIEndpoint,
		kubeconfigReqPath,
	)
	kubeconfig, expiresAt, err := waitForSREBreakglassSessionReady(ctx, httpClient, kubeconfigEndpoint)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to get ready session kubeconfig from %s: %w", kubeconfigReqPath, err)
	}

	restConfig, err := clientcmd.NewDefaultClientConfig(*kubeconfig, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to create rest config from kubeconfig: %w", err)
	}
	// Skip TLS verification for development environments with self-signed certificates
	if IsDevelopmentEnvironment() {
		restConfig.Insecure = true
	}
	return restConfig, expiresAt, nil
}

// GetFirstVMFromManagedResourceGroup retrieves the name of the first VM found in the managed resource group.
// Returns an error if no VMs are found or if the Azure API calls fail.
func (tc *perItOrDescribeTestContext) GetFirstVMFromManagedResourceGroup(ctx context.Context, managedResourceGroupName string) (string, error) {
	cred, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	subscriptionID, err := tc.SubscriptionID(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get subscription ID: %w", err)
	}

	computeClient, err := armcompute.NewVirtualMachinesClient(subscriptionID, cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create compute client: %w", err)
	}

	pager := computeClient.NewListPager(managedResourceGroupName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to list VMs: %w", err)
		}
		if len(page.Value) > 0 {
			if page.Value[0].Name != nil {
				return *page.Value[0].Name, nil
			}
		}
	}

	return "", fmt.Errorf("no VMs found in managed resource group %s", managedResourceGroupName)
}

// GetSerialConsoleLogs retrieves serial console logs for a VM in an HCP cluster's managed resource group
func (tc *perItOrDescribeTestContext) GetSerialConsoleLogs(ctx context.Context, resourceID string, vmName string, identityDetails *AzureIdentityDetails) (string, error) {
	tlsConfig := &tls.Config{}
	if IsDevelopmentEnvironment() {
		tlsConfig.InsecureSkipVerify = true
	}
	httpClient := &http.Client{
		Transport: &clientPrincipalTransport{
			base: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
			identityDetails: identityDetails,
		},
		Timeout: adminAPIRequestTimeout,
	}

	adminAPIEndpoint := tc.perBinaryInvocationTestContext.adminAPIAddress

	serialConsoleEndpoint := fmt.Sprintf("%s/admin/v1/hcp%s/serialconsole?vmName=%s",
		adminAPIEndpoint,
		resourceID,
		vmName,
	)

	By(fmt.Sprintf("reaching out to the admin API to retrieve serial console logs for VM %s: %s", vmName, serialConsoleEndpoint))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serialConsoleEndpoint, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return "", fmt.Errorf("expected status 200 OK, got %d (failed to read body: %w)", resp.StatusCode, readErr)
		}
		return "", fmt.Errorf("expected status 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(body), nil
}
