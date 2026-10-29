// Copyright 2026 Microsoft Corporation
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

package client

//go:generate $MOCKGEN -typed -source=policy_exemptions_client.go -destination=mock_policy_exemptions_client.go -package client PolicyExemptionsClient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// PolicyExemptionsClient lists exemptions effective at a resource group, including
// inherited exemptions but excluding exemptions on resources within that group.
// armpolicy v1.0.0 does not provide an exemptions client.
type PolicyExemptionsClient interface {
	NewListForResourceGroupPager(resourceGroupName string) *runtime.Pager[PolicyExemptionListResponse]
}

// PolicyExemption contains the fields needed to evaluate a policy exemption.
type PolicyExemption struct {
	ID         *string                    `json:"id,omitempty"`
	Properties *PolicyExemptionProperties `json:"properties,omitempty"`
}

// PolicyExemptionProperties describes the assignment and restrictions exempted.
// ResourceSelectors is retained as JSON so selectors are not silently discarded.
type PolicyExemptionProperties struct {
	PolicyAssignmentID           *string         `json:"policyAssignmentId,omitempty"`
	ExpiresOn                    *time.Time      `json:"expiresOn,omitempty"`
	PolicyDefinitionReferenceIDs []*string       `json:"policyDefinitionReferenceIds,omitempty"`
	ResourceSelectors            json.RawMessage `json:"resourceSelectors,omitempty"`
	AssignmentScopeValidation    *string         `json:"assignmentScopeValidation,omitempty"`
}

// Scope returns the exemption's parent scope, not the policy assignment's scope.
// The service does not return a separate scope property. Invalid IDs return "".
func (exemption *PolicyExemption) Scope() string {
	if exemption == nil || exemption.ID == nil {
		return ""
	}
	const marker = "/providers/microsoft.authorization/policyexemptions/"
	id := *exemption.ID
	index := strings.LastIndex(strings.ToLower(id), marker)
	if index < 0 || index+len(marker) == len(id) || strings.Contains(id[index+len(marker):], "/") {
		return ""
	}
	if index == 0 {
		return "/"
	}
	return id[:index]
}

// PolicyExemptionListResponse is a single page of policy exemptions.
type PolicyExemptionListResponse struct {
	Value    []*PolicyExemption `json:"value,omitempty"`
	NextLink *string            `json:"nextLink,omitempty"`
}

type policyExemptionsClient struct {
	subscriptionID string
	internal       *azcorearm.Client
}

var _ PolicyExemptionsClient = (*policyExemptionsClient)(nil)

// NewPolicyExemptionsClient creates an ARM pipeline REST adapter. Cloud endpoint,
// authentication, transport, retries and policies come from the SDK options.
func NewPolicyExemptionsClient(subscriptionID string, credential azcore.TokenCredential, options *azcorearm.ClientOptions) (PolicyExemptionsClient, error) {
	if subscriptionID == "" {
		return nil, errors.New("parameter subscriptionID cannot be empty")
	}
	if credential == nil {
		return nil, errors.New("parameter credential cannot be nil")
	}
	internal, err := azcorearm.NewClient("github.com/Azure/ARO-HCP/backend", "v0.0.0", credential, options)
	if err != nil {
		return nil, err
	}
	return &policyExemptionsClient{subscriptionID: subscriptionID, internal: internal}, nil
}

func (client *policyExemptionsClient) NewListForResourceGroupPager(resourceGroupName string) *runtime.Pager[PolicyExemptionListResponse] {
	return runtime.NewPager(runtime.PagingHandler[PolicyExemptionListResponse]{
		More: func(page PolicyExemptionListResponse) bool {
			return page.NextLink != nil && *page.NextLink != ""
		},
		Fetcher: func(ctx context.Context, page *PolicyExemptionListResponse) (PolicyExemptionListResponse, error) {
			nextLink := ""
			if page != nil && page.NextLink != nil {
				nextLink = *page.NextLink
			}
			// Match generated SDK pagers: this helper encodes nextLink queries,
			// uses the ARM pipeline, and checks HasStatusCode / NewResponseError.
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), nextLink, func(ctx context.Context) (*policy.Request, error) {
				return client.listForResourceGroupCreateRequest(ctx, resourceGroupName)
			}, nil)
			if err != nil {
				return PolicyExemptionListResponse{}, err
			}
			var result PolicyExemptionListResponse
			if err := runtime.UnmarshalAsJSON(resp, &result); err != nil {
				return PolicyExemptionListResponse{}, err
			}
			return result, nil
		},
		Tracer: client.internal.Tracer(),
	})
}

func (client *policyExemptionsClient) listForResourceGroupCreateRequest(ctx context.Context, resourceGroupName string) (*policy.Request, error) {
	if resourceGroupName == "" {
		return nil, errors.New("parameter resourceGroupName cannot be empty")
	}
	urlPath := "/subscriptions/" + url.PathEscape(client.subscriptionID) + "/resourceGroups/" + url.PathEscape(resourceGroupName) + "/providers/Microsoft.Authorization/policyExemptions"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	if err != nil {
		return nil, err
	}
	query := req.Raw().URL.Query()
	query.Set("api-version", "2022-07-01-preview")
	query.Set("$filter", "atScope()")
	req.Raw().URL.RawQuery = query.Encode()
	req.Raw().Header.Set("Accept", "application/json")
	return req, nil
}
