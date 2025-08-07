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

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	. "github.com/onsi/ginkgo/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/environment"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/log"
)

var (
	clients        *api.ClientFactory
	subscriptionID string
	e2eSetup       integration.SetupModel
	testEnv        environment.Environment
)

// systemDataPolicy adds the X-Ms-Arm-Resource-System-Data header to cluster creation PUT requests.
type systemDataPolicy struct{}

func (p *systemDataPolicy) Do(req *policy.Request) (*http.Response, error) {
	// This policy should only apply to the PUT request that creates a cluster.
	if req.Raw().Method == http.MethodPut && strings.Contains(req.Raw().URL.Path, "/hcpOpenShiftClusters/") {
		createdBy := "shadownman@example.com"
		createdByType := api.CreatedByTypeUser
		createdAt := time.Now()
		systemData := &api.SystemData{
			CreatedBy:     &createdBy,
			CreatedByType: &createdByType,
			CreatedAt:     &createdAt,
		}
		systemDataBytes, err := json.Marshal(systemData)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal systemData for header: %w", err)
		}
		req.Raw().Header.Set("X-Ms-Arm-Resource-System-Data", string(systemDataBytes))
	}
	return req.Next()
}

// identityURLPolicy adds the X-Ms-Identity-Url header to simulate ARM.
type identityURLPolicy struct{}

func (p *identityURLPolicy) Do(req *policy.Request) (*http.Response, error) {
	// This header is needed for requests directly against the frontend.
	// The value can be a dummy value for local development.
	req.Raw().Header.Set("X-Ms-Identity-Url", "https://dummyhost.identity.azure.net")
	return req.Next()
}

func prepareEnvironmentConf(testEnv environment.Environment) azcore.ClientOptions {
	c := cloud.AzurePublic
	if environment.Development.Compare(testEnv) {
		c = cloud.Configuration{
			ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
			Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {
					Audience: "https://management.core.windows.net/",
					Endpoint: testEnv.Url(),
				},
			},
		}
	}
	opts := azcore.ClientOptions{
		Cloud:                           c,
		InsecureAllowCredentialWithHTTP: environment.Development.Compare(testEnv),
	}

	return opts
}

func setup(ctx context.Context) error {
	var (
		found            bool
		err              error
		opts             azcore.ClientOptions
		creds            azcore.TokenCredential
		subscriptionName string
	)

	if subscriptionName, found = os.LookupEnv("CUSTOMER_SUBSCRIPTION"); !found {
		subscriptionName = "FallbackSubscription"
	}

	testEnv = environment.Environment(strings.ToLower(os.Getenv("AROHCP_ENV")))
	if testEnv == "" {
		testEnv = environment.Development
	}

	opts = prepareEnvironmentConf(testEnv)
	// Add the custom policies to the PerCallPolicies slice.
	opts.PerCallPolicies = []policy.Policy{&systemDataPolicy{}, &identityURLPolicy{}}

	envOptions := &azidentity.EnvironmentCredentialOptions{
		ClientOptions: opts,
	}
	creds, err = azidentity.NewEnvironmentCredential(envOptions)

	if _, found := os.LookupEnv("LOCAL_DEVELOPMENT"); found {
		creds, err = azidentity.NewAzureCLICredential(nil)
	}
	if err != nil {
		return err
	}

	armOptions := &azcorearm.ClientOptions{
		ClientOptions: opts,
	}
	// Create a new armsubscriptions.Client from Azure SDK for Go
	armSubscriptionsClient, err := armsubscriptions.NewClient(creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create armsubscriptions.Client: %w", err)
	}
	subscriptionID, err = framework.GetSubscriptionID(ctx, armSubscriptionsClient, subscriptionName)
	if err != nil {
		return fmt.Errorf("failed to get subscription ID: %w", err)
	}
	clients, err = api.NewClientFactory(subscriptionID, creds, armOptions)
	if err != nil {
		return err
	}

	// Use GinkgoLabelFilter to check for the 'requirenothing' label
	labelFilter := GinkgoLabelFilter()
	if labels.RequireNothing.MatchesLabelFilter(labelFilter) {
		// Skip loading the e2esetup file
		e2eSetup = integration.SetupModel{} // zero value
	} else {
		e2eSetup, err = integration.LoadE2ESetupFile(os.Getenv("SETUP_FILEPATH"))
		if err != nil {
			if bicepName, found := os.LookupEnv("FALLBACK_TO_BICEP"); found {
				// Fallback: create a complete HCP cluster using bicep
				log.Logger.Warnf("Failed to load e2e setup file: %v. Falling back to bicep deployment.", err)
				e2eSetup, err = integration.FallbackCreateClusterWithBicep(ctx, subscriptionID, creds, clients, bicepName)
				if err != nil {
					return fmt.Errorf("failed to create cluster with bicep fallback: %w", err)
				}
			} else {
				return fmt.Errorf("failed to load e2e setup file and FALLBACK_TO_BICEP is not set: %w", err)
			}
		}
	}

	return nil
}
