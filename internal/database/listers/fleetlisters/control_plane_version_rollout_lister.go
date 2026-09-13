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

package fleetlisters

import (
	"context"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/listerutils"
)

// ControlPlaneVersionRolloutLister lists and gets control-plane version rollouts
// from an informer's indexer. The identifier is the y-stream channel (e.g.
// "stable-4.21"), which is the rollout's top-level resource name / partition key.
type ControlPlaneVersionRolloutLister interface {
	List(ctx context.Context) ([]*fleetapi.ControlPlaneVersionRollout, error)
	Get(ctx context.Context, ystreamChannel string) (*fleetapi.ControlPlaneVersionRollout, error)
}

type informerBasedControlPlaneVersionRolloutLister struct {
	indexer cache.Indexer
}

// NewControlPlaneVersionRolloutLister creates a ControlPlaneVersionRolloutLister
// from a SharedIndexInformer's indexer.
func NewControlPlaneVersionRolloutLister(indexer cache.Indexer) ControlPlaneVersionRolloutLister {
	return &informerBasedControlPlaneVersionRolloutLister{
		indexer: indexer,
	}
}

func (l *informerBasedControlPlaneVersionRolloutLister) List(ctx context.Context) ([]*fleetapi.ControlPlaneVersionRollout, error) {
	return listerutils.ListAll[fleetapi.ControlPlaneVersionRollout](l.indexer)
}

func (l *informerBasedControlPlaneVersionRolloutLister) Get(ctx context.Context, ystreamChannel string) (*fleetapi.ControlPlaneVersionRollout, error) {
	key := fleetapi.ToControlPlaneVersionRolloutResourceIDString(ystreamChannel)
	return listerutils.GetByKey[fleetapi.ControlPlaneVersionRollout](l.indexer, key)
}
