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

package informerutils

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-logr/logr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// CosmosSnapshotLogger attaches the same resource identity and snapshot metadata
// to lists and change-feed events. It preserves the caller's context fields and
// derives resource identity per item, rather than from a controller workqueue key.
func CosmosSnapshotLogger(ctx context.Context, container string, obj any, resourceID *azcorearm.ResourceID) logr.Logger {
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddLogValuesForResourceID(resourceID)...)
	if resourceID != nil {
		logger = logger.WithValues("currentResourceID", resourceID.String())
	}
	if container != "" {
		metadata := changeFeedItemObjectMetadata(container, obj, resourceID)
		logger = logger.WithValues("objectMetadata", metadata)
		if metadata.ClusterResourceID != "" {
			logger = logger.WithValues(utils.LogValues{}.AddHCPClusterName(metadata.ClusterResourceID)...)
		}
		if metadata.ResourceGroup != "" {
			logger = logger.WithValues(utils.LogValues{}.AddResourceGroup(metadata.ResourceGroup)...)
		}
	}
	var managementCluster *azcorearm.ResourceID
	switch obj := obj.(type) {
	case *kubeapplierapi.ApplyDesire:
		managementCluster = obj.Spec.ManagementCluster
	case *kubeapplierapi.ReadDesire:
		managementCluster = obj.Spec.ManagementCluster
	}
	if managementCluster != nil {
		logger = logger.WithValues("managementCluster", strings.ToLower(managementCluster.String()))
	}
	return logger
}

// LogCosmosListItem logs a listed object in the stored document shape used by
// change-feed snapshots. Serialization and redaction operate on a detached copy
// so the object delivered to the informer retains its original data. Logging is
// best effort: failure must neither fail the list nor expose unredacted content.
func LogCosmosListItem[T any](ctx context.Context, container string, obj *T) {
	if container == "" {
		return
	}
	metadata, ok := any(obj).(coreapi.CosmosMetadataAccessor)
	if !ok || obj == nil || metadata.GetResourceID() == nil {
		utils.LoggerFromContext(ctx).Error(nil, "cannot snapshot listed document without resource identity")
		return
	}
	logger := CosmosSnapshotLogger(ctx, container, obj, metadata.GetResourceID())
	document, err := cosmosstorageutils.InternalToCosmosGeneric(obj)
	if err != nil {
		logger.Error(err, "failed to serialize listed document for snapshot")
		return
	}
	document.CosmosETag = metadata.GetEtag()
	serialized, err := json.Marshal(document)
	if err != nil {
		logger.Error(err, "failed to serialize listed document for snapshot")
		return
	}
	var loggedDocument cosmosstorageutils.TypedDocument
	if err := json.Unmarshal(serialized, &loggedDocument); err != nil {
		logger.Error(err, "failed to decode listed document for snapshot")
		return
	}
	if err := cosmosstorageutils.RedactTypedDocument(&loggedDocument); err != nil {
		logger.Error(err, "failed to redact listed document for snapshot")
		return
	}
	logger.Info(fmt.Sprintf("dumping resourceID %v", metadata.GetResourceID()),
		"snapshotType", "cosmos",
		"content", &loggedDocument,
	)
}
