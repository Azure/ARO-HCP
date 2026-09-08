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
	"maps"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
)

// provisioningTagKey marks a ManagedCluster as mid-provisioning by this tool.
// The value "true" marks active provisioning; removing it commits finalization.
const (
	provisioningTagKey   = "aro-hcp-provisioning"
	provisioningTagValue = "true"
)

func hasProvisioningTag(tags map[string]*string) bool {
	return ptr.Deref(tags[provisioningTagKey], "") == provisioningTagValue
}

// initialClusterTags builds the creation-time tag set: the cluster tags plus
// the provisioning marker that makes this tool own pool provisioning until
// reconcileClusterTags removes it.
func initialClusterTags(clusterTags map[string]string) map[string]*string {
	tags := make(map[string]*string, len(clusterTags)+1)
	for key, value := range clusterTags {
		tags[key] = ptr.To(value)
	}
	tags[provisioningTagKey] = ptr.To(provisioningTagValue)
	return tags
}

// mergeClusterTags clones clusterTags and overlays the configured values.
// Returns the merged map and whether any values changed.
func mergeClusterTags(clusterTags map[string]*string, overlayTags map[string]string) (map[string]*string, bool) {
	merged := maps.Clone(clusterTags)
	if merged == nil {
		merged = map[string]*string{}
	}
	changed := false
	for key, value := range overlayTags {
		if previous, ok := merged[key]; ok && previous != nil && *previous == value {
			continue
		}
		merged[key] = ptr.To(value)
		changed = true
	}
	return merged, changed
}

// reconcileClusterTags overlays the configured cluster tags and clears the
// provisioning marker once the cluster has finished provisioning, handing pool
// management to the controller. The result is written in one conditional tag
// update.
func (o *completedOptions) reconcileClusterTags(ctx context.Context, cluster *armcontainerservice.ManagedCluster) error {
	tags, changed := mergeClusterTags(cluster.Tags, o.clusterTags)

	// Clear the provisioning marker once the cluster and all its pools report
	// success, handing pool management over to the controller.
	needsFinalization := hasProvisioningTag(cluster.Tags)
	if needsFinalization {
		if err := requireProvisioned(cluster); err != nil {
			return err
		}
		delete(tags, provisioningTagKey)
	}
	if !changed && !needsFinalization {
		return nil
	}
	if cluster.ETag == nil || len(*cluster.ETag) == 0 {
		return fmt.Errorf("cluster has no ETag for tag reconciliation")
	}
	poller, err := o.clustersClient.BeginUpdateTags(ctx, o.resourceGroup, o.clusterName,
		armcontainerservice.TagsObject{Tags: tags},
		&armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions{IfMatch: cluster.ETag})
	if err != nil {
		return fmt.Errorf("submitting tag update: %w", err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("polling tag update: %w", err)
	}
	return nil
}

// requireProvisioned rejects finalization until the cluster and every observed
// pool report a successful provisioning state.
func requireProvisioned(cluster *armcontainerservice.ManagedCluster) error {
	if cluster.Properties == nil || ptr.Deref(cluster.Properties.ProvisioningState, "") != provisioningStateSucceeded {
		return fmt.Errorf("cluster must be successfully provisioned before finalizing tags")
	}
	for _, pool := range cluster.Properties.AgentPoolProfiles {
		if pool == nil {
			return fmt.Errorf("cannot finalize tags with a missing pool observation")
		}
		if ptr.Deref(pool.ProvisioningState, "") != provisioningStateSucceeded {
			return fmt.Errorf("pool %q must be successfully provisioned before finalizing tags", ptr.Deref(pool.Name, ""))
		}
	}
	return nil
}
