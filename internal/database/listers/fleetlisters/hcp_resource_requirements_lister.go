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
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/listers/listerutils"
)

// HCPResourceRequirementsLister lists HCP resource requirements from an informer's
// indexer and gets the fleet-wide singleton named "default".
type HCPResourceRequirementsLister interface {
	List(ctx context.Context) ([]*fleetapi.HCPResourceRequirements, error)
	Get(ctx context.Context) (*fleetapi.HCPResourceRequirements, error)
}

type informerBasedHCPResourceRequirementsLister struct {
	indexer cache.Indexer
}

// NewHCPResourceRequirementsLister creates an HCPResourceRequirementsLister
// from a SharedIndexInformer's indexer.
func NewHCPResourceRequirementsLister(indexer cache.Indexer) HCPResourceRequirementsLister {
	return &informerBasedHCPResourceRequirementsLister{
		indexer: indexer,
	}
}

func (lister *informerBasedHCPResourceRequirementsLister) List(ctx context.Context) ([]*fleetapi.HCPResourceRequirements, error) {
	return listerutils.ListAll[fleetapi.HCPResourceRequirements](lister.indexer)
}

func (lister *informerBasedHCPResourceRequirementsLister) Get(ctx context.Context) (*fleetapi.HCPResourceRequirements, error) {
	key := fleetapihelpers.ToHCPResourceRequirementsResourceIDString(fleetapi.HCPResourceRequirementsResourceName)
	return listerutils.GetByKey[fleetapi.HCPResourceRequirements](lister.indexer, key)
}
