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

package subscriptions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	msgraph "github.com/microsoftgraph/msgraph-sdk-go"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/e2e"
)

const (
	// DeploymentLocation stores subscription-scope deployment metadata only; the
	// RBAC resources themselves are not regional.
	DeploymentLocation = "westus3"

	templateDirectory = "test-artifacts/generated-test-artifacts/subscription-rbac/"
	ciBotTemplate     = "ci-bot-rbac-subscription"
	mockRBACTemplate  = "e2e-subscription-rbac-assignment-subscription"
)

// Deployment names the canonical Bicep template for one subscription-scope
// ARM deployment. Parameters holds plain values; the ARM {"value": ...} envelope is added on
// submission. Deployments are incremental: they never remove existing resources.
type Deployment struct {
	Name         string
	templateName string
	Parameters   map[string]any
}

// DeploymentClient submits a deployment to one subscription and waits for it.
type DeploymentClient interface {
	Deploy(ctx context.Context, deployment Deployment) error
}

type armDeploymentClient struct {
	client *armresources.DeploymentsClient
}

func (c armDeploymentClient) Deploy(ctx context.Context, deployment Deployment) error {
	template, err := readTemplate(deployment.templateName)
	if err != nil {
		return err
	}
	parameters := make(map[string]map[string]any, len(deployment.Parameters))
	for name, value := range deployment.Parameters {
		parameters[name] = map[string]any{"value": value}
	}
	poller, err := c.client.BeginCreateOrUpdateAtSubscriptionScope(ctx, deployment.Name, armresources.Deployment{
		Location: to.Ptr(DeploymentLocation),
		Properties: &armresources.DeploymentProperties{
			Mode:       to.Ptr(armresources.DeploymentModeIncremental),
			Template:   template,
			Parameters: parameters,
		},
	}, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// Deployer applies deployments to subscriptions.
type Deployer struct {
	NewClient func(subscriptionID string) (DeploymentClient, error)
}

// NewDeployer returns a deployer backed by the ARM SDK.
func NewDeployer(credential azcore.TokenCredential, options *azcorearm.ClientOptions) *Deployer {
	return &Deployer{NewClient: func(subscriptionID string) (DeploymentClient, error) {
		client, err := armresources.NewDeploymentsClient(subscriptionID, credential, options)
		if err != nil {
			return nil, err
		}
		return armDeploymentClient{client: client}, nil
	}}
}

// Deploy applies every deployment, in order, to every subscription. Failures on
// one subscription do not stop the others; all are returned together.
func (d *Deployer) Deploy(ctx context.Context, subscriptionIDs []string, deployments ...Deployment) error {
	logger := logr.FromContextOrDiscard(ctx)
	var failures []error
	for _, subscriptionID := range subscriptionIDs {
		client, err := d.NewClient(subscriptionID)
		if err != nil {
			failures = append(failures, fmt.Errorf("subscription %s: create deployment client: %w", subscriptionID, err))
			continue
		}
		for _, deployment := range deployments {
			logger.Info("Deploying subscription RBAC", "subscriptionID", subscriptionID, "deployment", deployment.Name)
			if err := client.Deploy(ctx, deployment); err != nil {
				failures = append(failures, fmt.Errorf("subscription %s: deployment %s: %w", subscriptionID, deployment.Name, err))
			}
		}
	}
	if err := errors.Join(failures...); err != nil {
		return fmt.Errorf("subscription RBAC deployment failed: %w", err)
	}
	return nil
}

// CIBotOptions mirrors the parameters of ci-bot-rbac-subscription.bicep that
// differ per subscription kind.
type CIBotOptions struct {
	IsGlobalSubscription bool
	GrantAKSRBAC         bool
	GrantKeyVaultAdmin   bool
}

// CIBotRBAC grants the CI bot its subscription roles.
func CIBotRBAC(botPrincipalID string, options CIBotOptions) Deployment {
	return Deployment{Name: "slot-manager-" + ciBotTemplate, templateName: ciBotTemplate, Parameters: map[string]any{
		"botPrincipalId":       botPrincipalID,
		"isGlobalSubscription": options.IsGlobalSubscription,
		"grantAksRbac":         options.GrantAKSRBAC,
		"grantKeyVaultAdmin":   options.GrantKeyVaultAdmin,
	}}
}

// MockPrincipals are the service principal object IDs of the shared mock
// identities. ClustersServiceARMHelper is optional.
type MockPrincipals struct {
	FirstParty, ARMHelper, MSIMock, ClustersServiceARMHelper string
}

// MockRBAC defines the subscription-local mock roles and assigns them to the
// shared mock principals. The slot mock identities' grants depend on the roles
// it defines.
func MockRBAC(principals MockPrincipals) Deployment {
	parameters := map[string]any{
		"firstPartyPrincipalId": principals.FirstParty,
		"armHelperPrincipalId":  principals.ARMHelper,
		"miMockPrincipalId":     principals.MSIMock,
	}
	if principals.ClustersServiceARMHelper != "" {
		parameters["clustersServiceArmHelperPrincipalId"] = principals.ClustersServiceARMHelper
	}
	return Deployment{Name: "slot-manager-" + mockRBACTemplate, templateName: mockRBACTemplate, Parameters: parameters}
}

// readTemplate reads a template compiled by `make -C test` from
// dev-infrastructure/templates and embedded in the test artifacts.
func readTemplate(name string) (map[string]any, error) {
	data, err := e2e.TestArtifactsFS.ReadFile(templateDirectory + name + ".json")
	if err != nil {
		return nil, fmt.Errorf("read compiled template %s (build with make -C test): %w", name, err)
	}
	var template map[string]any
	if err := json.Unmarshal(data, &template); err != nil {
		return nil, fmt.Errorf("decode compiled template %s: %w", name, err)
	}
	return template, nil
}

// ResolveServicePrincipalID returns the object ID of the service principal of
// the shared Entra application with the given display name. The application is
// looked up by its normalized unique name (lowercase, spaces to dashes), exactly
// as the Bicep entra-app-lookup module does. The application must already exist.
func ResolveServicePrincipalID(ctx context.Context, client *msgraph.GraphServiceClient, displayName string) (string, error) {
	uniqueName := strings.ToLower(strings.ReplaceAll(displayName, " ", "-"))
	application, err := client.ApplicationsWithUniqueName(&uniqueName).Get(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("read application %q: %w", displayName, err)
	}
	appID := ptr.Deref(application.GetAppId(), "")
	if appID == "" {
		return "", fmt.Errorf("application %q has no application ID", displayName)
	}
	principal, err := client.ServicePrincipalsWithAppId(&appID).Get(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("read service principal of application %q: %w", displayName, err)
	}
	principalID := ptr.Deref(principal.GetId(), "")
	if principalID == "" {
		return "", fmt.Errorf("service principal of application %q has no object ID", displayName)
	}
	return principalID, nil
}
