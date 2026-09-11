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

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterMinor returns the rollout minor from the earliest active version, then
// the desired version, then the backing cluster's requested version ID.
// The boolean is false when none of those versions can be determined.
func clusterMinor(serviceProviderCluster *coreapi.ServiceProviderCluster, cluster *coreapi.HCPOpenShiftCluster) (string, bool) {
	if active := earliestActiveVersion(serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions); active != nil {
		return minorString(*active), true
	}
	if desired := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion; desired != nil {
		return minorString(*desired), true
	}
	if cluster == nil {
		return "", false
	}
	requested, err := semver.ParseTolerant(cluster.CustomerProperties.Version.ID)
	if err != nil {
		return "", false
	}
	return minorString(requested), true
}

// serviceProviderClustersForChannel returns every ServiceProviderCluster that
// belongs to the given y-stream channel: its cluster's channel group matches and
// its effective minor (see clusterMinor) equals the channel's minor. Clusters
// whose backing HCPOpenShiftCluster is gone, or which have no channel group, are
// not matched (there is no default channel group).
func serviceProviderClustersForChannel(ctx context.Context, serviceProviderClusterLister corelisters.ServiceProviderClusterLister, clusterLister corelisters.ClusterLister, yStreamChannel string) ([]*coreapi.ServiceProviderCluster, error) {
	logger := utils.LoggerFromContext(ctx).WithValues("ystreamChannel", yStreamChannel)
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
	clustersByID := make(map[string]*coreapi.HCPOpenShiftCluster, len(clusters))
	for _, cluster := range clusters {
		if cluster.ID == nil {
			continue
		}
		clustersByID[strings.ToLower(cluster.ID.String())] = cluster
	}

	serviceProviderClusters, err := serviceProviderClusterLister.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list ServiceProviderClusters: %w", err)
	}

	logger.Info("Matching clusters to rollout channel", "clusterCount", len(clusters), "serviceProviderClusterCount", len(serviceProviderClusters))
	var matched []*coreapi.ServiceProviderCluster
	for _, serviceProviderCluster := range serviceProviderClusters {
		if serviceProviderCluster.ResourceID == nil || serviceProviderCluster.ResourceID.Parent == nil {
			logger.Info("Excluding cluster from rollout: missing cluster resource ID", "resourceID", serviceProviderCluster.ResourceID)
			continue
		}
		cluster, ok := clustersByID[strings.ToLower(serviceProviderCluster.ResourceID.Parent.String())]
		clusterChannelGroup := ""
		if ok {
			clusterChannelGroup = cluster.CustomerProperties.Version.ChannelGroup
		}
		if !ok || clusterChannelGroup != channelGroup {
			logger.Info("Excluding cluster from rollout: backing cluster missing or channel group does not match", "resourceID", serviceProviderCluster.ResourceID, "clusterFound", ok, "clusterChannelGroup", clusterChannelGroup, "channelGroup", channelGroup)
			continue
		}
		clusterMinorVersion, ok := clusterMinor(serviceProviderCluster, cluster)
		if !ok || clusterMinorVersion != minor {
			logger.Info("Excluding cluster from rollout: minor version unknown or does not match", "resourceID", serviceProviderCluster.ResourceID, "requestedVersion", cluster.CustomerProperties.Version.ID, "minorKnown", ok, "clusterMinor", clusterMinorVersion, "channelMinor", minor, "desiredVersion", versionString(serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion), "activeVersion", versionString(earliestActiveVersion(serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions)))
			continue
		}
		logger.Info("Including cluster in rollout channel", "resourceID", serviceProviderCluster.ResourceID)
		matched = append(matched, serviceProviderCluster)
	}
	return matched, nil
}
