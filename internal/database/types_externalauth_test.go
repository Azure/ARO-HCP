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

package database

import (
	"math/rand"
	"testing"

	"sigs.k8s.io/randfill"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestExternalAuthJSONRoundTripThroughResourceDocument(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := fuzzerFor([]interface{}{
		func(j *TypedDocument, c randfill.Continue) {
			c.FillNoCustom(j)
			j.ResourceType = api.ExternalAuthResourceType.String()
		},
		func(j *map[string]any, c randfill.Continue) {
		},
		func(j *azcorearm.ResourceID, c randfill.Continue) {
			*j = *api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg"))
		},
		func(j *ocm.InternalID, c randfill.Continue) {
			*j = api.Must(ocm.NewInternalID("/api/clusters_mgmt/v1/clusters/fixed-value"))
		},
	}, rand.NewSource(seed))

	// Try a few times, since runTest uses random values.
	for i := 0; i < 20; i++ {
		original := &ExternalAuth{}
		roundTrippedObj := &ExternalAuth{}
		fuzzer.Fill(original)
		roundTrip(t, original, roundTrippedObj, FilterExternalAuthState)
	}
}
