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
	"fmt"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	. "github.com/onsi/ginkgo/v2"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/environment"
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
		found bool
		err   error
		opts  azcore.ClientOptions
		creds azcore.TokenCredential
	)

	if subscriptionID, found = os.LookupEnv("CUSTOMER_SUBSCRIPTION"); !found {
		subscriptionID = "00000000-0000-0000-0000-000000000000"
	}
	testEnv = environment.Environment(strings.ToLower(os.Getenv("AROHCP_ENV")))
	if testEnv == "" {
		testEnv = environment.Development
	}

	opts = prepareEnvironmentConf(testEnv)
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
			if _, found := os.LookupEnv("FALLBACK_TO_BICEP"); found {
				// Fallback: create a complete HCP cluster using bicep
				log.Logger.Warnf("Failed to load e2e setup file: %v. Falling back to bicep deployment.", err)
				e2eSetup, err = integration.FallbackCreateClusterWithBicep(ctx, subscriptionID, creds, clients)
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
