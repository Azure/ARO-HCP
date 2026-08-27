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

// RolloutConfig holds the rollout policy. Production values are hardcoded (see
// NewDefaultRolloutConfig); it stays a struct only so tests can exercise the
// pure decision logic with different values.
type RolloutConfig struct {
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

// Hardcoded rollout tuning.
const (
	// DefaultCanaryPercentage upgrades 6% of clusters as canaries first.
	DefaultCanaryPercentage = 6
	// DefaultRollingPercentage upgrades 12% of clusters at a time, chosen to fit
	// ~2h upgrading plus ~1h readiness (3h/cluster) into a single day.
	DefaultRollingPercentage = 12
	// DefaultMinVersionReadyDuration is how long a control plane must hold the
	// achieved version before it counts as successful.
	DefaultMinVersionReadyDuration = 1 * time.Hour
	// DefaultMaxUpgradeDuration is how long a control plane may take to reach its
	// desired version before the cluster counts as failed, used for any minor
	// version without an explicit MaxUpgradeDuration override.
	DefaultMaxUpgradeDuration = 2 * time.Hour
)

// MaxUpgradeDurationForMinor returns the configured maximum upgrade duration for
// the given minor version (e.g. "4.21"), falling back to DefaultMaxUpgradeDuration
// when no per-minor override is configured.
func (c RolloutConfig) MaxUpgradeDurationForMinor(minor string) time.Duration {
	if d, ok := c.MaxUpgradeDuration[minor]; ok {
		return d
	}
	return DefaultMaxUpgradeDuration
}

// NewDefaultRolloutConfig returns the hardcoded production rollout config. The
// per-channel-group z-stream offset is not configured here; it comes from the
// per-cluster desired-version controller's GetZStreamOffset (stable holds one
// z-stream back, other groups take the latest), reused by the best-version selector.
func NewDefaultRolloutConfig() RolloutConfig {
	return RolloutConfig{
		CanaryPercentage:        DefaultCanaryPercentage,
		RollingPercentage:       DefaultRollingPercentage,
		MinVersionReadyDuration: DefaultMinVersionReadyDuration,
		MinimumVersions:         map[string]semver.Version{},
		MaxUpgradeDuration:      map[string]time.Duration{},
	}
}
