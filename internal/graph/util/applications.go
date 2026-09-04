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
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	abstractions "github.com/microsoft/kiota-abstractions-go"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/applications"
	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/me"
	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/models"
	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/models/odataerrors"
	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/serviceprincipals"
)

const AppRegistrationPrefix = "aro-hcp-e2e-"

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

// CreateApplication creates a new Microsoft Entra application with
// requestedAccessTokenVersion=2 to allow token issuance from login.microsoftonline.com
func (c *Client) CreateApplication(ctx context.Context, displayName string, redirectURIs []string) (*Application, error) {
	app := models.NewApplication()
	app.SetDisplayName(&displayName)

	// Create web application with redirect URIs
	webApp := models.NewWebApplication()
	webApp.SetRedirectUris(redirectURIs)
	app.SetWeb(webApp)

	// set requestedAccessTokenVersion=2
	apiApp := models.NewApiApplication()
	apiApp.SetRequestedAccessTokenVersion(to.Ptr[int32](2))
	app.SetApi(apiApp)

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

// AddPassword adds a password credential to an application.
// Transient errors and eventual-consistency delays (e.g. 404 right after app
// creation) are retried by the transport-level graphRetryHandler middleware.
func (c *Client) AddPassword(ctx context.Context, appID, displayName string, startTime, endTime time.Time) (*PasswordCredential, error) {
	passwordCred := models.NewPasswordCredential()
	passwordCred.SetDisplayName(&displayName)
	passwordCred.SetStartDateTime(&startTime)
	passwordCred.SetEndDateTime(&endTime)

	reqBody := applications.NewItemAddPasswordPostRequestBody()
	reqBody.SetPasswordCredential(passwordCred)

	result, err := c.graphClient.Applications().ByApplicationId(appID).AddPassword().Post(ctx, reqBody, nil)
	if err != nil {
		return nil, fmt.Errorf("add password: %w", odataErrorWithDiagnostics(err))
	}

	return &PasswordCredential{
		SecretText: *result.GetSecretText(),
		KeyID:      result.GetKeyId().String(),
		StartTime:  *result.GetStartDateTime(),
		EndTime:    *result.GetEndDateTime(),
	}, nil
}

// odataErrorWithDiagnostics wraps an error with OData diagnostic details
// (HTTP status, error code, message) when available, for easier failure triage.
func odataErrorWithDiagnostics(err error) error {
	var odataErr *odataerrors.ODataError
	if !errors.As(err, &odataErr) {
		return err
	}

	diagDetails := fmt.Sprintf("HTTP %d", odataErr.ResponseStatusCode)
	if odataError := odataErr.GetErrorEscaped(); odataError != nil {
		if errCode := odataError.GetCode(); errCode != nil {
			diagDetails += fmt.Sprintf(", %s", *errCode)
		}
		if errMsg := odataError.GetMessage(); errMsg != nil {
			diagDetails += fmt.Sprintf(": %s", *errMsg)
		}
	}
	return fmt.Errorf("%s: %w", diagDetails, err)
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
		return fmt.Errorf("delete application: %w", odataErrorWithDiagnostics(err))
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

// ListOwnedExpiredApplications retrieves applications owned by the current caller
// where all their credentials have expired and a display name starting with the e2e prefix.
// For user credentials, this uses /me/ownedObjects. For service principal credentials,
// this uses /servicePrincipals/{id}/ownedObjects to ensure we only return applications
// we have permission to delete.
func (c *Client) ListOwnedExpiredApplications(ctx context.Context) ([]Application, error) {
	logger := logr.FromContextOrDiscard(ctx)
	if c.isUser {
		logger.V(1).Info("Using /me/ownedObjects endpoint (user credential)")
	} else {
		logger.V(1).Info("Using /servicePrincipals/ownedObjects endpoint (service principal credential)", "objectID", c.objectID)
	}

	headers := abstractions.NewRequestHeaders()
	headers.Add("ConsistencyLevel", "eventual")

	filter := to.Ptr(fmt.Sprintf("startsWith(displayName,'%s')", AppRegistrationPrefix))
	count := to.Ptr(true)

	// Default to the service-principal-scoped /servicePrincipals/{id}/ownedObjects endpoint;
	// when the caller is a user token, this is overridden below to use /me/ownedObjects instead.
	getPage := func(ctx context.Context, nextLink *string) (models.ApplicationCollectionResponseable, error) {
		builder := c.graphClient.ServicePrincipals().ByServicePrincipalId(c.objectID).OwnedObjects().GraphApplication()
		if nextLink != nil {
			builder = builder.WithUrl(*nextLink)
		}
		return builder.Get(ctx, &serviceprincipals.ItemOwnedObjectsGraphApplicationRequestBuilderGetRequestConfiguration{
			Headers: headers,
			QueryParameters: &serviceprincipals.ItemOwnedObjectsGraphApplicationRequestBuilderGetQueryParameters{
				Filter: filter,
				Count:  count,
			},
		})
	}

	if c.isUser {
		getPage = func(ctx context.Context, nextLink *string) (models.ApplicationCollectionResponseable, error) {
			builder := c.graphClient.Me().OwnedObjects().GraphApplication()
			if nextLink != nil {
				builder = builder.WithUrl(*nextLink)
			}
			return builder.Get(ctx, &me.OwnedObjectsGraphApplicationRequestBuilderGetRequestConfiguration{
				Headers: headers,
				QueryParameters: &me.OwnedObjectsGraphApplicationRequestBuilderGetQueryParameters{
					Filter: filter,
					Count:  count,
				},
			})
		}
	}

	var apps []Application
	resp, err := getPage(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list owned applications: %w", err)
	}

	now := time.Now()
	for resp != nil {
		for _, app := range resp.GetValue() {
			creds := app.GetPasswordCredentials()
			// Skip apps with no credentials - they may still be in use
			if len(creds) == 0 {
				continue
			}

			// Only include apps where all credentials have expired
			allExpired := true
			for _, cred := range creds {
				if cred.GetEndDateTime() == nil || cred.GetEndDateTime().After(now) {
					allExpired = false
					break
				}
			}
			if allExpired {
				apps = append(apps, Application{
					ID:          *app.GetId(),
					AppID:       *app.GetAppId(),
					DisplayName: *app.GetDisplayName(),
				})
			}
		}

		nextLink := resp.GetOdataNextLink()
		if nextLink == nil || *nextLink == "" {
			break
		}

		resp, err = getPage(ctx, nextLink)
		if err != nil {
			return nil, fmt.Errorf("list owned applications (next page): %w", err)
		}
	}

	return apps, nil
}
