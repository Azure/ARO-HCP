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

package backendapihelpers

import (
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// IsDataplaneOIDCFederationAssignmentEnsured reports whether OIDC federation
// for this assignment completed for the current TargetIdentity on the last
// pass that ensured every desired FIC. A draining assignment is not ensured.
// This is not live Azure health: a failed recheck of an already confirmed FIC
// leaves the stamp in place.
func IsDataplaneOIDCFederationAssignmentEnsured(status *coreapi.DataplaneOIDCFederationAssignmentStatus) bool {
	return status.DeconfigureTimestamp == nil && status.EnsuredIdentity != nil && *status.EnsuredIdentity == status.TargetIdentity
}

// IsDataplaneOIDCFederationAssignmentsTargetIdentityEnsured reports whether
// every non-draining assignment in dataplaneOIDCFederationAssignments on
// identityResourceID is federated for that assignment's TargetIdentity. False
// when there is no such assignment.
func IsDataplaneOIDCFederationAssignmentsTargetIdentityEnsured(dataplaneOIDCFederationAssignments map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus, identityResourceID string) bool {
	identityResourceID = strings.ToLower(identityResourceID)
	hasDesiredAssignment := false
	for key, status := range dataplaneOIDCFederationAssignments {
		if key.IdentityResourceID != identityResourceID {
			continue
		}
		if status == nil || status.DeconfigureTimestamp != nil {
			continue
		}
		hasDesiredAssignment = true
		if !IsDataplaneOIDCFederationAssignmentEnsured(status) {
			return false
		}
	}
	return hasDesiredAssignment
}
