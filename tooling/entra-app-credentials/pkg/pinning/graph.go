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
	"fmt"
	"strings"
	"time"

	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	graphapplications "github.com/microsoftgraph/msgraph-sdk-go/applications"
	graphmodels "github.com/microsoftgraph/msgraph-sdk-go/models"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/graph"
)

type graphAPI interface {
	ListApplications(ctx context.Context, filter string) ([]graphmodels.Applicationable, error)
	ReplaceKeyCredentials(ctx context.Context, applicationID, certificateName string, certificate []byte) error
	GetApplication(ctx context.Context, applicationID string) (graphmodels.Applicationable, error)
}

type microsoftGraphAPI struct {
	client *msgraphsdk.GraphServiceClient
}

type graphClient struct {
	api           graphAPI
	retryInterval time.Duration
}

// GraphClientOption configures a graph client.
type GraphClientOption func(*graphClient)

// WithRetryInterval sets how long lookups wait between attempts while an object
// is still replicating in Microsoft Graph.
func WithRetryInterval(interval time.Duration) GraphClientOption {
	return func(c *graphClient) {
		c.retryInterval = interval
	}
}

// NewGraphClient creates an application client backed by Microsoft's official
// Graph SDK.
func NewGraphClient(credential azcore.TokenCredential, options ...GraphClientOption) (ApplicationClient, error) {
	client, err := msgraphsdk.NewGraphServiceClientWithCredentials(credential, []string{graph.Scope})
	if err != nil {
		return nil, fmt.Errorf("create Microsoft Graph client: %w", err)
	}
	return newGraphClient(&microsoftGraphAPI{client: client}, options...), nil
}

func newGraphClient(api graphAPI, options ...GraphClientOption) ApplicationClient {
	client := &graphClient{api: api, retryInterval: graph.DefaultRetryInterval}
	for _, option := range options {
		option(client)
	}
	return client
}

func (c *graphClient) FindApplication(ctx context.Context, displayName string) (*Application, error) {
	return graph.Retry(ctx, c.retryInterval, func(ctx context.Context) (*Application, error) {
		filter := fmt.Sprintf("displayName eq '%s'", graph.QuoteFilterValue(displayName))
		applications, err := c.api.ListApplications(ctx, filter)
		if err != nil {
			return nil, err
		}

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

func (c *graphClient) ReplaceKeyCredentials(ctx context.Context, applicationID, certificateName string, certificate []byte) error {
	return graph.RetryDo(ctx, c.retryInterval, func(ctx context.Context) error {
		return c.api.ReplaceKeyCredentials(ctx, applicationID, certificateName, certificate)
	})
}

func (c *graphClient) GetApplication(ctx context.Context, applicationID string) (*Application, error) {
	return graph.Retry(ctx, c.retryInterval, func(ctx context.Context) (*Application, error) {
		application, err := c.api.GetApplication(ctx, applicationID)
		if err != nil {
			return nil, err
		}
		return convertApplication(application)
	})
}

func (c *microsoftGraphAPI) ListApplications(ctx context.Context, filter string) ([]graphmodels.Applicationable, error) {
	response, err := c.client.Applications().Get(ctx, &graphapplications.ApplicationsRequestBuilderGetRequestConfiguration{
		QueryParameters: &graphapplications.ApplicationsRequestBuilderGetQueryParameters{
			Filter: &filter,
			Select: []string{"id", "appId", "displayName", "keyCredentials"},
			Top:    ptr.To(int32(2)),
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
	keyCredential := graphmodels.NewKeyCredential()
	keyCredential.SetDisplayName(&certificateName)
	keyCredential.SetKey(certificate)
	keyCredential.SetTypeEscaped(ptr.To(keyCredentialType))
	keyCredential.SetUsage(ptr.To(keyCredentialUsage))

	application := graphmodels.NewApplication()
	application.SetKeyCredentials([]graphmodels.KeyCredentialable{keyCredential})
	_, err := c.client.Applications().ByApplicationId(applicationID).Patch(ctx, application, nil)
	if err != nil {
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
	if application == nil || strings.TrimSpace(ptr.Deref(application.GetId(), "")) == "" {
		return nil, fmt.Errorf("application returned by Microsoft Graph has no ID")
	}

	credentials := make([]KeyCredential, 0, len(application.GetKeyCredentials()))
	for _, credential := range application.GetKeyCredentials() {
		if credential == nil {
			continue
		}
		credentials = append(credentials, KeyCredential{
			CustomKeyIdentifier: normalizeCustomKeyIdentifier(credential.GetCustomKeyIdentifier()),
			Type:                ptr.Deref(credential.GetTypeEscaped(), ""),
			Usage:               ptr.Deref(credential.GetUsage(), ""),
		})
	}
	return &Application{
		ID:             *application.GetId(),
		AppID:          ptr.Deref(application.GetAppId(), ""),
		DisplayName:    ptr.Deref(application.GetDisplayName(), ""),
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
