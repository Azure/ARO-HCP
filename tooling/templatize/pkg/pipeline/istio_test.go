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

package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/istio"
)

func TestStopAfterForPhase(t *testing.T) {
	tests := []struct {
		name      string
		phase     string
		want      istio.StopAfter
		wantError bool
	}{
		{name: "install maps to StopAfterCanaryStart", phase: "install", want: istio.StopAfterCanaryStart},
		{name: "upgrade runs full lifecycle", phase: "upgrade", want: ""},
		{name: "empty runs full lifecycle", phase: "", want: ""},
		{name: "unknown phase returns error", phase: "rollback", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stopAfterForPhase(tt.phase)
			if tt.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unknown IstioUpgrade phase")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
