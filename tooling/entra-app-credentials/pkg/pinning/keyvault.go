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

package pinning

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

type azureCertificateClient interface {
	GetCertificate(ctx context.Context, name, version string, options *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error)
}

type certificateClient struct {
	client azureCertificateClient
}

// NewCertificateClient adapts the Azure Key Vault SDK client.
func NewCertificateClient(client azureCertificateClient) CertificateClient {
	return &certificateClient{client: client}
}

func (c *certificateClient) GetCertificate(ctx context.Context, name string) ([]byte, error) {
	response, err := c.client.GetCertificate(ctx, name, "", nil)
	if err != nil {
		return nil, fmt.Errorf("get certificate: %w", err)
	}
	return response.CER, nil
}
