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

package versionrollout

import (
	"context"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
)

// fleetRolloutWriter adapts the fleet Cosmos CRUD to the RolloutWriter interface
// the syncers depend on (dropping the ItemOptions argument the controllers do not
// need).
type fleetRolloutWriter struct {
	crud cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout]
}

// NewFleetRolloutWriter returns a RolloutWriter backed by the fleet DB client.
func NewFleetRolloutWriter(fleetDBClient fleetcosmosstorage.FleetDBClient) RolloutWriter {
	return &fleetRolloutWriter{crud: fleetDBClient.ControlPlaneVersionRollouts()}
}

func (w *fleetRolloutWriter) Replace(ctx context.Context, newRollout, oldRollout *fleetapi.ControlPlaneVersionRollout) (*fleetapi.ControlPlaneVersionRollout, error) {
	return w.crud.Replace(ctx, newRollout, oldRollout, nil)
}
