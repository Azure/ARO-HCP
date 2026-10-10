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

package coreapihelpers

import (
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// IdentityMetadataValueHasResolvedIdentityInformation reports whether value
// has non-empty ClientID, PrincipalID, and TenantID. A non-nil RetrievalError
// is unresolved. An empty value (all fields nil) is also unresolved: that
// source applies but has not been resolved yet or could not be resolved in
// this pass. value must be non-nil.
func IdentityMetadataValueHasResolvedIdentityInformation(value *coreapi.IdentityMetadataValue) bool {
	if value.RetrievalError != nil {
		return false
	}

	return len(ptr.Deref(value.ClientID, "")) > 0 &&
		len(ptr.Deref(value.PrincipalID, "")) > 0 &&
		len(ptr.Deref(value.TenantID, "")) > 0
}
