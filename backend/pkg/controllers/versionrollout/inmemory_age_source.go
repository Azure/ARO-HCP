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
	"strings"
	"sync"
	"time"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// inMemoryVersionAgeSource tracks, in process memory, how long each cluster has
// been mismatched (desired set but not achieved) or has held its achieved
// version. It records the first time it observes a (cluster, version) pair and
// reports the elapsed time since. A restart resets the timers, which only makes
// Failed/Successful detection more conservative (delayed), never wrong.
//
// This avoids persisting a per-cluster version-transition timestamp on the API
// (see the plan's open question); it is appropriate for a dark-launched feature.
type inMemoryVersionAgeSource struct {
	clock utilsclock.PassiveClock

	mu            sync.Mutex
	mismatchSince map[string]versionSince
	achievedSince map[string]versionSince
}

type versionSince struct {
	version string
	since   time.Time
}

var _ VersionAgeSource = (*inMemoryVersionAgeSource)(nil)

// NewInMemoryVersionAgeSource returns an age source backed by process memory.
func NewInMemoryVersionAgeSource(clock utilsclock.PassiveClock) *inMemoryVersionAgeSource {
	return &inMemoryVersionAgeSource{
		clock:         clock,
		mismatchSince: map[string]versionSince{},
		achievedSince: map[string]versionSince{},
	}
}

func (s *inMemoryVersionAgeSource) MismatchAge(spc *coreapi.ServiceProviderCluster) (time.Duration, bool) {
	key := spcKey(spc)
	if key == "" {
		return 0, false
	}
	desired := desiredVersion(spc)
	achieved := earliestActiveVersion(spc.Status.ControlPlaneVersion.ActiveVersions)
	mismatched := desired != nil && (achieved == nil || !achieved.EQ(*desired))

	s.mu.Lock()
	defer s.mu.Unlock()
	if !mismatched {
		delete(s.mismatchSince, key)
		return 0, false
	}
	return s.ageLocked(s.mismatchSince, key, desired.String()), true
}

func (s *inMemoryVersionAgeSource) AchievedAge(spc *coreapi.ServiceProviderCluster) (time.Duration, bool) {
	key := spcKey(spc)
	if key == "" {
		return 0, false
	}
	achieved := earliestActiveVersion(spc.Status.ControlPlaneVersion.ActiveVersions)

	s.mu.Lock()
	defer s.mu.Unlock()
	if achieved == nil {
		delete(s.achievedSince, key)
		return 0, false
	}
	return s.ageLocked(s.achievedSince, key, achieved.String()), true
}

// ageLocked returns the elapsed time since (key, version) was first observed,
// (re)starting the timer when the version changed. Callers must hold s.mu.
func (s *inMemoryVersionAgeSource) ageLocked(m map[string]versionSince, key, version string) time.Duration {
	now := s.clock.Now()
	rec, ok := m[key]
	if !ok || rec.version != version {
		m[key] = versionSince{version: version, since: now}
		return 0
	}
	return now.Sub(rec.since)
}

// spcKey returns a stable key for a ServiceProviderCluster (its lowercased
// resource ID), or "" when it has none.
func spcKey(spc *coreapi.ServiceProviderCluster) string {
	if spc.ResourceID == nil {
		return ""
	}
	return strings.ToLower(spc.ResourceID.String())
}
