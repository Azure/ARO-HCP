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

package azauth

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

func SetupAzureAuth(ctx context.Context) error {
	if githubAuthSupported() {
		err := setupGithubAzureFederationAuthRefresher(ctx)
		if err != nil {
			return fmt.Errorf("failed to setup GitHub Azure Federation Auth Refresher: %w", err)
		}
	}
	return nil
}

func GetAzureTokenCredentials() (azcore.TokenCredential, error) {
	azCLI, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		return nil, err
	}

	def, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}

	chain, err := azidentity.NewChainedTokenCredential([]azcore.TokenCredential{azCLI, def}, nil)
	if err != nil {
		return nil, err
	}
	return chain, nil
}
