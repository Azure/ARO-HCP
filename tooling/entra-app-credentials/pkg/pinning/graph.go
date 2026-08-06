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

package pinning

import (
	"context"
	"crypto/sha1" //nolint:gosec // Microsoft Entra certificate thumbprints are defined as SHA-1.
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	kiotaabstractions "github.com/microsoft/kiota-abstractions-go"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	graphapplications "github.com/microsoftgraph/msgraph-sdk-go/applications"
	graphmodels "github.com/microsoftgraph/msgraph-sdk-go/models"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

const graphScope = "https://graph.microsoft.com/.default"

var retryInterval = 2 * time.Second

type graphAPI interface {
	ListApplications(ctx context.Context, filter string) ([]graphmodels.Applicationable, error)
	ReplaceKeyCredentials(ctx context.Context, applicationID, certificateName string, certificate []byte) error
	GetApplication(ctx context.Context, applicationID string) (graphmodels.Applicationable, error)
}

type microsoftGraphAPI struct {
	client *msgraphsdk.GraphServiceClient
}

type graphClient struct {
	api graphAPI
}

// NewGraphClient creates an application client backed by Microsoft's official
// Graph SDK.
func NewGraphClient(credential azcore.TokenCredential) (ApplicationClient, error) {
	client, err := msgraphsdk.NewGraphServiceClientWithCredentials(credential, []string{graphScope})
	if err != nil {
		return nil, fmt.Errorf("create Microsoft Graph client: %w", err)
	}
	return newGraphClient(&microsoftGraphAPI{client: client}), nil
}

func newGraphClient(api graphAPI) ApplicationClient {
	return &graphClient{api: api}
}

func (c *graphClient) FindApplication(ctx context.Context, displayName string) (*Application, error) {
	var application *Application
	err := retry(ctx, func() error {
		filter := fmt.Sprintf("displayName eq '%s'", strings.ReplaceAll(displayName, "'", "''"))
		applications, err := c.api.ListApplications(ctx, filter)
		if err != nil {
			return classifyGraphError(err)
		}
		switch len(applications) {
		case 0:
			return &transientError{err: fmt.Errorf("application %q is not visible in Microsoft Graph yet", displayName)}
		case 1:
			application, err = convertApplication(applications[0])
			return err
		default:
			return fmt.Errorf("display name %q matched %d applications; expected exactly one", displayName, len(applications))
		}
	})
	return application, err
}

func (c *graphClient) ReplaceKeyCredentials(ctx context.Context, applicationID, certificateName string, certificate []byte) error {
	return retry(ctx, func() error {
		return classifyGraphError(c.api.ReplaceKeyCredentials(ctx, applicationID, certificateName, certificate))
	})
}

func (c *graphClient) GetApplication(ctx context.Context, applicationID string) (*Application, error) {
	var application *Application
	err := retry(ctx, func() error {
		graphApplication, err := c.api.GetApplication(ctx, applicationID)
		if err != nil {
			return classifyGraphError(err)
		}
		application, err = convertApplication(graphApplication)
		return err
	})
	return application, err
}

func (c *microsoftGraphAPI) ListApplications(ctx context.Context, filter string) ([]graphmodels.Applicationable, error) {
	top := int32(2)
	response, err := c.client.Applications().Get(ctx, &graphapplications.ApplicationsRequestBuilderGetRequestConfiguration{
		QueryParameters: &graphapplications.ApplicationsRequestBuilderGetQueryParameters{
			Filter: &filter,
			Select: []string{"id", "appId", "displayName", "keyCredentials"},
			Top:    &top,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	if response == nil {
		return nil, fmt.Errorf("list applications: Microsoft Graph returned an empty response")
	}
	return response.GetValue(), nil
}

func (c *microsoftGraphAPI) ReplaceKeyCredentials(ctx context.Context, applicationID, certificateName string, certificate []byte) error {
	credentialType := keyCredentialType
	credentialUsage := keyCredentialUsage
	keyCredential := graphmodels.NewKeyCredential()
	keyCredential.SetDisplayName(&certificateName)
	keyCredential.SetKey(certificate)
	keyCredential.SetTypeEscaped(&credentialType)
	keyCredential.SetUsage(&credentialUsage)

	application := graphmodels.NewApplication()
	application.SetKeyCredentials([]graphmodels.KeyCredentialable{keyCredential})
	if _, err := c.client.Applications().ByApplicationId(applicationID).Patch(ctx, application, nil); err != nil {
		return fmt.Errorf("patch application: %w", err)
	}
	return nil
}

func (c *microsoftGraphAPI) GetApplication(ctx context.Context, applicationID string) (graphmodels.Applicationable, error) {
	application, err := c.client.Applications().ByApplicationId(applicationID).Get(
		ctx,
		&graphapplications.ApplicationItemRequestBuilderGetRequestConfiguration{
			QueryParameters: &graphapplications.ApplicationItemRequestBuilderGetQueryParameters{
				Select: []string{"id", "appId", "displayName", "keyCredentials"},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("get application: %w", err)
	}
	if application == nil {
		return nil, fmt.Errorf("get application: Microsoft Graph returned an empty response")
	}
	return application, nil
}

func convertApplication(application graphmodels.Applicationable) (*Application, error) {
	if application == nil || application.GetId() == nil || strings.TrimSpace(*application.GetId()) == "" {
		return nil, fmt.Errorf("application returned by Microsoft Graph has no ID")
	}

	credentials := make([]KeyCredential, 0, len(application.GetKeyCredentials()))
	for _, credential := range application.GetKeyCredentials() {
		if credential == nil {
			continue
		}
		credentials = append(credentials, KeyCredential{
			CustomKeyIdentifier: normalizeCustomKeyIdentifier(credential.GetCustomKeyIdentifier()),
			Type:                valueOrEmpty(credential.GetTypeEscaped()),
			Usage:               valueOrEmpty(credential.GetUsage()),
		})
	}
	return &Application{
		ID:             *application.GetId(),
		AppID:          valueOrEmpty(application.GetAppId()),
		DisplayName:    valueOrEmpty(application.GetDisplayName()),
		KeyCredentials: credentials,
	}, nil
}

// Microsoft Graph currently serializes customKeyIdentifier as an uppercase hex
// SHA-1 string even though the SDK model exposes it as []byte. Depending on the
// Kiota parser version, the SDK may return the 20 thumbprint bytes, the 40 ASCII
// hex bytes, or the base64-decoded bytes of that hex string.
func normalizeCustomKeyIdentifier(value []byte) []byte {
	if len(value) == 0 || len(value) == sha1.Size {
		return value
	}
	if decoded, err := hex.DecodeString(string(value)); err == nil && len(decoded) == sha1.Size {
		return decoded
	}
	reencoded := base64.StdEncoding.EncodeToString(value)
	if decoded, err := hex.DecodeString(reencoded); err == nil && len(decoded) == sha1.Size {
		return decoded
	}
	return value
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type transientError struct {
	err error
}

func (e *transientError) Error() string {
	return e.err.Error()
}

func (e *transientError) Unwrap() error {
	return e.err
}

func retry(ctx context.Context, operation func() error) error {
	var lastErr error
	for {
		err := operation()
		if err == nil {
			return nil
		}
		var transient *transientError
		if !errors.As(err, &transient) {
			return err
		}
		lastErr = err
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("retry Microsoft Graph operation: %w (last error: %v)", ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

func classifyGraphError(err error) error {
	if err == nil {
		return nil
	}

	// Kiota handles its retryable HTTP statuses. The outer retry only covers
	// Graph propagation and transport failures that escape the SDK middleware.
	var apiError kiotaabstractions.ApiErrorable
	if errors.As(err, &apiError) {
		if apiError.GetStatusCode() == http.StatusNotFound {
			return &transientError{err: err}
		}
		return err
	}

	var networkError net.Error
	if errors.As(err, &networkError) {
		return &transientError{err: err}
	}
	return err
}
