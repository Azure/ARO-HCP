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

package informers

import (
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	kubeapplierapi "github.com/Azure/ARO-HCP/internal/apis/kubeapplier"
	metaapi "github.com/Azure/ARO-HCP/internal/apis/meta"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// desireMetadataAccessor narrows the interface set we need from each *Desire
// to power the indexers, without committing the index funcs to a single concrete type.
type desireMetadataAccessor interface {
	metaapi.CosmosMetadataAccessor
	kubeapplierapi.ManagementClusterAccessor
}

// asDesire performs the runtime type assertion to the metadata-accessor
// interface and produces a useful error when an unexpected type is indexed.
func asDesire(obj any) (desireMetadataAccessor, error) {
	d, ok := obj.(desireMetadataAccessor)
	if !ok {
		return nil, utils.TrackError(fmt.Errorf("indexer received unexpected type %T", obj))
	}
	return d, nil
}

// managementClusterIndexFunc returns the lower-cased spec.managementCluster value.
func managementClusterIndexFunc(obj any) ([]string, error) {
	d, err := asDesire(obj)
	if err != nil {
		return nil, err
	}
	mgmt := strings.ToLower(d.GetManagementCluster())
	if len(mgmt) == 0 {
		return nil, nil
	}
	return []string{mgmt}, nil
}

// clusterResourceIDIndexFunc walks a *Desire's resource-ID parent chain to find
// the containing HCPOpenShiftCluster and returns its lower-cased resource ID.
// Both cluster- and node-pool-scoped *Desires produce a key here.
func clusterResourceIDIndexFunc(obj any) ([]string, error) {
	d, err := asDesire(obj)
	if err != nil {
		return nil, err
	}
	id := d.GetResourceID()
	if id == nil {
		return nil, utils.TrackError(fmt.Errorf("desire has nil ResourceID: %T %v", obj, obj))
	}
	clusterID := findAncestorOfType(id, resourcesapi.ClusterResourceType)
	if clusterID == nil {
		return nil, nil
	}
	return []string{strings.ToLower(clusterID.String())}, nil
}

// nodePoolResourceIDIndexFunc walks a *Desire's resource-ID parent chain to find
// the containing NodePool. Cluster-scoped *Desires (no NodePool ancestor)
// produce no key.
func nodePoolResourceIDIndexFunc(obj any) ([]string, error) {
	d, err := asDesire(obj)
	if err != nil {
		return nil, err
	}
	id := d.GetResourceID()
	if id == nil {
		return nil, utils.TrackError(fmt.Errorf("desire has nil ResourceID: %T %v", obj, obj))
	}
	npID := findAncestorOfType(id, resourcesapi.NodePoolResourceType)
	if npID == nil {
		return nil, nil
	}
	return []string{strings.ToLower(npID.String())}, nil
}

// findAncestorOfType walks parent pointers up from id and returns the first
// ancestor whose ResourceType matches target (compared case-insensitively on
// both namespace and full nested type). Returns nil if no ancestor matches.
func findAncestorOfType(id *azcorearm.ResourceID, target azcorearm.ResourceType) *azcorearm.ResourceID {
	for cur := id; cur != nil; cur = cur.Parent {
		if strings.EqualFold(cur.ResourceType.Namespace, target.Namespace) &&
			strings.EqualFold(cur.ResourceType.Type, target.Type) {
			return cur
		}
	}
	return nil
}
