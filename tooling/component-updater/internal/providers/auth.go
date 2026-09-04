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

package providers

import (
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// AzureAuthTransport is an http.RoundTripper that injects a Bearer token from
// an azcore.TokenCredential into every request's Authorization header.
type AzureAuthTransport struct {
	Credential azcore.TokenCredential
	Scopes     []string
	Base       http.RoundTripper
}

func (t *AzureAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.Credential.GetToken(req.Context(), policy.TokenRequestOptions{
		Scopes: t.Scopes,
	})
	if err != nil {
		return nil, err
	}

	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+token.Token)

	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

func NewAzureHTTPClient(cred azcore.TokenCredential) *http.Client {
	return &http.Client{
		Transport: &AzureAuthTransport{
			Credential: cred,
			Scopes:     []string{"https://management.azure.com/.default"},
		},
	}
}
