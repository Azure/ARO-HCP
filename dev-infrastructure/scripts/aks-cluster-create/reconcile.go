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

package main

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// ensureManagedCluster uses one conditional PUT path for creation, drift
// correction, and recovery of failed updates.
func (o *completedOptions) ensureManagedCluster(ctx context.Context, existing *armcontainerservice.ManagedCluster, bootstrap *compute.Pool, logger logr.Logger) (*armcontainerservice.ManagedCluster, error) {
	spec, changed, err := buildClusterSpec(o.validatedOptions, existing, bootstrap)
	if err != nil {
		return nil, err
	}
	options := &armcontainerservice.ManagedClustersClientBeginCreateOrUpdateOptions{}
	if existing == nil {
		options.IfNoneMatch = ptr.To("*")
		logger.Info("creating AKS cluster", "bootstrapPool", bootstrap.Name, "bootstrapVMSize", bootstrap.Spec.Size)
	} else {
		if len(changed) == 0 && ptr.Deref(existing.Properties.ProvisioningState, "") == "Succeeded" {
			logger.Info("cluster configuration is up to date")
			return existing, nil
		}
		if existing.ETag == nil || *existing.ETag == "" {
			return nil, fmt.Errorf("cluster ETag is missing")
		}
		options.IfMatch = existing.ETag
		logger.Info("updating cluster configuration", "fields", changed)
	}
	poller, err := o.clustersClient.BeginCreateOrUpdate(ctx, o.resourceGroup, o.clusterName, spec, options)
	if err != nil {
		return nil, fmt.Errorf("ensuring cluster: %w", err)
	}
	result, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("waiting for cluster: %w", err)
	}
	return &result.ManagedCluster, nil
}
