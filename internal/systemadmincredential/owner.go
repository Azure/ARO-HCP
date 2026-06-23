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

package systemadmincredential

import (
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const (
	// OwnerAnnotationKey is the annotation key set on every k8s object
	// delivered to a management cluster via ApplyDesire. Its value is
	// the lowercased ARM resource ID of the owning ARO-HCP resource.
	OwnerAnnotationKey = "aro-hcp.openshift.io/owner"
)

// requireOwner panics if owner is nil. Every Build* helper must call
// this so a missing owner is a programming error caught in tests.
func requireOwner(owner *azcorearm.ResourceID) {
	if owner == nil {
		panic("systemadmincredential: owner resource ID must not be nil")
	}
}

// ownerAnnotation returns the owner annotation map for the given
// resource ID.
func ownerAnnotation(owner *azcorearm.ResourceID) map[string]string {
	return map[string]string{
		OwnerAnnotationKey: strings.ToLower(owner.String()),
	}
}
