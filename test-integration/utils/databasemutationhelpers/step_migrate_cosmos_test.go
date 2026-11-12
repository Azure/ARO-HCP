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

package databasemutationhelpers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

func TestMigrateCosmosStepIncludesFleetWithoutSubscriptions(t *testing.T) {
	info, err := integrationutils.NewMockCosmosFromTestingEnv(t.Context(), t)
	require.NoError(t, err)
	input := NewCosmosStepInput(info)
	db := input.FleetDBClient.(*fleetcosmosstoragetesting.MockFleetDBClient)
	id, err := fleetapihelpers.ToControlPlaneVersionRolloutResourceID("fast-4.21")
	require.NoError(t, err)
	before, err := db.ControlPlaneVersionRollouts().Create(t.Context(), &fleetapi.ControlPlaneVersionRollout{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: strings.ToLower(coreapi.ProviderNamespace)},
		Spec: fleetapi.ControlPlaneVersionRolloutSpec{
			Version: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "fast"},
		},
	}, nil)
	require.NoError(t, err)
	data, exists := db.GetDocument(before.GetCosmosUID())
	require.True(t, exists)
	var doc cosmosstorageutils.GenericDocument[fleetapi.ControlPlaneVersionRollout]
	require.NoError(t, json.Unmarshal(data, &doc))
	doc.Content.Spec.Version = coreapi.VersionProfile{}
	data, err = json.Marshal(doc)
	require.NoError(t, err)
	db.StoreDocument(before.GetCosmosUID(), data)

	step, err := newMigrateCosmosStep(NewStepID(0, "migrateCosmos", "fleet"), nil)
	require.NoError(t, err)
	step.RunTest(t.Context(), t, *input)
	data, exists = db.GetDocument(before.GetCosmosUID())
	require.True(t, exists)
	require.NoError(t, json.Unmarshal(data, &doc))
	require.Equal(t, before.Spec, doc.Content.Spec, "migration step must persist the normalized profile in Fleet")
}
