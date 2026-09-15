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
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeployments"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// Match the API version used by armcontainerservice/v8 v8.2.0. The deployment
// API version is independent of the resource API versions in its template.
const aksAPIVersion = "2025-10-01"

func (o *validatedOptions) buildDeployment(existing *armcontainerservice.ManagedCluster, pools []compute.Pool) (armdeployments.Deployment, []string, error) {
	// Locate the system pool, which is created inline with the cluster resource.
	bootstrapIndex := slices.IndexFunc(pools, func(pool compute.Pool) bool { return pool.Role == compute.PoolRoleSystem })
	if bootstrapIndex < 0 {
		return armdeployments.Deployment{}, nil, fmt.Errorf("no system pool was configured")
	}

	// Compute the desired cluster spec and the set of changed paths.
	cluster, changed, err := o.desiredClusterSpec(existing, &pools[bootstrapIndex])
	if err != nil {
		return armdeployments.Deployment{}, nil, err
	}

	// Emit the cluster resource only when a deployment is actually needed:
	// even an identical AKS PUT starts reconciliation, so only deploy a
	// cluster for configuration changes or recovery of a failed operation.
	resources := make([]map[string]any, 0, len(pools)+1)
	var clusterDependency []string
	if existing == nil || len(changed) > 0 || ptr.Deref(existing.Properties.ProvisioningState, "") != provisioningStateSucceeded {
		// These observation fields are not part of a template resource definition.
		cluster.ID, cluster.Name, cluster.Type, cluster.ETag, cluster.SystemData = nil, nil, nil, nil, nil
		clusterResource, err := deploymentResource(cluster, "Microsoft.ContainerService/managedClusters", o.clusterName)
		if err != nil {
			return armdeployments.Deployment{}, nil, err
		}
		resources = append(resources, clusterResource)
		clusterDependency = []string{fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s", o.subscriptionID, o.resourceGroup, o.clusterName)}
	}

	// Emit an agent pool resource for every pool not already inlined above.
	network := compute.NetworkConfig{VnetSubnetID: o.nodeSubnetID, PodSubnetID: o.podSubnetID}
	for i, pool := range pools {
		// The bootstrap pool is already created inline. On updates, the cluster
		// omits inline pools and every configured pool is a child resource.
		if existing == nil && i == bootstrapIndex {
			continue
		}
		resource, err := deploymentResource(armcontainerservice.AgentPool{Properties: agentpoolspec.Build(pool, network)}, "Microsoft.ContainerService/managedClusters/agentPools", o.clusterName+"/"+pool.Name)
		if err != nil {
			return armdeployments.Deployment{}, nil, err
		}
		if len(clusterDependency) > 0 {
			resource["dependsOn"] = clusterDependency
		}
		resources = append(resources, resource)
	}

	// Assemble the ARM template for an incremental deployment.
	return armdeployments.Deployment{
		Properties: &armdeployments.DeploymentProperties{
			Mode: ptr.To(armdeployments.DeploymentModeIncremental),
			Template: map[string]any{
				"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
				"contentVersion": "1.0.0.0",
				"resources":      resources,
			},
		},
	}, changed, nil
}

// Use the SDK's wire representation instead of maintaining a second resource schema.
func deploymentResource(spec any, resourceType, name string) (map[string]any, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("encoding resource %s: %w", name, err)
	}
	var resource map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&resource); err != nil {
		return nil, fmt.Errorf("decoding resource %s: %w", name, err)
	}
	resource["type"] = resourceType
	resource["apiVersion"] = aksAPIVersion
	resource["name"] = name
	return resource, nil
}
