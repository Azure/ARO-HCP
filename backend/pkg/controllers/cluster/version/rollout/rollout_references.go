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
	"github.com/Azure/ARO-HCP/internal/versionpolicy"
)

// collectRolloutReferences identifies the minor version and channel group pairs the
// fleet still depends on for creation, upgrades, pin release, and deletion. Seeding
// uses these dependencies to restore missing rollout state; retirement preserves that state
// until every referring cluster document is gone.
//
// It returns known dependencies alongside errors describing incomplete reference
// information. Known dependencies authorize additive repair, while retirement
// requires an error-free inventory before deleting any rollout.
func collectRolloutReferences(clusterList []*coreapi.Cluster, spcList []*coreapi.ServiceProviderCluster) (sets.Set[coreapi.VersionProfile], error) {
	refs := sets.New[coreapi.VersionProfile]()
	var errs []error
	groups := map[string]string{}
	add := func(group, version string) error {
		profile, err := versionpolicy.NormalizeProfile(coreapi.VersionProfile{ChannelGroup: group, ID: version})
		if err != nil {
			return err
		}
		refs.Insert(profile)
		return nil
	}
	for _, cluster := range clusterList {
		group := cluster.CustomerProperties.Version.ChannelGroup
		if cluster.ID == nil {
			errs = append(errs, fmt.Errorf("cluster without resource ID in rollout reference scan"))
			continue
		}
		groups[strings.ToLower(cluster.ID.String())] = group
		if err := add(group, cluster.CustomerProperties.Version.ID); err != nil {
			errs = append(errs, err)
		}
		if exact := cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion; exact != nil {
			if err := add(group, exact.String()); err != nil {
				errs = append(errs, err)
			}
		}
		for _, active := range cluster.Status.ActiveVersions {
			if err := add(group, active.Version); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for _, spc := range spcList {
		if spc.ResourceID == nil || spc.ResourceID.Parent == nil {
			errs = append(errs, fmt.Errorf("service provider cluster without parent in rollout reference scan"))
			continue
		}
		group, ok := groups[strings.ToLower(spc.ResourceID.Parent.String())]
		if !ok || group == "" {
			// Resolving an SPC's channel group requires its parent cluster.
			// Retirement requires this dependency to be resolved even during deletion.
			errs = append(errs, fmt.Errorf("cannot determine channel group for %s", spc.ResourceID))
			continue
		}
		// Pin release compares UntilExactVersion against the selection in the
		// ExactVersion channel, so that channel carries the pin dependency.
		versions := []*semver.Version{spc.Spec.ControlPlaneVersion.DesiredVersion, spc.Spec.PinnedVersion.ExactVersion}
		for _, active := range spc.Status.ControlPlaneVersion.ActiveVersions {
			versions = append(versions, active.Version)
		}
		for _, version := range versions {
			if version != nil {
				if err := add(group, version.String()); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	return refs, errors.Join(errs...)
}
