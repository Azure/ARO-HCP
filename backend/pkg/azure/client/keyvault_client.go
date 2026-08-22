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

package client

//go:generate $MOCKGEN -typed -source=keyvault_client.go -destination=mock_keyvault_client.go -package client KeyVaultKeysClient

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
)

// KeyVaultKeysClient is the Key Vault Keys data plane client, addressed
// directly by vault DNS name rather than by ARM resource ID. This lets
// callers reach a customer-owned Key Vault without needing to know which
// resource group or subscription it lives in.
type KeyVaultKeysClient interface {
	GetKey(ctx context.Context, name string, version string, options *azkeys.GetKeyOptions) (azkeys.GetKeyResponse, error)
}

var _ KeyVaultKeysClient = (*azkeys.Client)(nil)
