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
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestIsControlPlaneMinorUpgrade(t *testing.T) {
	for _, tc := range []struct {
		name, source, target string
		minor                bool
		wantErr              string
	}{
		{name: "patch", source: "4.19.1", target: "4.19.2"},
		{name: "equal", source: "4.19.1", target: "4.19.1"},
		{name: "lower patch", source: "4.19.2", target: "4.19.1"},
		{name: "minor", source: "4.19.1", target: "4.20.2", minor: true},
		{name: "single major", source: "4.99.1", target: "5.0.0", minor: true},
		{name: "major jump", source: "4.19.1", target: "6.0.0", wantErr: "only single major"},
		{name: "lower major", source: "5.0.0", target: "4.20.0", wantErr: "only single major"},
		{name: "lower minor", source: "4.20.1", target: "4.19.1"},
		{name: "prerelease", source: "4.19.0-rc.1", target: "4.20.0-rc.2", minor: true},
		{name: "metadata", source: "4.19.0+build.1", target: "4.20.0+build.2", minor: true},
		{name: "short minor", source: "4.19", target: "4.20.0", minor: true},
		{name: "short major", source: "4", target: "4.1.0", minor: true},
		{name: "v prefix", source: "v4.19.0", target: "4.20.0", minor: true},
		{name: "extra segment accepted by CS parser", source: "4.19.0.1", target: "4.20.0", minor: true},
		{name: "extra segment with prerelease", source: "4.19.0.1-rc.1", target: "4.20.0", minor: true},
		{name: "invalid extra segment", source: "4.19.0.extra", target: "4.20.0", wantErr: "invalid source"},
		{name: "invalid", source: "invalid", target: "4.20.0", wantErr: "invalid source"},
		{name: "API ID is not raw version", source: "openshift-v4.19.0", target: "4.20.0", wantErr: "invalid source"},
		{name: "empty source", target: "4.20.0", wantErr: "source version is empty"},
		{name: "missing target", source: "4.19.0", wantErr: "desired version is missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var target *semver.Version
			if tc.target != "" {
				target = ptr.To(semver.MustParse(tc.target))
			}
			minor, err := isControlPlaneMinorUpgrade(tc.source, target)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.minor, minor)
		})
	}
}

func TestControlPlaneUpgradeConditions(t *testing.T) {
	condition := func(kind string, status metav1.ConditionStatus, reason string) *metav1.Condition {
		return &metav1.Condition{Type: kind, Status: status, Reason: reason, Message: "original diagnostic"}
	}
	reconciliation := string(hsv1beta1.ReconciliationActive)
	upgradeable := string(hsv1beta1.ClusterVersionUpgradeable)
	active := condition(reconciliation, metav1.ConditionTrue, "AsExpected")
	for _, tc := range []struct {
		name        string
		active      *metav1.Condition
		upgradeable *metav1.Condition
		minor       bool
		eligible    bool
		wantErr     string
	}{
		{name: "missing reconciliation takes precedence", minor: true, wantErr: reconciliation},
		{name: "patch only needs reconciliation", active: active, eligible: true},
		{name: "patch ignores blocked minor condition", active: active, upgradeable: condition(upgradeable, metav1.ConditionFalse, "Blocked"), eligible: true},
		{name: "missing minor condition", active: active, minor: true, wantErr: upgradeable},
		{name: "minor true", active: active, minor: true, upgradeable: condition(upgradeable, metav1.ConditionTrue, ""), eligible: true},
		{name: "admin ack", active: active, minor: true, upgradeable: condition(upgradeable, metav1.ConditionFalse, "AdminAckRequired"), eligible: true},
		{name: "old updating reason", active: active, minor: true, upgradeable: condition(upgradeable, metav1.ConditionFalse, "UpdateInProgress"), eligible: true},
		{name: "new updating reason", active: active, minor: true, upgradeable: condition(upgradeable, metav1.ConditionFalse, "ClusterVersionUpdating"), eligible: true},
		{name: "unknown passes", active: active, minor: true, upgradeable: condition(upgradeable, metav1.ConditionUnknown, "Blocked"), eligible: true},
		{name: "empty status passes", active: active, minor: true, upgradeable: condition(upgradeable, "", "Blocked"), eligible: true},
		{name: "unexpected status passes", active: active, minor: true, upgradeable: condition(upgradeable, "Unexpected", "Blocked"), eligible: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var conditions []metav1.Condition
			if tc.active != nil {
				conditions = append(conditions, *tc.active)
			}
			if tc.upgradeable != nil {
				conditions = append(conditions, *tc.upgradeable)
			}
			decision, err := controlPlaneUpgradeConditions(conditions, tc.minor)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.False(t, decision.eligible)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.eligible, decision.eligible)
		})
	}
	for _, status := range []metav1.ConditionStatus{metav1.ConditionFalse, metav1.ConditionUnknown, "", "Unexpected"} {
		t.Run("reconciliation blocks "+string(status), func(t *testing.T) {
			blocked := condition(reconciliation, status, "Paused")
			// A missing minor condition must not replace the reconciliation decision.
			for _, minor := range []bool{false, true} {
				decision, err := controlPlaneUpgradeConditions([]metav1.Condition{*blocked}, minor)
				require.NoError(t, err)
				require.Equal(t, controlPlaneUpgradeDecision{conditionType: reconciliation, reason: "Paused", message: "original diagnostic"}, decision)
			}
		})
	}
	for _, reason := range []string{"Blocked", "", "adminackrequired", "updateinprogress", "clusterversionupdating"} {
		t.Run("minor blocks reason "+reason, func(t *testing.T) {
			blocked := condition(upgradeable, metav1.ConditionFalse, reason)
			decision, err := controlPlaneUpgradeConditions([]metav1.Condition{*active, *blocked}, true)
			require.NoError(t, err)
			require.Equal(t, controlPlaneUpgradeDecision{conditionType: upgradeable, reason: reason, message: "original diagnostic"}, decision)
		})
	}
}
