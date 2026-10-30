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

package istio

import (
	"slices"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
)

type Action string

const (
	ActionInstall           Action = "install"
	ActionUpgrade           Action = "upgrade"
	ActionSkip              Action = "skip"
	ActionResume            Action = "resume"
	ActionCleanupAndUpgrade Action = "cleanup-and-upgrade"
	ActionReconcile         Action = "reconcile"
)

// UpgradeState combines cluster provisioning state with Istio mesh profile
// state to drive the upgrade decision engine.
type UpgradeState struct {
	ClusterName            string
	MeshProfileRevisions   []string
	IstioAvailableUpgrades []string
	KubernetesVersion      string
	ProvisioningState      string
	IstioUpgradeInProgress bool
}

type scenario int

const (
	scenarioNotReady scenario = iota
	scenarioFreshInstall
	scenarioAlreadyAtTarget
	scenarioMidUpgrade
	scenarioTooManyRevisions
	scenarioStaleCanary
	scenarioDowngrade
	scenarioUpgradeAvailable
	scenarioUpgradeUnavailable
)

// classify is a pure function that maps cluster state to a scenario enum. No I/O,
// no side effects. Called once per RunUpgrade invocation to determine what action
// to take. The scenario enum decouples state classification from action selection,
// keeping Decide() a simple switch.
func classify(state UpgradeState, target string) scenario {
	if state.ProvisioningState != "Succeeded" {
		return scenarioNotReady
	}

	// ARM is provisioning a mesh change. If our target is already in the revision
	// list, the canary CP is installed and we can resume post-install. If not, ARM
	// is upgrading to a different revision — skip and let EV2 retry after it finishes.
	// Returning an error here would block the pipeline on someone else's upgrade.
	if state.IstioUpgradeInProgress {
		if slices.Contains(state.MeshProfileRevisions, target) {
			return scenarioMidUpgrade
		}
		return scenarioNotReady
	}

	if len(state.MeshProfileRevisions) == 0 {
		return scenarioFreshInstall
	}

	hasTarget := false
	hasOther := false
	for _, rev := range state.MeshProfileRevisions {
		if rev == target {
			hasTarget = true
		} else {
			hasOther = true
		}
	}

	if hasTarget && !hasOther {
		return scenarioAlreadyAtTarget
	}
	// Two CPs installed, target is one of them. This is the same cluster state as
	// the ARM-in-progress path above, just reached differently: ARM finished the
	// canary provisioning but our post-install didn't complete. Same action —
	// runCanaryPostInstall is idempotent.
	if hasTarget && hasOther {
		return scenarioMidUpgrade
	}

	// AKS supports at most 2 mesh revisions (one stable + one canary). 3+ indicates
	// an AKS bug or data corruption. Automated cleanup could make it worse.
	if len(state.MeshProfileRevisions) > 2 {
		return scenarioTooManyRevisions
	}

	highest := slices.MaxFunc(state.MeshProfileRevisions, compareRevisions)
	if compareRevisions(highest, target) > 0 {
		return scenarioDowngrade
	}

	if !slices.Contains(state.IstioAvailableUpgrades, target) {
		return scenarioUpgradeUnavailable
	}

	// At this point: target is newer than what's installed, available for upgrade,
	// and not yet installed. The only remaining question is whether we start clean
	// (1 revision) or need to clean up a stale canary first (2 revisions).
	if len(state.MeshProfileRevisions) == 1 {
		return scenarioUpgradeAvailable
	}

	// Two revisions with target available — stale canary from a prior failed upgrade.
	return scenarioStaleCanary
}

func Decide(logger logr.Logger, state UpgradeState, target string) Action {
	sc := classify(state, target)
	switch sc {
	case scenarioNotReady:
		if state.IstioUpgradeInProgress {
			logger.Info("Skipping: ARM upgrade to a different revision is in progress, will retry on next pipeline run",
				"provisioningState", state.ProvisioningState,
				"installed", state.MeshProfileRevisions,
				"configTarget", target)
		} else {
			logger.Info("Skipping: cluster provisioning has not succeeded",
				"provisioningState", state.ProvisioningState)
		}
		return ActionSkip
	case scenarioFreshInstall:
		logger.Info("No revisions installed, installing from svc.istio.versions",
			"target", target)
		return ActionInstall
	case scenarioAlreadyAtTarget:
		logger.Info("Already at target -- reconciling expected resource state",
			"target", target)
		return ActionReconcile
	case scenarioMidUpgrade:
		logger.Info("Mid-upgrade detected, resuming",
			"installed", state.MeshProfileRevisions,
			"target", target)
		return ActionResume
	case scenarioTooManyRevisions:
		logger.Info("Unexpected revision count, manual intervention required",
			"revisions", state.MeshProfileRevisions,
			"count", len(state.MeshProfileRevisions))
		return ActionSkip
	case scenarioStaleCanary:
		logger.Info("Stale canary detected, will clean up and upgrade",
			"installed", state.MeshProfileRevisions,
			"target", target)
		return ActionCleanupAndUpgrade
	case scenarioDowngrade:
		highest := slices.MaxFunc(state.MeshProfileRevisions, compareRevisions)
		logger.Info("Downgrade detected -- reconciling installed revision instead of config target",
			"installed", highest,
			"configTarget", target)
		return ActionReconcile
	case scenarioUpgradeAvailable:
		highest := slices.MaxFunc(state.MeshProfileRevisions, compareRevisions)
		logger.Info("Upgrading to svc.istio.versions target",
			"from", highest,
			"to", target,
			"k8sVersion", state.KubernetesVersion)
		return ActionUpgrade
	case scenarioUpgradeUnavailable:
		logger.Info("Target not in available upgrades, skipping",
			"target", target,
			"k8sVersion", state.KubernetesVersion)
		return ActionSkip
	default:
		logger.Info("Unhandled scenario, skipping")
		return ActionSkip
	}
}

// compareRevisions compares AKS Istio revision strings segment-by-segment.
// Format is asm-{major}-{minor}. Numeric segments are compared as integers,
// not lexicographically — so asm-1-9 < asm-1-29 (9 < 29), which string
// comparison would get wrong.
func compareRevisions(a, b string) int {
	aParts := strings.Split(a, "-")
	bParts := strings.Split(b, "-")

	maxLen := max(len(aParts), len(bParts))

	for i := range maxLen {
		var aVal, bVal string
		if i < len(aParts) {
			aVal = aParts[i]
		}
		if i < len(bParts) {
			bVal = bParts[i]
		}

		aNum, aErr := strconv.Atoi(aVal)
		bNum, bErr := strconv.Atoi(bVal)

		if aErr == nil && bErr == nil {
			if aNum < bNum {
				return -1
			}
			if aNum > bNum {
				return 1
			}
			continue
		}

		if aVal < bVal {
			return -1
		}
		if aVal > bVal {
			return 1
		}
	}
	return 0
}
