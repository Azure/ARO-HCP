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

package framework

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/onsi/gomega"

	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	graphutil "github.com/Azure/ARO-HCP/internal/graph/util"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

// ExternalAuthParams configures Entra claim mappings and the console and CLI clients.
// Prefix and NoPrefix give the test a predictable RBAC subject.
type ExternalAuthParams struct {
	PrefixPolicy   hcpsdk20240610preview.UsernameClaimPrefixPolicy
	SubjectPrefix  string
	UsernameClaim  string
	GroupsClaim    string
	IncludeClients bool
}

// NewDefaultExternalAuthParams maps sub and groups with an explicit username prefix
// and configures both OpenShift clients.
func NewDefaultExternalAuthParams() ExternalAuthParams {
	return ExternalAuthParams{
		PrefixPolicy:   hcpsdk20240610preview.UsernameClaimPrefixPolicyPrefix,
		SubjectPrefix:  "prefix-",
		UsernameClaim:  "sub",
		GroupsClaim:    "groups",
		IncludeClients: true,
	}
}

// EntraExternalAuthContext holds the registered identity and its Kubernetes subject.
// ClientSecret is available for tests that create the console OAuth secret.
type EntraExternalAuthContext struct {
	App          *graphutil.Application
	SP           *graphutil.ServicePrincipal
	Credential   azcore.TokenCredential `json:"-"`
	ClientSecret string                 `json:"-"`
	Subject      string
	Scope        string
}

// String describes the external subject without formatting its credentials.
func (auth EntraExternalAuthContext) String() string {
	return fmt.Sprintf("Entra external auth subject %q", auth.Subject)
}

// GoString keeps credentials out of Go-syntax diagnostics.
func (auth EntraExternalAuthContext) GoString() string { return auth.String() }

// GomegaString keeps credentials out of Gomega assertion failures.
func (auth EntraExternalAuthContext) GomegaString() string { return auth.String() }

func buildEntraExternalAuth(tenantID, appID string, params ExternalAuthParams) hcpsdk20240610preview.ExternalAuth {
	username := &hcpsdk20240610preview.UsernameClaimProfile{
		Claim:        to.Ptr(params.UsernameClaim),
		PrefixPolicy: to.Ptr(params.PrefixPolicy),
	}
	if params.PrefixPolicy == hcpsdk20240610preview.UsernameClaimPrefixPolicyPrefix {
		username.Prefix = to.Ptr(params.SubjectPrefix)
	}
	props := &hcpsdk20240610preview.ExternalAuthProperties{
		Issuer: &hcpsdk20240610preview.TokenIssuerProfile{
			URL:       to.Ptr(fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tenantID)),
			Audiences: []*string{to.Ptr(appID)},
		},
		Claim: &hcpsdk20240610preview.ExternalAuthClaimProfile{
			Mappings: &hcpsdk20240610preview.TokenClaimMappingsProfile{
				Username: username,
				Groups:   &hcpsdk20240610preview.GroupClaimProfile{Claim: to.Ptr(params.GroupsClaim)},
			},
		},
	}
	if params.IncludeClients {
		client := func(name string, clientType hcpsdk20240610preview.ExternalAuthClientType) *hcpsdk20240610preview.ExternalAuthClientProfile {
			return &hcpsdk20240610preview.ExternalAuthClientProfile{
				ClientID: to.Ptr(appID),
				Component: &hcpsdk20240610preview.ExternalAuthClientComponentProfile{
					Name:                to.Ptr(name),
					AuthClientNamespace: to.Ptr("openshift-console"),
				},
				Type: to.Ptr(clientType),
			}
		}
		props.Clients = []*hcpsdk20240610preview.ExternalAuthClientProfile{
			client("console", hcpsdk20240610preview.ExternalAuthClientTypeConfidential),
			client("cli", hcpsdk20240610preview.ExternalAuthClientTypePublic),
		}
	}
	return hcpsdk20240610preview.ExternalAuth{Properties: props}
}

