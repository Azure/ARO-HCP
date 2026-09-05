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
	"strings"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// provisioningTagKey marks a ManagedCluster as mid-provisioning by this tool.
// Set in the initial create/update, cleared once the cluster and all its pools
// exist. A pipeline retry that finds the tag still set resumes from wherever
// it left off; a retry that finds the tag already gone treats the run as a
// no-op success.
const (
	provisioningTagKey   = "aro-hcp-provisioning"
	provisioningTagValue = "true"
)

func hasProvisioningTag(tags map[string]*string) bool {
	v, ok := tags[provisioningTagKey]
	return ok && v != nil && *v == provisioningTagValue
}

// provisioningTags builds the tag set for the cluster PUT. Configured tags and
// the provisioning marker are applied while stored capacity baselines survive
// retries. Fleet updates those baselines after observing configuration convergence.
func provisioningTags(o *validatedOptions, existing *armcontainerservice.ManagedCluster) map[string]*string {
	tags := armTags(o.clusterTags)
	tags[provisioningTagKey] = ptr.To(provisioningTagValue)
	if existing != nil {
		for key, value := range existing.Tags {
			if strings.HasPrefix(strings.ToLower(key), agentpools.CapacityTagPrefix) {
				tags[key] = value
			}
		}
	}
	return tags
}

// finalizeClusterTags records any missing capacity baseline before marking
// provisioning complete. A failed baseline write leaves the marker in place.
func finalizeClusterTags(ctx context.Context, o *completedOptions) error {
	if err := initializeCapacityTags(ctx, o); err != nil {
		return fmt.Errorf("initializing capacity baseline: %w", err)
	}

	if err := removeProvisioningTag(ctx, o.clustersClient, o.resourceGroup, o.clusterName); err != nil {
		return fmt.Errorf("removing provisioning tag: %w", err)
	}

	return nil
}

// initializeCapacityTags adopts the observed pool ceilings. It never uses a
// newly calculated desired plan, which may be smaller than the existing pools.
// Existing role baselines are preserved, including on pipeline retries.
func initializeCapacityTags(ctx context.Context, o *completedOptions) error {
	cluster, err := o.clustersClient.Get(ctx, o.resourceGroup, o.clusterName, nil)
	if err != nil {
		return err
	}
	baseline, err := agentpools.ReadCapacityTags(cluster.Tags)
	if err != nil {
		return err
	}
	if len(baseline) == len(compute.CapacityRoles) {
		return nil
	}
	if cluster.Properties == nil || ptr.Deref(cluster.Properties.ProvisioningState, "") != "Succeeded" {
		return fmt.Errorf("cluster must be successfully provisioned before adopting capacity")
	}
	var pools []armcontainerservice.AgentPool
	pager := o.poolsClient.NewListPager(o.resourceGroup, o.clusterName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, pool := range page.Value {
			if pool == nil {
				continue
			}
			if pool.Properties == nil || ptr.Deref(pool.Properties.ProvisioningState, "") != "Succeeded" {
				return fmt.Errorf("pool %q must be successfully provisioned before adopting capacity", ptr.Deref(pool.Name, ""))
			}
			pools = append(pools, *pool)
		}
	}
	metadata, err := o.skuCache.SKUMetadataByVMSize(ctx, o.subscriptionID)
	if err != nil {
		return err
	}
	capacities, err := agentpools.ObservedPoolCapacities(pools, metadata)
	if err != nil {
		return err
	}
	if capacities[compute.PoolRoleSystem].VCPUs == 0 {
		return fmt.Errorf("cannot adopt capacity without a managed system pool")
	}
	for role := range baseline {
		delete(capacities, role)
	}
	return agentpools.WriteCapacityTags(ctx, o.clustersClient, o.resourceGroup, o.clusterName, cluster.ManagedCluster, capacities)
}

// armTags converts a plain tag map into the ARM pointer-map shape.
func armTags(tags map[string]string) map[string]*string {
	result := make(map[string]*string, len(tags))
	for k, v := range tags {
		result[k] = ptr.To(v)
	}
	return result
}

// removeProvisioningTag marks the cluster as fully provisioned by clearing the
// provisioning marker. ARM's tags update replaces the whole map, so it reads the
// live tags and etag, drops only the provisioning key, and sends the rest back
// under If-Match. This preserves any tags added out-of-band during provisioning
// and fails rather than clobbering a concurrent tag writer.
func removeProvisioningTag(ctx context.Context, client *armcontainerservice.ManagedClustersClient, resourceGroup, clusterName string) error {
	resp, err := client.Get(ctx, resourceGroup, clusterName, nil)
	if err != nil {
		return fmt.Errorf("reading cluster tags: %w", err)
	}
	tags := resp.Tags
	if _, ok := tags[provisioningTagKey]; !ok {
		return nil
	}
	delete(tags, provisioningTagKey)
	poller, err := client.BeginUpdateTags(ctx, resourceGroup, clusterName,
		armcontainerservice.TagsObject{Tags: tags},
		&armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions{IfMatch: resp.ETag})
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}
