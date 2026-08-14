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

// defaultChannelGroup is used when a cluster does not specify a channel group.
const defaultChannelGroup = "stable"

// clusterMinor returns the minor version that places a cluster in a rollout: the
// desired version's minor when set, otherwise the earliest active version's
// minor. The boolean is false when neither is known.
func clusterMinor(spc *coreapi.ServiceProviderCluster) (string, bool) {
	if d := desiredVersion(spc); d != nil {
		return minorString(*d), true
	}
	if a := earliestActiveVersion(spc.Status.ControlPlaneVersion.ActiveVersions); a != nil {
		return minorString(*a), true
	}
	return "", false
}

// serviceProviderClustersForChannel returns every ServiceProviderCluster that
// belongs to the given y-stream channel: its cluster's channel group matches and
// its effective minor (see clusterMinor) equals the channel's minor. Clusters
// whose backing HCPOpenShiftCluster is gone are skipped.
func serviceProviderClustersForChannel(ctx context.Context, spcLister corelisters.ServiceProviderClusterLister, clusterLister corelisters.ClusterLister, channel string) ([]*coreapi.ServiceProviderCluster, error) {
	channelGroup, minor, ok := parseYStreamChannel(channel)
	if !ok {
		return nil, fmt.Errorf("invalid y-stream channel %q", channel)
	}

	clusters, err := clusterLister.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list Clusters: %w", err)
	}
	groupByClusterID := make(map[string]string, len(clusters))
	for _, cl := range clusters {
		if cl.ID == nil {
			continue
		}
		cg := cl.CustomerProperties.Version.ChannelGroup
		if cg == "" {
			cg = defaultChannelGroup
		}
		groupByClusterID[strings.ToLower(cl.ID.String())] = cg
	}

	spcs, err := spcLister.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list ServiceProviderClusters: %w", err)
	}

	var out []*coreapi.ServiceProviderCluster
	for _, spc := range spcs {
		m, ok := clusterMinor(spc)
		if !ok || m != minor {
			continue
		}
		if spc.ResourceID == nil || spc.ResourceID.Parent == nil {
			continue
		}
		cg, ok := groupByClusterID[strings.ToLower(spc.ResourceID.Parent.String())]
		if !ok {
			continue // backing cluster is gone
		}
		if cg != channelGroup {
			continue
		}
		out = append(out, spc)
	}
	return out, nil
}
