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

package providers

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// Supported provider names. Add new providers here and in New().
const (
	ProviderAzureIstio = "azure-istio"
	// Future: ProviderAzureAKS = "azure-aks"
	// Future: ProviderAzureGrafana = "azure-grafana"
)

// New constructs the VersionProvider for a component's configured provider name.
// Each component resolves its own provider so Istio, AKS, and Grafana can coexist.
func New(providerName, subscriptionID string, credential azcore.TokenCredential) (VersionProvider, error) {
	switch providerName {
	case ProviderAzureIstio:
		return NewIstioProvider(subscriptionID, credential)
	default:
		return nil, fmt.Errorf("unsupported provider %q", providerName)
	}
}
