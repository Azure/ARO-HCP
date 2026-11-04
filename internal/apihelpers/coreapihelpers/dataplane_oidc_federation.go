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
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// IsDataplaneOIDCFederationAssignmentTargetIdentityEnsured reports whether OIDC federation
// for this assignment completed for the current TargetIdentity on the last
// pass that ensured every desired FIC. A draining assignment is not ensured.
// This is not live Azure health: a failed recheck of an already confirmed FIC
// leaves the stamp in place.
//
// EnsuredIdentity is only ever set once every FIC desired as of that pass was
// confirmed, and the executor clears it as soon as a later pass detects new or
// still-outstanding desired FICs (staged into PendingAzureResources) -- before
// it even attempts to create them. So a true result here already implies no
// known outstanding FIC work, without this function needing to separately
// inspect AzureResources/PendingAzureResources itself.
func IsDataplaneOIDCFederationAssignmentTargetIdentityEnsured(status *coreapi.DataplaneOIDCFederationAssignmentStatus) bool {
	return status.DeconfigureTimestamp == nil && status.EnsuredIdentity != nil && *status.EnsuredIdentity == status.TargetIdentity
}
