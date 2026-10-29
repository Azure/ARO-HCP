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

package rollout

import (
	"errors"
	"fmt"
	"strings"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
)

// collectRolloutReferences counts unique parent clusters per minor version and
// channel group used for creation, upgrades, pin release, and deletion.
//
// It returns known dependencies alongside errors describing incomplete reference
// information. Watches enqueue known dependencies; status collection returns on
// error with all prior rollout status unchanged for queue retry.
func collectRolloutReferences(clusterList []*coreapi.Cluster, serviceProviderClusters []*coreapi.ServiceProviderCluster) (map[coreapi.VersionProfile]int64, error) {
	refs := map[coreapi.VersionProfile]sets.Set[string]{}
	var errs []error
	groups := map[string]string{}
	add := func(clusterID, group, version string) error {
		v, err := semver.ParseTolerant(version)
		if err != nil {
			return fmt.Errorf("invalid version %q: %w", version, err)
		}
		profile, err := fleetapihelpers.RolloutVersionFromName(group + "-" + minorString(v))
		if err != nil {
			return fmt.Errorf("unsupported channel group %q: %w", group, err)
		}
		if refs[profile] == nil {
			refs[profile] = sets.New[string]()
		}
		refs[profile].Insert(clusterID)
		return nil
	}
	for _, cluster := range clusterList {
		group := cluster.CustomerProperties.Version.ChannelGroup
		if cluster.ID == nil {
			errs = append(errs, fmt.Errorf("cluster without resource ID in rollout reference scan"))
			continue
		}
		clusterID := strings.ToLower(cluster.ID.String())
		groups[clusterID] = group
		if err := add(clusterID, group, cluster.CustomerProperties.Version.ID); err != nil {
			errs = append(errs, err)
		}
		if exact := cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion; exact != nil {
			if err := add(clusterID, group, exact.String()); err != nil {
				errs = append(errs, err)
			}
		}
		for _, active := range cluster.Status.ActiveVersions {
			if err := add(clusterID, group, active.Version); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for _, serviceProviderCluster := range serviceProviderClusters {
		if serviceProviderCluster.ResourceID == nil || serviceProviderCluster.ResourceID.Parent == nil {
			errs = append(errs, fmt.Errorf("service provider cluster without parent in rollout reference scan"))
			continue
		}
		clusterID := strings.ToLower(serviceProviderCluster.ResourceID.Parent.String())
		group, ok := groups[clusterID]
		if !ok || group == "" {
			// Resolving a serviceProviderCluster's channel group requires its parent cluster.
			// Reference collection requires this dependency even during deletion.
			errs = append(errs, fmt.Errorf("cannot determine channel group for %s", serviceProviderCluster.ResourceID))
			continue
		}
		// Pin release compares UntilExactVersion against the selection in the
		// ExactVersion channel, so that channel carries the pin dependency.
		versions := []*semver.Version{serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion, serviceProviderCluster.Spec.PinnedVersion.ExactVersion}
		for _, active := range serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions {
			versions = append(versions, active.Version)
		}
		for _, version := range versions {
			if version != nil {
				if err := add(clusterID, group, version.String()); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	counts := make(map[coreapi.VersionProfile]int64, len(refs))
	for profile, clusterIDs := range refs {
		counts[profile] = int64(clusterIDs.Len())
	}
	return counts, errors.Join(errs...)
}
