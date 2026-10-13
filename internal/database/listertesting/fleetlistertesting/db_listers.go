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

package fleetlistertesting

import (
	"context"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/listertestingutils"
)

// DBControlPlaneVersionRolloutLister implements
// fleetlisters.ControlPlaneVersionRolloutLister backed by a
// fleetcosmosstorage.FleetDBClient (e.g. the fleet mock). Reads go through the
// backing store so the returned objects carry the store's current etags, which
// lets a controller's read-modify-Replace cycle round-trip against the same mock.
type DBControlPlaneVersionRolloutLister struct {
	FleetDBClient fleetcosmosstorage.FleetDBClient
}

var _ fleetlisters.ControlPlaneVersionRolloutLister = &DBControlPlaneVersionRolloutLister{}

func (l *DBControlPlaneVersionRolloutLister) List(ctx context.Context) ([]*fleetapi.ControlPlaneVersionRollout, error) {
	iter, err := l.FleetDBClient.GlobalListers().ControlPlaneVersionRollouts().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

func (l *DBControlPlaneVersionRolloutLister) Get(ctx context.Context, ystreamChannel string) (*fleetapi.ControlPlaneVersionRollout, error) {
	return l.FleetDBClient.ControlPlaneVersionRollouts().Get(ctx, ystreamChannel)
}