// SetupEntraExternalAuth registers a test-managed application and service principal
// and waits for their ExternalAuth resource to reach Succeeded. The username claim
// should identify the service principal by its object ID when using the grant helpers.
func (tc *perItOrDescribeTestContext) SetupEntraExternalAuth(ctx context.Context, resourceGroup, cluster, authName string, params ExternalAuthParams) (*EntraExternalAuthContext, error) {
	if params.PrefixPolicy != hcpsdk20240610preview.UsernameClaimPrefixPolicyPrefix && params.PrefixPolicy != hcpsdk20240610preview.UsernameClaimPrefixPolicyNoPrefix {
		return nil, fmt.Errorf("expected Prefix or NoPrefix to determine the Entra subject, got %q", params.PrefixPolicy)
	}
	tenantID := tc.TenantID()
	if tenantID == "" {
		return nil, errors.New("tenant ID is required for Entra authentication")
	}
	app, sp, err := tc.NewAppRegistrationWithServicePrincipal(ctx)
	if err != nil {
		return nil, fmt.Errorf("register Entra app and service principal: %s", azureFailureError(err))
	}
	graphClient, err := tc.GetGraphClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("get Graph client for Entra application: %s", azureFailureError(err))
	}
	now := time.Now()
	// Allow for clock skew between the test runner and Microsoft Graph.
	password, err := graphClient.AddPassword(ctx, app.ID, "external-auth-pass", now.Add(-5*time.Minute), now.Add(24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("add password to Entra application: %s", azureFailureError(err))
	}
	credential, err := azidentity.NewClientSecretCredential(tenantID, app.AppID, password.SecretText, nil)
	if err != nil {
		return nil, fmt.Errorf("create credential for Entra application: %s", azureFailureError(err))
	}
	factory, err := tc.Get20240610ClientFactory(ctx)
	if err != nil {
		return nil, fmt.Errorf("get ExternalAuth client factory: %s", azureFailureError(err))
	}
	externalAuth := buildEntraExternalAuth(tenantID, app.AppID, params)
	result, err := CreateOrUpdateExternalAuthAndWait20240610(ctx, factory.NewExternalAuthsClient(), resourceGroup, cluster, authName, externalAuth, ExternalAuthCreationTimeout)
	if err != nil {
		return nil, fmt.Errorf("provision external auth %q: %s", authName, azureFailureError(err))
	}
	if result == nil || result.Properties == nil || result.Properties.ProvisioningState == nil {
		return nil, fmt.Errorf("external auth %q returned no provisioning state", authName)
	}
	if state := *result.Properties.ProvisioningState; state != hcpsdk20240610preview.ExternalAuthProvisioningStateSucceeded {
		return nil, fmt.Errorf("external auth %q provisioning state is %q, expected Succeeded", authName, state)
	}
	subject := sp.ID
	if params.PrefixPolicy == hcpsdk20240610preview.UsernameClaimPrefixPolicyPrefix {
		subject = params.SubjectPrefix + sp.ID
	}
	return &EntraExternalAuthContext{
		App:          app,
		SP:           sp,
		Credential:   credential,
		ClientSecret: password.SecretText,
		Subject:      subject,
		Scope:        app.AppID + "/.default",
	}, nil
}

// GrantClusterRole creates a named cluster role binding for the external subject.
func (auth *EntraExternalAuthContext) GrantClusterRole(ctx context.Context, bindingName, clusterRoleName string, adminRESTConfig *rest.Config) error {
	return CreateClusterRoleBindingForSubject(ctx, bindingName, auth.Subject, clusterRoleName, adminRESTConfig)
}

// GrantClusterAdmin binds the external subject to cluster-admin.
func (auth *EntraExternalAuthContext) GrantClusterAdmin(ctx context.Context, bindingName string, adminRESTConfig *rest.Config) error {
	return auth.GrantClusterRole(ctx, bindingName, "cluster-admin", adminRESTConfig)
}

// GetRESTConfig waits for Entra to issue a token and builds a client config
// using the admin config's server address and TLS trust settings.
func (auth *EntraExternalAuthContext) GetRESTConfig(ctx context.Context, adminRESTConfig *rest.Config) (*rest.Config, error) {
	if adminRESTConfig == nil {
		return nil, errors.New("admin REST config is required for the cluster connection settings")
	}
	server, _, err := rest.DefaultServerUrlFor(adminRESTConfig)
	if err != nil {
		return nil, errors.New("invalid cluster server address in admin REST config")
	}
	// A kubeconfig server URL can itself contain credentials.
	server.User = nil
	server.RawQuery = ""
	server.ForceQuery = false
	server.Fragment = ""
	server.RawFragment = ""

	ctx, cancel := context.WithTimeout(ctx, OIDCTokenAcquisitionTimeout)
	defer cancel()
	var token azcore.AccessToken
	var tokenErr error
	// Return a safe error to the caller. Gomega's failure message may include
	// the caller's cancellation cause or other request details.
	g := gomega.NewGomega(func(string, ...int) {})
	acquired := g.Eventually(ctx, func() bool {
		token, tokenErr = auth.Credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{auth.Scope}})
		return tokenErr == nil && token.Token != ""
	}).WithPolling(10*time.Second).Should(gomega.BeTrue(), "acquire an OIDC token for external authentication")
	if !acquired {
		if tokenErr != nil {
			return nil, fmt.Errorf("acquire Entra OIDC token: %s", azureFailureError(tokenErr))
		}
		return nil, errors.New("received no access token from Entra")
	}
	return &rest.Config{
		Host:        server.String(),
		APIPath:     adminRESTConfig.APIPath,
		BearerToken: token.Token,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure:   adminRESTConfig.Insecure,
			ServerName: adminRESTConfig.ServerName,
			CAFile:     adminRESTConfig.CAFile,
			CAData:     slices.Clone(adminRESTConfig.CAData),
			NextProtos: slices.Clone(adminRESTConfig.NextProtos),
		},
	}, nil
}

// SetupExternalAuthAccess provisions the default Entra provider, grants cluster-admin,
// and returns the identity and its bearer-token config.
func (tc *perItOrDescribeTestContext) SetupExternalAuthAccess(ctx context.Context, resourceGroup, cluster, authName string, adminRESTConfig *rest.Config) (*EntraExternalAuthContext, *rest.Config, error) {
	auth, err := tc.SetupEntraExternalAuth(ctx, resourceGroup, cluster, authName, NewDefaultExternalAuthParams())
	if err != nil {
		return nil, nil, err
	}
	if err := auth.GrantClusterAdmin(ctx, "entra-admins", adminRESTConfig); err != nil {
		return nil, nil, err
	}
	config, err := auth.GetRESTConfig(ctx, adminRESTConfig)
	if err != nil {
		return nil, nil, err
	}
	return auth, config, nil
}
