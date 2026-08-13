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

package controllerutils

import (
	"context"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// BuildReadDesire produces the desired-state ReadDesire for a resource.
// The status section is intentionally zero — the kube-applier owns status.
// It is shared by the per-cluster and per-node-pool ReadDesire creators.
func BuildReadDesire(resourceIDString string, managementCluster *azcorearm.ResourceID, target kubeapplierapi.ResourceReference) *kubeapplierapi.ReadDesire {
	resourceID, _ := azcorearm.ParseResourceID(resourceIDString) // resourceIDString is built from helpers and always parses
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: managementCluster,
			TargetItem:        target,
		},
	}
}

// GetExistingReadDesire returns the named ReadDesire from cosmos, or nil
// when the document doesn't exist. Non-NotFound errors are propagated.
func GetExistingReadDesire(
	ctx context.Context, crud cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], name string,
) (*kubeapplierapi.ReadDesire, error) {
	existing, err := crud.Get(ctx, name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ReadDesire: %w", err))
	}
	return existing, nil
}

// ReadDesireNeedsWork reports whether existing matches desired in the
// fields the backend writes (Spec.ManagementCluster, Spec.TargetItem).
// A nil existing means "doesn't exist yet" — work is required.
func ReadDesireNeedsWork(existing, desired *kubeapplierapi.ReadDesire) bool {
	if existing == nil {
		return true
	}
	if !controllerutil.ResourceIDsEqual(existing.Spec.ManagementCluster, desired.Spec.ManagementCluster) {
		return true
	}
	return existing.Spec.TargetItem != desired.Spec.TargetItem
}
