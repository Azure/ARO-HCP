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
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBackend_NilOptionsReturnsError(t *testing.T) {
	var options *BackendOptions
	b, err := options.NewBackend()
	require.Error(t, err)
	assert.Nil(t, b)
}

func TestNewBackend_MetricsRegistryPairing(t *testing.T) {
	registry := prometheus.NewRegistry()

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
