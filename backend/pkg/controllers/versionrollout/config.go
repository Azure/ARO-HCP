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
	"time"

	"github.com/blang/semver/v4"
)

// RolloutConfig holds the SRE-tunable rollout policy from the design's
// Code.ControlPlaneUpgradeController.* fields. It is threaded into the
// controllers as a value (later phases add config.yaml/flag plumbing).
type RolloutConfig struct {
	// ZStreamOffset selects the version this many z-streams behind the latest
	// available (e.g. 2 means pick 4.21.6 when 4.21.8 is latest).
	ZStreamOffset int

	// CanaryPercentage is the percent of clusters to upgrade first as canaries.
	CanaryPercentage int

	// RollingPercentage is the percent of clusters upgraded at the same time
	// during the rolling phase.
	RollingPercentage int

	// MinVersionReadyDuration is the minimum time a control plane must be at the
	// achieved version before it counts as successful.
	MinVersionReadyDuration time.Duration

	// MinimumVersions is the SRE floor per y-stream channel (e.g.
	// "stable-4.21" -> 4.21.6). A rollout never selects below its channel floor.
	MinimumVersions map[string]semver.Version

	// MaxUpgradeDuration is the maximum time to wait for a control plane upgrade
	// to a particular minor version before counting the cluster as failed, keyed
	// by minor version (e.g. "4.21" -> 2h).
	MaxUpgradeDuration map[string]time.Duration
}

// Default rollout tuning suggested by the design document.
const (
	DefaultZStreamOffset     = 2
	DefaultCanaryPercentage  = 5
	DefaultRollingPercentage = 5
)

// NewDefaultRolloutConfig returns a RolloutConfig with the design's suggested
// defaults and empty per-channel/per-minor maps.
func NewDefaultRolloutConfig() RolloutConfig {
	return RolloutConfig{
		ZStreamOffset:      DefaultZStreamOffset,
		CanaryPercentage:   DefaultCanaryPercentage,
		RollingPercentage:  DefaultRollingPercentage,
		MinimumVersions:    map[string]semver.Version{},
		MaxUpgradeDuration: map[string]time.Duration{},
	}
}
