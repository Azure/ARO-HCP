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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// armSystemDataPolicy adds ARM system data headers for direct RP requests.
// This is used in development environments where requests go directly to the
// RP frontend without going through ARM.
type armSystemDataPolicy struct{}

func (p *armSystemDataPolicy) Do(req *policy.Request) (*http.Response, error) {
	frontendURL, err := url.Parse(frontendAddress())
	if err != nil {
		return nil, fmt.Errorf("failed to parse frontend address: %w", err)
	}

	if req.Raw().URL.Host == frontendURL.Host {
		systemData := fmt.Sprintf(`{"createdBy": "e2e-test", "createdByType": "Application", "createdAt": "%s"}`, time.Now().UTC().Format(time.RFC3339))
		req.Raw().Header.Set("X-Ms-Arm-Resource-System-Data", systemData)
		req.Raw().Header.Set("X-Ms-Identity-Url", "https://dummyhost.identity.azure.net")
	}
	return req.Next()
}

// armResourceGroupValidationPolicy simulates ARM's resource group validation
// for development environments where requests go directly to the RP frontend.
// In production, ARM validates that the target resource group exists before
// routing requests to the RP. This policy replicates that behavior so that
// AroRpApiCompatible tests produce consistent results across environments.
type armResourceGroupValidationPolicy struct {
	cred azcore.TokenCredential
}

func (p *armResourceGroupValidationPolicy) Do(req *policy.Request) (*http.Response, error) {
	frontendURL, err := url.Parse(frontendAddress())
	if err != nil {
		return nil, fmt.Errorf("failed to parse frontend address: %w", err)
	}

	if req.Raw().URL.Host != frontendURL.Host {
		return req.Next()
	}

	subID, rgName := parseResourceGroupFromPath(req.Raw().URL.Path)
	if subID == "" || rgName == "" {
		return req.Next()
	}

	client, err := armresources.NewResourceGroupsClient(subID, p.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource group client: %w", err)
	}

	_, err = client.Get(req.Raw().Context(), rgName, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.ErrorCode == arm.CloudErrorCodeResourceGroupNotFound {
			cloudErr := arm.NewCloudError(
				http.StatusNotFound,
				arm.CloudErrorCodeResourceGroupNotFound,
				"",
				"Resource group '%s' could not be found.",
				rgName,
			)
			body, _ := json.Marshal(cloudErr)
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(string(body))),
				Request:    req.Raw(),
			}, nil
		}
		return nil, err
	}

	return req.Next()
}

// parseResourceGroupFromPath extracts the subscription ID and resource group
// name from a URL path like /subscriptions/{subId}/resourceGroups/{rgName}/...
func parseResourceGroupFromPath(path string) (subscriptionID, resourceGroupName string) {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.EqualFold(part, "subscriptions") && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if strings.EqualFold(part, "resourcegroups") && i+1 < len(parts) {
			resourceGroupName = parts[i+1]
		}
	}
	return
}

// correlationRequestIDPolicy generates a UUIDv4 correlation ID per request
// to the RP frontend, setting the X-Ms-Correlation-Request-Id header. When
// requests go through ARM, ARM generates this header; in development
// environments where e2e tests talk directly to the RP, we need to set it
// ourselves. The header is only set when the request targets the RP frontend
// and no correlation ID is already present.
type correlationRequestIDPolicy struct{}

func (p *correlationRequestIDPolicy) Do(req *policy.Request) (*http.Response, error) {
	frontendURL, err := url.Parse(frontendAddress())
	if err != nil {
		return nil, fmt.Errorf("failed to parse frontend address: %w", err)
	}

	if req.Raw().URL.Host == frontendURL.Host && req.Raw().Header.Get(arm.HeaderNameCorrelationRequestID) == "" {
		req.Raw().Header.Set(arm.HeaderNameCorrelationRequestID, uuid.New().String())
	}
	return req.Next()
}

// lroPollerRetryDeploymentNotFoundPolicy is a pipeline policy that retries transient
// 404 DeploymentNotFound errors during ARM deployment LRO polling.
//
// This addresses Azure Resource Manager's eventual consistency behavior where
// the operationStatuses endpoint may return 404 immediately after a deployment
// is created, even though the deployment will eventually succeed.
//
// The policy only activates for GET requests to URLs matching:
//
//	/providers/Microsoft.Resources/deployments/{name}/operationStatuses/{id}
//
// All other requests pass through unmodified.
type lroPollerRetryDeploymentNotFoundPolicy struct {
	backoff        wait.Backoff
	maxRetryWindow time.Duration
}

func NewLROPollerRetryDeploymentNotFoundPolicy() *lroPollerRetryDeploymentNotFoundPolicy {
	return &lroPollerRetryDeploymentNotFoundPolicy{
		backoff: wait.Backoff{
			Duration: 2 * time.Second,
			Factor:   2,
			Jitter:   0.5,
			Steps:    5,
			Cap:      10 * time.Second,
		},
		maxRetryWindow: 90 * time.Second,
	}
}

