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

package version

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// isControlPlaneMinorUpgrade preserves CS models.IsMinorVersionUpgrade's
// classification, including a single major step and short or multi-segment
// numeric source versions. This selects a condition gate; it does not validate
// target eligibility.
func isControlPlaneMinorUpgrade(source string, desired *semver.Version) (bool, error) {
	if source == "" {
		return false, fmt.Errorf("source version is empty")
	}
	if desired == nil {
		return false, fmt.Errorf("desired version is missing")
	}
	// CS accepts additional numeric components, while semver permits at most
	// three. Validate the extras before dropping them: only major and minor
	// determine which condition gate applies.
	parseSource := source
	core, suffix := source, ""
	if index := strings.IndexAny(source, "-+"); index >= 0 {
		core, suffix = source[:index], source[index:]
	}
	segments := strings.Split(core, ".")
	if len(segments) > 3 {
		for _, segment := range segments[3:] {
			if _, err := strconv.ParseInt(segment, 10, 64); err != nil {
				return false, fmt.Errorf("invalid source version %q: %w", source, err)
			}
		}
		parseSource = strings.Join(segments[:3], ".") + suffix
	}
	parsed, err := semver.ParseTolerant(parseSource)
	if err != nil {
		return false, fmt.Errorf("invalid source version: %w", err)
	}
	sourceMajor, sourceMinor := parsed.Major, parsed.Minor
	if sourceMajor != desired.Major {
		if desired.Major > sourceMajor && desired.Major-sourceMajor == 1 {
			return true, nil
		}
		return false, fmt.Errorf("only single major version upgrades are supported: %s -> %s", source, desired)
	}
	return sourceMinor < desired.Minor, nil
}

type controlPlaneUpgradeDecision struct {
	eligible      bool
	conditionType string
	reason        string
	message       string
}

// controlPlaneUpgradeConditions ports CS's IsClusterVersionUpgradeable predicate.
// Missing observations are errors; known blocking conditions retain their original
// diagnostics. ReconciliationActive always takes precedence over the minor gate.
func controlPlaneUpgradeConditions(conditions []metav1.Condition, minorUpgrade bool) (controlPlaneUpgradeDecision, error) {
	reconciliation := meta.FindStatusCondition(conditions, string(hsv1beta1.ReconciliationActive))
	if reconciliation == nil {
		return controlPlaneUpgradeDecision{}, fmt.Errorf("HostedCluster has no %s condition", hsv1beta1.ReconciliationActive)
	}
	if reconciliation.Status != metav1.ConditionTrue {
		return blockedControlPlaneUpgrade(reconciliation), nil
	}
	if !minorUpgrade {
		return controlPlaneUpgradeDecision{eligible: true}, nil
	}
	upgradeable := meta.FindStatusCondition(conditions, string(hsv1beta1.ClusterVersionUpgradeable))
	if upgradeable == nil {
		return controlPlaneUpgradeDecision{}, fmt.Errorf("HostedCluster has no %s condition", hsv1beta1.ClusterVersionUpgradeable)
	}
	// CS blocks only explicit False with an unaccepted reason. Unknown and other
	// non-False values pass. Do not add freshness or observed-generation checks.
	if upgradeable.Status == metav1.ConditionFalse && !slices.Contains(
		[]string{"AdminAckRequired", "UpdateInProgress", "ClusterVersionUpdating"}, upgradeable.Reason,
	) {
		return blockedControlPlaneUpgrade(upgradeable), nil
	}
	return controlPlaneUpgradeDecision{eligible: true}, nil
}

func blockedControlPlaneUpgrade(condition *metav1.Condition) controlPlaneUpgradeDecision {
	return controlPlaneUpgradeDecision{
		conditionType: condition.Type,
		reason:        condition.Reason,
		message:       condition.Message,
	}
}
