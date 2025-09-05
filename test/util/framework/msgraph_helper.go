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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	abstractions "github.com/microsoft/kiota-abstractions-go"
	kiotahttp "github.com/microsoft/kiota-http-go"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk"
	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/applications"
	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/models"
)

const appRegistrationPrefix = "aro-hcp-e2e-"

// Application represents a Microsoft Entra application
type Application struct {
	ID          string `json:"id"`
	AppID       string `json:"appId"`
	DisplayName string `json:"displayName"`
}

// PasswordCredential represents a password credential for an application
type PasswordCredential struct {
	SecretText string    `json:"secretText"`
	KeyID      string    `json:"keyId"`
	StartTime  time.Time `json:"startTime"`
	EndTime    time.Time `json:"endTime"`
}

// CreateApplication creates a new Microsoft Entra application
func (c *GraphClient) CreateApplication(ctx context.Context, displayName string, redirectURIs []string) (*Application, error) {
	app := models.NewApplication()
	app.SetDisplayName(&displayName)

	// Create web application with redirect URIs
	webApp := models.NewWebApplication()
	webApp.SetRedirectUris(redirectURIs)
	app.SetWeb(webApp)

	createdApp, err := c.graphClient.Applications().Post(ctx, app, nil)
	if err != nil {
		return nil, fmt.Errorf("create application: %w", err)
	}

	return &Application{
		ID:          *createdApp.GetId(),
		AppID:       *createdApp.GetAppId(),
		DisplayName: *createdApp.GetDisplayName(),
	}, nil
}

// AddPassword adds a password credential to an application
func (c *GraphClient) AddPassword(ctx context.Context, appID, displayName string, startTime, endTime time.Time) (*PasswordCredential, error) {
	// Create password credential
	passwordCred := models.NewPasswordCredential()
	passwordCred.SetDisplayName(&displayName)
	passwordCred.SetStartDateTime(&startTime)
	passwordCred.SetEndDateTime(&endTime)

	// Create request body for addPassword
	reqBody := applications.NewItemAddPasswordPostRequestBody()
	reqBody.SetPasswordCredential(passwordCred)

	// Add password to application
	result, err := c.graphClient.Applications().ByApplicationId(appID).AddPassword().Post(ctx, reqBody, nil)
	if err != nil {
		return nil, fmt.Errorf("add password: %w", err)
	}

	return &PasswordCredential{
		SecretText: *result.GetSecretText(),
		KeyID:      result.GetKeyId().String(),
		StartTime:  *result.GetStartDateTime(),
		EndTime:    *result.GetEndDateTime(),
	}, nil
}

// UpdateApplicationRedirectUris updates the redirect URIs for an application
func (c *GraphClient) UpdateApplicationRedirectUris(ctx context.Context, appID string, redirectURIs []string) error {
	// Get existing application
	app, err := c.graphClient.Applications().ByApplicationId(appID).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("get application: %w", err)
	}

	// Update web application with new redirect URIs
	webApp := app.GetWeb()
	if webApp == nil {
		webApp = models.NewWebApplication()
	}
	webApp.SetRedirectUris(redirectURIs)
	app.SetWeb(webApp)

	// Patch the application
	_, err = c.graphClient.Applications().ByApplicationId(appID).Patch(ctx, app, nil)
	if err != nil {
		return fmt.Errorf("patch application: %w", err)
	}

	return nil
}

// DeleteApplication deletes an application
func (c *GraphClient) DeleteApplication(ctx context.Context, appID string) error {
	err := c.graphClient.Applications().ByApplicationId(appID).Delete(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete application: %w", err)
	}
	return nil
}

// GetApplication retrieves an application by ID
func (c *GraphClient) GetApplication(ctx context.Context, appID string) (*Application, error) {
	app, err := c.graphClient.Applications().ByApplicationId(appID).Get(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("get application: %w", err)
	}

	return &Application{
		ID:          *app.GetId(),
		AppID:       *app.GetAppId(),
		DisplayName: *app.GetDisplayName(),
	}, nil
}

// ListAllExpiredApplications retrieves applications that have expired password credentials
// with the prefix used for e2e testing
func (c *GraphClient) ListAllExpiredApplications(ctx context.Context) ([]Application, error) {
	apps := []Application{}
	resp, err := c.graphClient.Applications().Get(ctx, &applications.ApplicationsRequestBuilderGetRequestConfiguration{
		QueryParameters: &applications.ApplicationsRequestBuilderGetQueryParameters{
			Filter: ptr.To(fmt.Sprintf("startsWith(displayName,'%s')", appRegistrationPrefix)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get application: %w", err)
	}

	for _, app := range resp.GetValue() {
		skipApp := false
		// Skip deleting app registrations that have creds not expired
		for _, cred := range app.GetPasswordCredentials() {
			if cred.GetEndDateTime() != nil && cred.GetEndDateTime().After(time.Now()) {
				skipApp = true
				break
			}
		}
		if !skipApp {
			apps = append(apps, Application{
				ID:          *app.GetId(),
				AppID:       *app.GetAppId(),
				DisplayName: *app.GetDisplayName(),
			})
		}
	}

	return apps, nil
}

// GraphClient wraps the Microsoft Graph SDK with authentication and common operations
type GraphClient struct {
	graphClient *graphsdk.GraphBaseServiceClient
}

// azureAuthProvider implements the Kiota AuthenticationProvider interface
type azureAuthProvider struct {
	cred azcore.TokenCredential
}

func (a *azureAuthProvider) AuthenticateRequest(ctx context.Context, request *abstractions.RequestInformation, additionalAuthenticationContext map[string]interface{}) error {
	token, err := a.cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://graph.microsoft.com/.default"},
	})
	if err != nil {
		return fmt.Errorf("get token: %w", err)
	}
	request.Headers.Add("Authorization", "Bearer "+token.Token)
	return nil
}

// NewGraphClient creates a new Graph GraphClient with automatic authentication
func NewGraphClient(ctx context.Context, cred azcore.TokenCredential) (*GraphClient, error) {
	authProvider := &azureAuthProvider{cred: cred}

	httpClient, err := kiotahttp.NewNetHttpRequestAdapter(authProvider)
	if err != nil {
		return nil, fmt.Errorf("create request adapter: %w", err)
	}

	graphClient := graphsdk.NewGraphBaseServiceClient(httpClient, nil)

	return &GraphClient{
		graphClient: graphClient,
	}, nil
}

// GetGraphClient returns the underlying Graph SDK GraphClient for advanced operations
func (c *GraphClient) GetGraphClient() *graphsdk.GraphBaseServiceClient {
	return c.graphClient
}
