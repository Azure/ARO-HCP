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

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/environment"
	"github.com/Azure/ARO-HCP/test/util/integration"
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
		found bool
		creds azcore.TokenCredential
		err   error
		opts  azcore.ClientOptions
	)

	if subscriptionID, found = os.LookupEnv("CUSTOMER_SUBSCRIPTION"); !found {
		subscriptionID = "00000000-0000-0000-0000-000000000000"
	}
	e2eSetup, err = integration.LoadE2ESetupFile(os.Getenv("SETUP_FILEPATH"))
	if err != nil {
		return err
	}
	testEnv = environment.Environment(strings.ToLower(os.Getenv("AROHCP_ENV")))
	if testEnv == "" {
		testEnv = environment.Development
	}

	opts := prepareDevelopmentConf()
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
	clients, err = api.NewClientFactory(subscriptionID, creds, armOptions)
	if err != nil {
		return err
	}

	return nil
}
