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

package cachedreader

import (
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
)

// FirstPartyApplicationAzureCachedReaders groups Azure cached readers used with the
// First Party Application (FPA) identity. The FPA identity is used to interact with
// customer Azure subscriptions and must not be confused with BackendIdentityAzureCachedReaders.
type FirstPartyApplicationAzureCachedReaders struct {
	// ResourceSKUsCachedReader returns Microsoft.Compute Resource SKUs for virtualMachines
	// with in-memory caching on read paths, authenticated via the FPA identity.
	ResourceSKUsCachedReader ResourceSKUsCachedReader
}

// NewFirstPartyApplicationAzureCachedReaders constructs FPA-scoped cached readers from the
// FPA client builder so the identity used for Azure calls is explicit in the type system.
// location is the Azure location (region) this backend is deployed in, used to scope
// Resource SKUs lookups to that region.
func NewFirstPartyApplicationAzureCachedReaders(
	fpaClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	location string,
) *FirstPartyApplicationAzureCachedReaders {
	return &FirstPartyApplicationAzureCachedReaders{
		ResourceSKUsCachedReader: NewResourceSKUsCachedReader(fpaClientBuilder, location),
	}
}
