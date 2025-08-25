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

package util

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/applications"
	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/models"
)

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
func (c *Client) CreateApplication(ctx context.Context, displayName string, redirectURIs []string) (*Application, error) {
	aud := "AzureADMyOrganization"
	app := models.NewApplication()
	app.SetDisplayName(&displayName)
	app.SetSignInAudience(&aud)

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
func (c *Client) AddPassword(ctx context.Context, appID, displayName string, startTime, endTime time.Time) (*PasswordCredential, error) {
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
func (c *Client) UpdateApplicationRedirectUris(ctx context.Context, appID string, redirectURIs []string) error {
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
func (c *Client) DeleteApplication(ctx context.Context, appID string) error {
	err := c.graphClient.Applications().ByApplicationId(appID).Delete(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete application: %w", err)
	}
	return nil
}

// GetApplication retrieves an application by ID
func (c *Client) GetApplication(ctx context.Context, appID string) (*Application, error) {
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
