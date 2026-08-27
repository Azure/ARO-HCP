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

package versionrollout

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
)

// clusterMinor returns the minor version that places a cluster in a rollout: the
// desired version's minor when set, otherwise the earliest active version's
// minor. The boolean is false when neither is known.
func clusterMinor(serviceProviderCluster *coreapi.ServiceProviderCluster) (string, bool) {
	if desired := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion; desired != nil {
		return minorString(*desired), true
	}
	if active := earliestActiveVersion(serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions); active != nil {
		return minorString(*active), true
	}
	return "", false
}

// serviceProviderClustersForChannel returns every ServiceProviderCluster that
// belongs to the given y-stream channel: its cluster's channel group matches and
// its effective minor (see clusterMinor) equals the channel's minor. Clusters
// whose backing HCPOpenShiftCluster is gone, or which have no channel group, are
// not matched (there is no default channel group).
func serviceProviderClustersForChannel(ctx context.Context, serviceProviderClusterLister corelisters.ServiceProviderClusterLister, clusterLister corelisters.ClusterLister, yStreamChannel string) ([]*coreapi.ServiceProviderCluster, error) {
	channelGroup, minor, ok := parseYStreamChannel(yStreamChannel)
	if !ok {
		return nil, fmt.Errorf("invalid y-stream channel %q", yStreamChannel)
	}

	clusters, err := clusterLister.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list Clusters: %w", err)
	}
	// A cluster with no channel group is not defaulted to any group; it simply
	// matches no rollout channel.
	channelGroupByClusterID := make(map[string]string, len(clusters))
	for _, cluster := range clusters {
		if cluster.ID == nil {
			continue
		}
		channelGroupByClusterID[strings.ToLower(cluster.ID.String())] = cluster.CustomerProperties.Version.ChannelGroup
	}

	serviceProviderClusters, err := serviceProviderClusterLister.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list ServiceProviderClusters: %w", err)
	}

	var matched []*coreapi.ServiceProviderCluster
	for _, serviceProviderCluster := range serviceProviderClusters {
		clusterMinorVersion, ok := clusterMinor(serviceProviderCluster)
		if !ok || clusterMinorVersion != minor {
			continue
		}
		if serviceProviderCluster.ResourceID == nil || serviceProviderCluster.ResourceID.Parent == nil {
			continue
		}
		clusterChannelGroup, ok := channelGroupByClusterID[strings.ToLower(serviceProviderCluster.ResourceID.Parent.String())]
		if !ok || clusterChannelGroup != channelGroup {
			continue
		}
		matched = append(matched, serviceProviderCluster)
	}
	return matched, nil
}
