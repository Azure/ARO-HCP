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
	"cmp"
	"context"
	"slices"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/versionpolicy"
)

type graphDataClient interface {
	VersionProfiles(context.Context) ([]coreapi.VersionProfile, error)
}

func discoverChannels(profiles []coreapi.VersionProfile) ([]rolloutSeedKey, error) {
	eligible := sets.New[coreapi.VersionProfile]()
	for _, profile := range profiles {
		profile, err := versionpolicy.NormalizeProfile(profile)
		if err != nil {
			return nil, err
		}
		if versionpolicy.AtLeast(profile.ID, versionpolicy.MinimumBackendVersion) {
			eligible.Insert(profile)
		}
	}
	profiles = eligible.UnsortedList()
	slices.SortFunc(profiles, func(a, b coreapi.VersionProfile) int {
		if order := cmp.Compare(a.ChannelGroup, b.ChannelGroup); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	var keys []rolloutSeedKey
	for _, profile := range profiles {
		keys = append(keys, rolloutSeedKey{Version: profile})
	}
	return keys, nil
}

func (c *rolloutSeedingSyncer) discover(ctx context.Context) error {
	profiles, err := c.discovery.VersionProfiles(ctx)
	if err != nil {
		return err
	}
	keys, err := discoverChannels(profiles)
	if err != nil {
		return err
	}
	for _, key := range keys {
		c.queue.Enqueue(key)
	}
	return nil
}
