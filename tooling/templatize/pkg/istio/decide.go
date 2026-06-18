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
)

type Action string

const (
	ActionInstall           Action = "install"
	ActionUpgrade           Action = "upgrade"
	ActionSkip              Action = "skip"
	ActionResume            Action = "resume"
	ActionCleanupAndUpgrade Action = "cleanup-and-upgrade"
)

type ClusterState struct {
	Name              string
	Revisions         []string
	AvailableUpgrades []string
	KubernetesVersion string
	ProvisioningState string
	UpgradeInProgress bool
}

type Decision struct {
	Action Action
	Reason string
}

func Decide(state ClusterState, targetVersion string) Decision {
	cluster := state.Name

	if state.ProvisioningState != "Succeeded" {
		return Decision{
			Action: ActionSkip,
			Reason: cluster + " provisioning state is " + state.ProvisioningState,
		}
	}

	if len(state.Revisions) == 0 {
		return Decision{
			Action: ActionInstall,
			Reason: cluster + " has no revisions installed — installing " + targetVersion + " from svc.istio.versions",
		}
	}

	hasTarget := false
	hasOlder := false
	for _, rev := range state.Revisions {
		if rev == targetVersion {
			hasTarget = true
		} else {
			hasOlder = true
		}
	}

	if hasTarget && !hasOlder {
		return Decision{
			Action: ActionSkip,
			Reason: cluster + " already at svc.istio.versions target " + targetVersion,
		}
	}

	if hasTarget && hasOlder {
		return Decision{
			Action: ActionResume,
			Reason: cluster + " mid-upgrade detected — " + strings.Join(state.Revisions, ", ") + " installed, resuming upgrade to svc.istio.versions target " + targetVersion,
		}
	}

	if len(state.Revisions) > 2 {
		return Decision{
			Action: ActionSkip,
			Reason: cluster + " has " + strconv.Itoa(len(state.Revisions)) + " revisions installed — unexpected state, manual intervention required",
		}
	}

	if len(state.Revisions) > 1 {
		highest := slices.MaxFunc(state.Revisions, compareRevisions)
		if compareRevisions(highest, targetVersion) > 0 {
			return Decision{
				Action: ActionSkip,
				Reason: cluster + " downgrade detected — installed " + highest + " is newer than svc.istio.versions target " + targetVersion,
			}
		}
		return Decision{
			Action: ActionCleanupAndUpgrade,
			Reason: cluster + " stale canary detected — " + strings.Join(state.Revisions, ", ") + " installed but config target is " + targetVersion + " — will clean up " + highest + " and upgrade",
		}
	}

	if state.UpgradeInProgress {
		return Decision{
			Action: ActionResume,
			Reason: cluster + " upgrade already in progress (409 ServiceMeshUpgradeInProgress)",
		}
	}

	highest := slices.MaxFunc(state.Revisions, compareRevisions)
	if compareRevisions(highest, targetVersion) > 0 {
		return Decision{
			Action: ActionSkip,
			Reason: cluster + " downgrade detected — installed " + highest + " is newer than svc.istio.versions target " + targetVersion,
		}
	}

	if slices.Contains(state.AvailableUpgrades, targetVersion) {
		return Decision{
			Action: ActionUpgrade,
			Reason: cluster + " upgrading from " + highest + " → " + targetVersion + " (svc.istio.versions target available, compatible with k8s " + state.KubernetesVersion + ")",
		}
	}

	return Decision{
		Action: ActionSkip,
		Reason: cluster + " svc.istio.versions target " + targetVersion + " is not in available upgrades — may be incompatible with k8s " + state.KubernetesVersion + " or not yet available in this region",
	}
}

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