func (p *lroPollerRetryDeploymentNotFoundPolicy) Do(req *policy.Request) (*http.Response, error) {
	if !strings.EqualFold(req.Raw().Method, http.MethodGet) {
		return req.Next()
	}
	path := req.Raw().URL.Path
	if !strings.Contains(path, "/providers/Microsoft.Resources/deployments/") || !strings.Contains(path, "/operationStatuses/") {
		return req.Next()
	}

	start := time.Now()
	backoff := p.backoff

	for attempt := 0; ; attempt++ {
		retryReq := req.Clone(req.Raw().Context())
		if err := retryReq.RewindBody(); err != nil {
			return nil, err
		}

		resp, err := retryReq.Next()
		if err == nil {
			return resp, nil
		}

		var respErr *azcore.ResponseError
		if !errors.As(err, &respErr) ||
			respErr.StatusCode != http.StatusNotFound ||
			!strings.EqualFold(respErr.ErrorCode, "DeploymentNotFound") {
			return resp, err
		}

		if resp != nil {
			if closeErr := resp.Body.Close(); closeErr != nil {
				ginkgo.GinkgoLogr.Error(closeErr, "failed to close response body")
			}
		}

		if attempt >= backoff.Steps || time.Since(start) >= p.maxRetryWindow {
			return resp, fmt.Errorf("max retries or max retry window reached: %w", err)
		}

		sleep := backoff.Step()

		ginkgo.GinkgoLogr.Info("transient 404 DeploymentNotFound on operationStatuses",
			"attempt", attempt+1,
			"sleep", sleep.String(),
			"url", req.Raw().URL.String())

		select {
		case <-time.After(sleep):
		case <-req.Raw().Context().Done():
			return nil, req.Raw().Context().Err()
		}
	}
}

// throttleRetryPolicy is an outer PerCallPolicy that retries 429 (Too Many
// Requests) responses with conservative exponential backoff.
//
// 429 is excluded from the SDK's inner retry StatusCodes so that throttle
// responses bubble up here immediately (one request, no burst). This policy
// applies exponential backoff floored by the server's Retry-After value,
// ensuring we never retry faster than ARM asks and that delays grow across
// successive attempts.
type throttleRetryPolicy struct {
	backoff        wait.Backoff
	maxRetryWindow time.Duration
}

func NewThrottleRetryPolicy() *throttleRetryPolicy {
	return &throttleRetryPolicy{
		backoff: wait.Backoff{
			Duration: 4 * time.Second,
			Factor:   2,
			Jitter:   0.5,
			Steps:    6,
			Cap:      5 * time.Minute,
		},
		maxRetryWindow: 2 * time.Minute,
	}
}

func (p *throttleRetryPolicy) Do(req *policy.Request) (*http.Response, error) {
	start := time.Now()
	backoff := p.backoff

	for attempt := 0; ; attempt++ {
		retryReq := req.Clone(req.Raw().Context())
		if err := retryReq.RewindBody(); err != nil {
			return nil, err
		}

		resp, err := retryReq.Next()

		if err != nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
			return resp, err
		}

		if attempt >= backoff.Steps || time.Since(start) >= p.maxRetryWindow {
			return resp, err
		}

		retryAfter := parseRetryAfter(resp)
		delay := max(backoff.Step(), retryAfter)
		runtime.Drain(resp)

		ginkgo.GinkgoLogr.Info("429 throttled, backing off",
			"attempt", attempt+1,
			"delay", delay.String(),
			"retryAfter", retryAfter.String(),
			"url", req.Raw().URL.String())

		select {
		case <-time.After(delay):
		case <-req.Raw().Context().Done():
			return nil, req.Raw().Context().Err()
		}
	}
}

// parseRetryAfter extracts the delay from Retry-After-Ms, x-ms-retry-after-ms,
// or Retry-After (seconds / HTTP-date) headers, in priority order.
func parseRetryAfter(resp *http.Response) time.Duration {
	if d := parseRetryAfterMS(resp, "Retry-After-Ms"); d > 0 {
		return d
	}
	if d := parseRetryAfterMS(resp, "X-Ms-Retry-After-Ms"); d > 0 {
		return d
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if seconds, err := strconv.ParseInt(ra, 10, 64); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		if t, err := time.Parse(time.RFC1123, ra); err == nil {
			return time.Until(t)
		}
	}
	return 0
}

func parseRetryAfterMS(resp *http.Response, header string) time.Duration {
	if v := resp.Header.Get(header); v != "" {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return 0
}

// sanitizeAuthHeaderPolicy is a pipeline policy that redacts the Authorization
// header in the *http.Request stored on the *http.Response after each call.
//
// The Azure SDK attaches the original request (including credentials) to every
// response via resp.Request. When an API call fails, the resulting
// *azcore.ResponseError embeds that response. If the error is later formatted
// by a deep struct dumper (e.g. Gomega's failure output), the bearer token in
// the Authorization header is printed in full to stdout/CI logs.
//
// By redacting the header from the stored request, we prevent token leakage
// regardless of how the error is subsequently logged or displayed.
type sanitizeAuthHeaderPolicy struct{}

func (p *sanitizeAuthHeaderPolicy) Do(req *policy.Request) (*http.Response, error) {
	resp, err := req.Next()
	if resp != nil && resp.Request != nil {
		resp.Request.Header["Authorization"] = []string{"redacted"}
	}
	return resp, err
}
