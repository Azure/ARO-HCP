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

package validation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

func TestValidateControlPlaneVersionRollout(t *testing.T) {
	for _, tc := range []struct {
		channel string
		valid   bool
	}{
		{"", false},
		{"stable-4.21", true},
		{"fast-4.22", true},
		{"candidate-4.22", true},
		{"nightly-4.23", true},
		{"unsupported-4.21", false},
		{"stable-invalid", false},
		{"stable-4", false},
		{"stable-4.21.0", false},
		{"stable-4.21-rc.1", false},
		{"stable-04.21", false},
		{"stable-4.021", false},
		{"stable-18446744073709551616.21", false},
	} {
		t.Run(tc.channel, func(t *testing.T) {
			rollout := &fleetapi.ControlPlaneVersionRollout{}
			if tc.channel != "" {
				id, err := fleetapi.ToControlPlaneVersionRolloutResourceID(tc.channel)
				require.NoError(t, err)
				rollout.CosmosMetadata = coreapi.CosmosMetadata{ResourceID: id}
			}
			for name, errs := range map[string]field.ErrorList{
				"create": ValidateControlPlaneVersionRolloutCreate(context.Background(), rollout),
				"update": ValidateControlPlaneVersionRolloutUpdate(context.Background(), rollout, rollout.DeepCopy()),
			} {
				t.Run(name, func(t *testing.T) {
					if tc.valid {
						require.Empty(t, errs)
					} else {
						require.Len(t, errs, 1)
						require.Equal(t, "cosmosMetadata.resourceID", errs[0].Field)
					}
				})
			}
		})
	}
}
