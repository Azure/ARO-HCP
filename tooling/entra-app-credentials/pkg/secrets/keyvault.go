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

package secrets

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

type azureSecretClient interface {
	GetSecret(ctx context.Context, name, version string, options *azsecrets.GetSecretOptions) (azsecrets.GetSecretResponse, error)
	SetSecret(ctx context.Context, name string, parameters azsecrets.SetSecretParameters, options *azsecrets.SetSecretOptions) (azsecrets.SetSecretResponse, error)
}

type keyVaultStore struct {
	client azureSecretClient
}

// NewKeyVaultStore adapts the Azure Key Vault secrets client.
func NewKeyVaultStore(client azureSecretClient) SecretStore {
	return &keyVaultStore{client: client}
}

func (s *keyVaultStore) Get(ctx context.Context, name string) (string, bool, error) {
	response, err := s.client.GetSecret(ctx, name, "", nil)
	var responseErr *azcore.ResponseError
	switch {
	case errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound:
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("get secret: %w", err)
	case response.Value == nil:
		return "", false, fmt.Errorf("secret %q exists but has no value", name)
	}
	return *response.Value, true, nil
}

func (s *keyVaultStore) Set(ctx context.Context, name, value string) error {
	_, err := s.client.SetSecret(ctx, name, azsecrets.SetSecretParameters{Value: &value}, nil)
	if err != nil {
		return fmt.Errorf("set secret: %w", err)
	}
	return nil
}
