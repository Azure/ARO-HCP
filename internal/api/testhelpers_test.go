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

package api

import (
	"testing"
)

// Testing our test resource...

func TestTestResourceVisibilityMap(t *testing.T) {
	expectedVisibility := map[string]VisibilityFlags{
		"ID":                              VisibilityRead,
		"Name":                            VisibilityRead,
		"Type":                            VisibilityRead,
		"SystemData":                      SkipVisibilityTest,
		"SystemData.CreatedBy":            VisibilityRead,
		"SystemData.CreatedByType":        VisibilityRead,
		"SystemData.CreatedAt":            VisibilityRead,
		"SystemData.LastModifiedBy":       VisibilityRead,
		"SystemData.LastModifiedByType":   VisibilityRead,
		"SystemData.LastModifiedAt":       VisibilityRead,
		"Location":                        VisibilityRead | VisibilityCreate,
		"Tags":                            VisibilityRead | VisibilityCreate | VisibilityUpdate,
		"Identity":                        SkipVisibilityTest,
		"Identity.PrincipalID":            VisibilityRead,
		"Identity.TenantID":               VisibilityRead,
		"Identity.Type":                   VisibilityRead | VisibilityCreate | VisibilityUpdate,
		"Identity.UserAssignedIdentities": SkipVisibilityTest,
		"Identity.UserAssignedIdentities.ClientID":    VisibilityRead,
		"Identity.UserAssignedIdentities.PrincipalID": VisibilityRead,
	}

	TestVersionedVisibilityMap[ExternalTestResource](t, testResourceVisibilityMap, expectedVisibility)
}
