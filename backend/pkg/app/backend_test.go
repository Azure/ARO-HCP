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

package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apisconfigv1 "github.com/Azure/ARO-HCP/backend/pkg/apis/config/v1"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/backups"
)

func TestNewBackend_NilOptionsReturnsError(t *testing.T) {
	var options *BackendOptions
	b, err := options.NewBackend()
	require.Error(t, err)
	assert.Nil(t, b)
}

func TestNewBackend_MetricsRegistryPairing(t *testing.T) {
	registry := prometheus.NewRegistry()
	cloudEnvironment, err := azureconfig.NewAzureCloudEnvironment(apisconfigv1.AzurePublicCloud, nil)
	require.NoError(t, err)

	for _, tc := range []struct {
		name       string
		registerer prometheus.Registerer
		gatherer   prometheus.Gatherer
		wantErr    bool
	}{
		{name: "both unset (rejected: production must wire both)", wantErr: true},
		{name: "both set", registerer: registry, gatherer: registry, wantErr: false},
		{name: "registerer only", registerer: registry, wantErr: true},
		{name: "gatherer only", gatherer: registry, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := (&BackendOptions{
				MetricsRegisterer: tc.registerer,
				MetricsGatherer:   tc.gatherer,
				BackupConfig:      &backups.BackupConfig{},
				StorageFactory:    &cosmosStorageFactory{},
				CloudEnvironment:  cloudEnvironment,
			}).NewBackend()
			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, b)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, b)
		})
	}
}

func TestNewBackend_IdentityModeMetric(t *testing.T) {
	for _, tc := range []struct {
		name       string
		hasRealFPA bool
		wantValue  int
	}{
		{name: "real FPA", hasRealFPA: true, wantValue: 0},
		{name: "insecure mock identities", hasRealFPA: false, wantValue: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := prometheus.NewPedanticRegistry()
			_, err := (&BackendOptions{
				MetricsRegisterer: registry,
				MetricsGatherer:   registry,
				BackupConfig:      &backups.BackupConfig{},
				StorageFactory:    &cosmosStorageFactory{},
				HasRealFPA:        tc.hasRealFPA,
			}).NewBackend()
			require.NoError(t, err)
			// The metric is available before leader election or controller startup.
			require.NoError(t, testutil.GatherAndCompare(registry, strings.NewReader(fmt.Sprintf(`
# HELP backend_insecure_mock_managed_identities_enabled Whether InsecureIgnoreUserAzureManagedIdentitiesThatNeedManagedIdentitiesDataplaneAvailableAndUseMock is enabled (1) or disabled (0).
# TYPE backend_insecure_mock_managed_identities_enabled gauge
backend_insecure_mock_managed_identities_enabled %d
`, tc.wantValue)), "backend_insecure_mock_managed_identities_enabled"))
		})
	}
}
