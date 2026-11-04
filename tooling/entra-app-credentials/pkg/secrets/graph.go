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

package secrets

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	graphapplications "github.com/microsoftgraph/msgraph-sdk-go/applications"
	graphmodels "github.com/microsoftgraph/msgraph-sdk-go/models"
	graphoauth2 "github.com/microsoftgraph/msgraph-sdk-go/oauth2permissiongrants"
	graphserviceprincipals "github.com/microsoftgraph/msgraph-sdk-go/serviceprincipals"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/graph"
)

const consentTypeAllPrincipals = "AllPrincipals"

type microsoftGraph struct {
	client        *msgraphsdk.GraphServiceClient
	retryInterval time.Duration
}

// GraphClientOption configures a Microsoft Graph client.
type GraphClientOption func(*microsoftGraph)

// WithRetryInterval sets how long lookups wait between attempts while an object
// is still replicating in Microsoft Graph.
func WithRetryInterval(interval time.Duration) GraphClientOption {
	return func(g *microsoftGraph) {
		g.retryInterval = interval
	}
}

// NewGraphClient creates a Graph client backed by Microsoft's official SDK.
// Lookups retry until the context ends while the object is not yet visible,
// because objects created moments earlier by a deployment replicate lazily.
func NewGraphClient(credential azcore.TokenCredential, options ...GraphClientOption) (GraphClient, error) {
	client, err := msgraphsdk.NewGraphServiceClientWithCredentials(credential, []string{graph.Scope})
	if err != nil {
		return nil, fmt.Errorf("create Microsoft Graph client: %w", err)
	}

	result := &microsoftGraph{client: client, retryInterval: graph.DefaultRetryInterval}
	for _, option := range options {
		option(result)
	}
	return result, nil
}

