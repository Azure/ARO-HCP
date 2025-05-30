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
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/integration"
)

var (
	clients        *api.ClientFactory
	subscriptionID string
	e2eSetup       integration.SetupModel
)

func prepareDevelopmentConf() azcore.ClientOptions {
	c := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Audience: "https://management.core.windows.net/",
				Endpoint: "http://localhost:8443",
			},
		},
	}
	opts := azcore.ClientOptions{
		Cloud:                           c,
		InsecureAllowCredentialWithHTTP: true,
	}

	return opts
}

func setup(ctx context.Context) error {
	var (
		found bool
		creds azcore.TokenCredential
		err   error
	)

	if subscriptionID, found = os.LookupEnv("CUSTOMER_SUBSCRIPTION"); !found {
		subscriptionID = "00000000-0000-0000-0000-000000000000"
	}
	e2eSetup, err = integration.LoadE2ESetupFile(os.Getenv("SETUP_FILEPATH"))
	if err != nil {
		return err
	}

	opts := prepareDevelopmentConf()

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
