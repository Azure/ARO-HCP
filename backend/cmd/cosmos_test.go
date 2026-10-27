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

package cmd

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/backend/pkg/app"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/denyassignments"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerconfig"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
)

type throughputReaderFunc func(context.Context, string) (float64, error)

func (reader throughputReaderFunc) ContainerThroughput(ctx context.Context, name string) (float64, error) {
	return reader(ctx, name)
}

func TestDiscoverStorageOptions(t *testing.T) {
	for _, testCase := range []struct {
		name                 string
		controllerContext    app.ControllerContext
		orphanCleanupEnabled bool
		resourcesRU          float64
		billingRU            float64
		fleetRU              float64
	}{
		{name: "mock FPA", resourcesRU: 100, billingRU: 200, fleetRU: 300},
		{name: "real FPA", controllerContext: app.ControllerContext{HasRealFPA: true}, resourcesRU: 1100, billingRU: 2200, fleetRU: 3300},
		{name: "empty cleanup config", controllerContext: app.ControllerContext{OrphanedMRGCleanupConfig: &controllerconfig.OrphanedMRGCleanupConfig{}}, resourcesRU: 100, billingRU: 200, fleetRU: 300},
		{name: "mock FPA target cleanup", controllerContext: app.ControllerContext{OrphanedMRGCleanupConfig: &controllerconfig.OrphanedMRGCleanupConfig{TargetSubscriptionAFECFlags: "target"}}, orphanCleanupEnabled: true, resourcesRU: 100, billingRU: 200, fleetRU: 300},
		{name: "real FPA excluded cleanup", controllerContext: app.ControllerContext{HasRealFPA: true, OrphanedMRGCleanupConfig: &controllerconfig.OrphanedMRGCleanupConfig{ExcludedSubscriptionAFECFlags: []string{"excluded"}}}, orphanCleanupEnabled: true, resourcesRU: 1100, billingRU: 2200, fleetRU: 3300},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var calls []string
			options, err := discoverStorageOptions(t.Context(), throughputReaderFunc(func(ctx context.Context, name string) (float64, error) {
				calls = append(calls, name)
				return map[string]float64{"Resources": testCase.resourcesRU, "Billing": testCase.billingRU, "Fleet": testCase.fleetRU}[name], nil
			}), testCase.controllerContext)
			require.NoError(t, err)
			require.Equal(t, testCase.resourcesRU, options.ResourcesRUsPerSecond)
			require.Equal(t, testCase.billingRU, options.BillingRUsPerSecond)
			require.Equal(t, testCase.fleetRU, options.FleetRUsPerSecond)
			require.Equal(t, app.BackendStorageFactoryOptions(testCase.controllerContext, testCase.resourcesRU, testCase.billingRU, testCase.fleetRU), options)
			require.Equal(t, testCase.controllerContext.HasRealFPA, slices.Contains(options.ControllerNames, strings.ToLower(denyassignments.ClusterDenyAssignmentControllerName)))
			orphanCleanupName := strings.ToLower(controllerutils.ManagedResourceGroupWatchingControllerName)
			require.Equal(t, testCase.orphanCleanupEnabled, slices.Contains(options.ControllerNames, orphanCleanupName))
			_, hasOrphanFraction := options.ControllerFractions[orphanCleanupName]
			require.Equal(t, testCase.orphanCleanupEnabled, hasOrphanFraction)
			require.Equal(t, 0.3, options.KubeApplierUtilization)
			expectedFractions := app.BackendCleanupControllerFractions(0.1)
			if !testCase.orphanCleanupEnabled {
				delete(expectedFractions, orphanCleanupName)
			}
			require.Equal(t, expectedFractions, options.ControllerFractions)
			for name := range options.ControllerFractions {
				require.Contains(t, options.ControllerNames, name)
			}
			require.Equal(t, []string{"Resources", "Billing", "Fleet"}, calls)
		})
	}
}

func TestDiscoverStorageOptionsFailsWithoutFallback(t *testing.T) {
	containers := []string{"Resources", "Billing", "Fleet"}
	for index, name := range containers {
		for _, testCase := range []struct {
			name        string
			value       float64
			lookupError error
		}{
			{name: "zero"},
			{name: "negative", value: -1},
			{name: "NaN", value: math.NaN()},
			{name: "positive infinity", value: math.Inf(1)},
			{name: "negative infinity", value: math.Inf(-1)},
			{name: "lookup error", lookupError: errors.New("ARM lookup failed")},
		} {
			t.Run(name+"/"+testCase.name, func(t *testing.T) {
				var calls []string
				options, err := discoverStorageOptions(t.Context(), throughputReaderFunc(func(ctx context.Context, container string) (float64, error) {
					calls = append(calls, container)
					if container == name {
						return testCase.value, testCase.lookupError
					}
					return 100, nil
				}), app.ControllerContext{})
				require.ErrorContains(t, err, name)
				if testCase.lookupError != nil {
					require.ErrorIs(t, err, testCase.lookupError)
				} else {
					require.ErrorContains(t, err, "expected positive finite RU/s")
				}
				require.Equal(t, app.StorageFactoryOptions{}, options)
				require.Equal(t, containers[:index+1], calls)
			})
		}
	}
}
