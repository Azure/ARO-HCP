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

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// controlPlaneUpgradeBaseline picks the version a transition is classified
// against, from newest-first mirrored history.
//
// A completed entry is preferred: a partial entry is an in-flight target rather
// than an achieved version, so it must not become the baseline for a later
// retarget. Upgrading 4.19.1 -> 4.20.1 and then retargeting to 4.20.2 is still
// a minor change and has to keep the minor gate.
//
// When no entry has completed, the newest entry is used instead of refusing to
// classify. On pre-4.22 clusters the mirror falls back to the guest
// ClusterVersion history, which can stay Partial indefinitely on a healthy
// control plane whose worker-dependent operators are unavailable — a cluster
// with no node pools, for example (see test/e2e/control_plane_version_pin.go).
// Requiring completion there would silently suppress every dispatch, including
// automatic z-stream rollout.
//
// The mirror writer stores only parsed, non-nil versions, so nil means empty
// history in normal operation; callers already filter that out.
func controlPlaneUpgradeBaseline(active []coreapi.ServiceProviderClusterActiveVersion) *semver.Version {
	var newest *semver.Version
	for _, entry := range active {
		if entry.State == configv1.CompletedUpdate {
			return entry.Version
		}
		if newest == nil {
			newest = entry.Version
		}
	}
	return newest
}

// isControlPlaneMinorUpgrade selects the condition gate for a version change.
// Target eligibility is validated separately before the desired version is set.
func isControlPlaneMinorUpgrade(source, desired *semver.Version) (bool, error) {
	if source == nil {
		return false, fmt.Errorf("source version is missing")
	}
	if desired == nil {
		return false, fmt.Errorf("desired version is missing")
	}
	// Compare only major.minor, the same granularity this function classifies
	// on. A nightly build carries prerelease identifiers that sort below a
	// stable release of the same minor even when it is newer, and admission
	// accepts it on that basis (admitClusterVersionProfile uses
	// VersionMustBeAtLeastMajorMinor for the nightly channel). A full semver
	// comparison here would let such a request be accepted and then refuse to
	// act on it. Other channels reject a lower target at admission already.
	if desired.Major < source.Major || (desired.Major == source.Major && desired.Minor < source.Minor) {
		return false, fmt.Errorf("control plane major.minor downgrade is not supported: %s -> %s", source, desired)
	}
	if source.Major != desired.Major {
		if desired.Major > source.Major && desired.Major-source.Major == 1 {
			return true, nil
		}
		return false, fmt.Errorf("only single major version upgrades are supported: %s -> %s", source, desired)
	}
	return source.Minor < desired.Minor, nil
}

type controlPlaneUpgradeDecision struct {
	eligible      bool
	conditionType string
	reason        string
	message       string
}

// controlPlaneUpgradeConditions gates a new policy using observed HostedCluster
// conditions. A missing condition is an error rather than a pass, because
// dispatching without an observation would submit an upgrade to a control plane
// whose readiness is unknown; the caller's error path retries until the mirror
// catches up. Blocking conditions are returned with their original reason and
// message so the operator sees the control plane's own diagnostic.
// ReconciliationActive takes precedence over the minor gate: a control plane
// that is not reconciling cannot act on any new target, minor or not.
func controlPlaneUpgradeConditions(conditions []metav1.Condition, minorUpgrade bool) (controlPlaneUpgradeDecision, error) {
	reconciliation := meta.FindStatusCondition(conditions, string(hsv1beta1.ReconciliationActive))
	if reconciliation == nil {
		return controlPlaneUpgradeDecision{}, fmt.Errorf("HostedCluster has no %s condition", hsv1beta1.ReconciliationActive)
	}
	if reconciliation.Status != metav1.ConditionTrue {
		return controlPlaneUpgradeDecision{conditionType: reconciliation.Type, reason: reconciliation.Reason, message: reconciliation.Message}, nil
	}
	if !minorUpgrade {
		return controlPlaneUpgradeDecision{eligible: true}, nil
	}
	upgradeable := meta.FindStatusCondition(conditions, string(hsv1beta1.ClusterVersionUpgradeable))
	if upgradeable == nil {
		return controlPlaneUpgradeDecision{}, fmt.Errorf("HostedCluster has no %s condition", hsv1beta1.ClusterVersionUpgradeable)
	}
	// Only an explicit False with an unaccepted reason blocks a minor upgrade.
	// The accepted reasons describe an update already in flight or an
	// acknowledgement the operator has to clear, neither of which means the
	// target itself is unsafe. Unknown means the cluster-version operator has
	// not reported yet and must not strand the upgrade indefinitely, so it
	// passes too.
	if upgradeable.Status == metav1.ConditionFalse && !slices.Contains(
		[]string{"AdminAckRequired", "UpdateInProgress", "ClusterVersionUpdating"}, upgradeable.Reason,
	) {
		return controlPlaneUpgradeDecision{conditionType: upgradeable.Type, reason: upgradeable.Reason, message: upgradeable.Message}, nil
	}
	return controlPlaneUpgradeDecision{eligible: true}, nil
}