func (g *microsoftGraph) FindApplication(ctx context.Context, displayName string) (*Application, error) {
	return graph.Retry(ctx, g.retryInterval, func(ctx context.Context) (*Application, error) {
		response, err := g.client.Applications().Get(ctx, &graphapplications.ApplicationsRequestBuilderGetRequestConfiguration{
			QueryParameters: &graphapplications.ApplicationsRequestBuilderGetQueryParameters{
				Filter: ptr.To(fmt.Sprintf("displayName eq '%s'", graph.QuoteFilterValue(displayName))),
				Select: []string{"id", "appId", "requiredResourceAccess"},
				Top:    ptr.To(int32(2)),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("list applications: %w", err)
		}

		applications := response.GetValue()
		switch len(applications) {
		case 0:
			return nil, fmt.Errorf("application %q is %w", displayName, graph.ErrNotVisible)
		case 1:
			return convertApplication(applications[0])
		default:
			return nil, fmt.Errorf("display name %q matched %d applications; expected exactly one", displayName, len(applications))
		}
	})
}

func convertApplication(application graphmodels.Applicationable) (*Application, error) {
	if application.GetId() == nil || application.GetAppId() == nil {
		return nil, fmt.Errorf("application returned by Microsoft Graph has no ID")
	}

	result := &Application{ID: *application.GetId(), AppID: *application.GetAppId()}
	for _, resource := range application.GetRequiredResourceAccess() {
		if resource.GetResourceAppId() == nil {
			continue
		}
		converted := ResourceAccess{ResourceAppID: *resource.GetResourceAppId()}
		for _, access := range resource.GetResourceAccess() {
			if access.GetId() == nil || access.GetTypeEscaped() == nil {
				continue
			}
			converted.Access = append(converted.Access, Access{ID: access.GetId().String(), Type: *access.GetTypeEscaped()})
		}
		result.RequiredResourceAccess = append(result.RequiredResourceAccess, converted)
	}
	return result, nil
}

func (g *microsoftGraph) FindServicePrincipal(ctx context.Context, appID string) (*ServicePrincipal, error) {
	return graph.Retry(ctx, g.retryInterval, func(ctx context.Context) (*ServicePrincipal, error) {
		response, err := g.client.ServicePrincipals().Get(ctx, &graphserviceprincipals.ServicePrincipalsRequestBuilderGetRequestConfiguration{
			QueryParameters: &graphserviceprincipals.ServicePrincipalsRequestBuilderGetQueryParameters{
				Filter: ptr.To(fmt.Sprintf("appId eq '%s'", graph.QuoteFilterValue(appID))),
				Select: []string{"id", "appId", "oauth2PermissionScopes"},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("list service principals: %w", err)
		}

		principals := response.GetValue()
		switch {
		case len(principals) == 0:
			return nil, fmt.Errorf("service principal for appId %q is %w", appID, graph.ErrNotVisible)
		case len(principals) > 1:
			return nil, fmt.Errorf("appId %q matched %d service principals; expected exactly one", appID, len(principals))
		case principals[0].GetId() == nil:
			return nil, fmt.Errorf("service principal for appId %q has no ID", appID)
		}

		result := &ServicePrincipal{ID: *principals[0].GetId(), ScopeValues: map[string]string{}}
		for _, scope := range principals[0].GetOauth2PermissionScopes() {
			if scope.GetId() == nil || scope.GetValue() == nil {
				continue
			}
			result.ScopeValues[scope.GetId().String()] = *scope.GetValue()
		}
		return result, nil
	})
}

func (g *microsoftGraph) TenantID(ctx context.Context) (string, error) {
	response, err := g.client.Organization().Get(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("get organization: %w", err)
	}

	organizations := response.GetValue()
	if len(organizations) != 1 || organizations[0].GetId() == nil {
		return "", fmt.Errorf("organization lookup returned %d organizations; expected exactly one", len(organizations))
	}
	return *organizations[0].GetId(), nil
}

func (g *microsoftGraph) AddPassword(ctx context.Context, applicationID, displayName string, end time.Time) (string, error) {
	credential := graphmodels.NewPasswordCredential()
	credential.SetDisplayName(&displayName)
	credential.SetEndDateTime(&end)
	body := graphapplications.NewItemAddPasswordPostRequestBody()
	body.SetPasswordCredential(credential)

	created, err := g.client.Applications().ByApplicationId(applicationID).AddPassword().Post(ctx, body, nil)
	if err != nil {
		return "", fmt.Errorf("add password: %w", err)
	}
	secret := ptr.Deref(created.GetSecretText(), "")
	if secret == "" {
		return "", fmt.Errorf("add password: Microsoft Graph returned no secret text")
	}
	return secret, nil
}

func (g *microsoftGraph) ListAppRoleAssignments(ctx context.Context, principalID string) ([]AppRoleAssignment, error) {
	builder := g.client.ServicePrincipals().ByServicePrincipalId(principalID).AppRoleAssignments()
	response, err := builder.Get(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list app role assignments: %w", err)
	}
	all, err := graph.CollectPages(ctx, response, func(ctx context.Context, link string) (graphmodels.AppRoleAssignmentCollectionResponseable, error) {
		return builder.WithUrl(link).Get(ctx, nil)
	})
	if err != nil {
		return nil, fmt.Errorf("list app role assignments: %w", err)
	}

	var assignments []AppRoleAssignment
	for _, assignment := range all {
		if assignment.GetResourceId() == nil || assignment.GetAppRoleId() == nil {
			continue
		}
		assignments = append(assignments, AppRoleAssignment{
			ResourceID: assignment.GetResourceId().String(),
			AppRoleID:  assignment.GetAppRoleId().String(),
		})
	}
	return assignments, nil
}

func (g *microsoftGraph) CreateAppRoleAssignment(ctx context.Context, principalID, resourceID, appRoleID string) error {
	principalUUID, err := uuid.Parse(principalID)
	if err != nil {
		return fmt.Errorf("parse principal ID: %w", err)
	}
	resourceUUID, err := uuid.Parse(resourceID)
	if err != nil {
		return fmt.Errorf("parse resource ID: %w", err)
	}
	roleUUID, err := uuid.Parse(appRoleID)
	if err != nil {
		return fmt.Errorf("parse app role ID: %w", err)
	}

	assignment := graphmodels.NewAppRoleAssignment()
	assignment.SetPrincipalId(&principalUUID)
	assignment.SetResourceId(&resourceUUID)
	assignment.SetAppRoleId(&roleUUID)
	_, err = g.client.ServicePrincipals().ByServicePrincipalId(principalID).AppRoleAssignments().Post(ctx, assignment, nil)
	if err != nil {
		return fmt.Errorf("create app role assignment: %w", err)
	}
	return nil
}

func (g *microsoftGraph) FindPermissionGrant(ctx context.Context, clientID, resourceID string) (*PermissionGrant, error) {
	filter := fmt.Sprintf("clientId eq '%s' and resourceId eq '%s' and consentType eq '%s'",
		graph.QuoteFilterValue(clientID), graph.QuoteFilterValue(resourceID), consentTypeAllPrincipals)
	response, err := g.client.Oauth2PermissionGrants().Get(ctx, &graphoauth2.Oauth2PermissionGrantsRequestBuilderGetRequestConfiguration{
		QueryParameters: &graphoauth2.Oauth2PermissionGrantsRequestBuilderGetQueryParameters{Filter: &filter},
	})
	if err != nil {
		return nil, fmt.Errorf("list permission grants: %w", err)
	}

	grants := response.GetValue()
	switch {
	case len(grants) == 0:
		return nil, nil
	case len(grants) > 1:
		return nil, fmt.Errorf("matched %d tenant-wide permission grants; expected at most one", len(grants))
	case grants[0].GetId() == nil:
		return nil, fmt.Errorf("permission grant returned by Microsoft Graph has no ID")
	}
	return &PermissionGrant{ID: *grants[0].GetId(), Scope: ptr.Deref(grants[0].GetScope(), "")}, nil
}

func (g *microsoftGraph) CreatePermissionGrant(ctx context.Context, clientID, resourceID, scope string) error {
	grant := graphmodels.NewOAuth2PermissionGrant()
	grant.SetClientId(&clientID)
	grant.SetResourceId(&resourceID)
	grant.SetConsentType(ptr.To(consentTypeAllPrincipals))
	grant.SetScope(&scope)
	_, err := g.client.Oauth2PermissionGrants().Post(ctx, grant, nil)
	if err != nil {
		return fmt.Errorf("create permission grant: %w", err)
	}
	return nil
}

func (g *microsoftGraph) UpdatePermissionGrant(ctx context.Context, grantID, scope string) error {
	grant := graphmodels.NewOAuth2PermissionGrant()
	grant.SetScope(&scope)
	_, err := g.client.Oauth2PermissionGrants().ByOAuth2PermissionGrantId(grantID).Patch(ctx, grant, nil)
	if err != nil {
		return fmt.Errorf("update permission grant: %w", err)
	}
	return nil
}
