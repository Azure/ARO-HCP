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
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// Supported provider names.
const (
	ProviderAzureIstio = "azure-istio"
)

// Deps gives providers access to shared clients. Each accessor is lazy, so a
// provider that does not call Azure never triggers Azure authentication.
type Deps struct {
	Azure func() (AzureAccess, error)
}

type AzureAccess struct {
	SubscriptionID string
	Credential     azcore.TokenCredential
}

// ComponentSpec is the provider-relevant part of a component's configuration,
// validated when the config file loads.
type ComponentSpec struct {
	Locations []string
	// Constraints maps constraint names to YAML paths in the target file.
	Constraints map[string]string
}

// registration is the single place a provider is wired in: add an entry here
// to make it constructible and to validate its configuration.
type registration struct {
	new      func(Deps) (VersionProvider, error)
	validate func(ComponentSpec) error
}

var registry = map[string]registration{
	ProviderAzureIstio: {new: newIstioFromDeps, validate: validateIstioSpec},
}

// New constructs the VersionProvider registered under providerName.
func New(providerName string, deps Deps) (VersionProvider, error) {
	reg, ok := registry[providerName]
	if !ok {
		return nil, unsupported(providerName)
	}
	return reg.new(deps)
}

// Validate checks provider-specific configuration. Unknown providers fail
// so typos are caught at load time.
func Validate(providerName string, spec ComponentSpec) error {
	reg, ok := registry[providerName]
	if !ok {
		return unsupported(providerName)
	}
	return reg.validate(spec)
}

func unsupported(providerName string) error {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return fmt.Errorf("unsupported provider %q (supported: %s)", providerName, strings.Join(names, ", "))
}

func (d Deps) azure() (AzureAccess, error) {
	if d.Azure == nil {
		return AzureAccess{}, errors.New("azure access is not configured")
	}
	return d.Azure()
}
