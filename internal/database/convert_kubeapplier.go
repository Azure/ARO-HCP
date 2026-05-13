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

package database

import (
	"fmt"
	"strings"

	kubeapplierapi "github.com/Azure/ARO-HCP/internal/apis/kubeapplier"
	metaapi "github.com/Azure/ARO-HCP/internal/apis/meta"
)

// InternalToCosmosKubeApplier wraps a *Desire in a GenericDocument envelope whose
// partitionKey is the management cluster name rather than the subscription ID.
// The kube-applier container is partitioned this way for credential isolation.
func InternalToCosmosKubeApplier[InternalAPIType any](
	internalObj *InternalAPIType,
) (*GenericDocument[InternalAPIType], error) {
	if internalObj == nil {
		return nil, nil
	}

	metadata, ok := any(internalObj).(metaapi.CosmosMetadataAccessor)
	if !ok {
		return nil, fmt.Errorf("internalObj must be an metaapi.CosmosMetadataAccessor: %T", internalObj)
	}
	mgmtAccessor, ok := any(internalObj).(kubeapplierapi.ManagementClusterAccessor)
	if !ok {
		return nil, fmt.Errorf("internalObj must be a kubeapplierapi.ManagementClusterAccessor: %T", internalObj)
	}
	mgmtCluster := mgmtAccessor.GetManagementCluster()
	if len(mgmtCluster) == 0 {
		return nil, fmt.Errorf("kube-applier object %T is missing spec.managementCluster", internalObj)
	}

	return &GenericDocument[InternalAPIType]{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: metadata.GetCosmosUID(),
			},
			PartitionKey: strings.ToLower(mgmtCluster),
			ResourceID:   metadata.GetResourceID(),
			ResourceType: metadata.GetResourceID().ResourceType.String(),
		},
		Content: *internalObj,
	}, nil
}
