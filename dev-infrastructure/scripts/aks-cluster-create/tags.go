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
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// provisioningTagKey marks a ManagedCluster as mid-provisioning by this tool.
// Its presence permits pool provisioning; its removal commits finalization.
const (
	provisioningTagKey   = "aro-hcp-provisioning"
	provisioningTagValue = "true"
)

func hasProvisioningTag(tags map[string]*string) bool {
	for key := range tags {
		if strings.EqualFold(key, provisioningTagKey) {
			return true
		}
	}
	return false
}

// initialClusterTags is used only for creation. Reserved lifecycle tags cannot
// be supplied by configuration: baselines must come from observed capacity.
func initialClusterTags(o *validatedOptions) map[string]*string {
	tags := make(map[string]*string, len(o.clusterTags)+1)
	overlayConfiguredTags(tags, o.clusterTags)
	tags[provisioningTagKey] = ptr.To(provisioningTagValue)
	return tags
}

// overlayConfiguredTags preserves live key spelling, since Azure tag keys are
// case insensitive. Unconfigured tags and reserved lifecycle tags are untouched.
func overlayConfiguredTags(tags map[string]*string, configured map[string]string) bool {
	changed := false
	for key, value := range configured {
		if strings.EqualFold(key, provisioningTagKey) || strings.HasPrefix(strings.ToLower(key), agentpools.CapacityTagPrefix) {
			continue
		}
		for liveKey := range tags {
			if strings.EqualFold(key, liveKey) {
				key = liveKey
				break
			}
		}
		if previous, ok := tags[key]; ok && previous != nil && *previous == value {
			continue
		}
		tags[key] = ptr.To(value)
		changed = true
	}
	return changed
}

// reconcileClusterTags atomically overlays configured tags, adopts missing
// baselines, and removes the provisioning marker using the caller's snapshot.
// Existing baselines survive byte-for-byte, regardless of observed capacity.
func (o *completedOptions) reconcileClusterTags(ctx context.Context, cluster *armcontainerservice.ManagedCluster) error {
	baseline, err := agentpools.ReadCapacityTags(cluster.Tags)
	if err != nil {
		return err
	}
	missingBaseline := len(baseline) != len(compute.CapacityRoles)
	provisioning := hasProvisioningTag(cluster.Tags)
	tags := maps.Clone(cluster.Tags)
	if tags == nil {
		tags = map[string]*string{}
	}
	changed := overlayConfiguredTags(tags, o.clusterTags)
	if missingBaseline || provisioning {
		if cluster.Properties == nil || ptr.Deref(cluster.Properties.ProvisioningState, "") != "Succeeded" {
			return fmt.Errorf("cluster must be successfully provisioned before finalizing tags")
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
					return fmt.Errorf("cannot finalize tags with a missing pool observation")
				}
				if pool.Properties == nil || ptr.Deref(pool.Properties.ProvisioningState, "") != "Succeeded" {
					return fmt.Errorf("pool %q must be successfully provisioned before finalizing tags", ptr.Deref(pool.Name, ""))
				}
				if missingBaseline {
					pools = append(pools, *pool)
				}
			}
		}
		if missingBaseline {
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
			for _, role := range compute.CapacityRoles {
				if _, present := baseline[role]; present {
					continue
				}
				value, err := json.Marshal(capacities[role])
				if err != nil {
					return err
				}
				tags[agentpools.CapacityTagPrefix+string(role)] = ptr.To(string(value))
				changed = true
			}
		}
		if provisioning {
			for key := range tags {
				if strings.EqualFold(key, provisioningTagKey) {
					delete(tags, key)
				}
			}
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if cluster.ETag == nil || len(*cluster.ETag) == 0 {
		return fmt.Errorf("cluster has no ETag for tag reconciliation")
	}
	poller, err := o.clustersClient.BeginUpdateTags(ctx, o.resourceGroup, o.clusterName,
		armcontainerservice.TagsObject{Tags: tags},
		&armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions{IfMatch: cluster.ETag})
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}
