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

package sharedingress

import (
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

// testStampIdentifier is a short (1-3 char) stamp identifier so the mock fleet
// DB's ManagementCluster create validation accepts it.
const testStampIdentifier = "s1"

func testKey() fleetcontrollers.StampKey {
	return fleetcontrollers.StampKey{StampIdentifier: testStampIdentifier}
}

func testManagementClusterResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(fleetapi.ToManagementClusterResourceID(testStampIdentifier))
}
