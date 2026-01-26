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

package azure

import (
	"fmt"

	"github.com/Azure/ARO-Tools/pkg/cmdutils"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// GetAzureTokenCredentials obtains Azure token credentials using the same
// mechanism as hcpctl. This function wraps cmdutils.GetAzureTokenCredentials()
// to provide a consistent interface for Azure authentication.
func GetAzureTokenCredentials() (azcore.TokenCredential, error) {
	cred, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}
	return cred, nil
}
