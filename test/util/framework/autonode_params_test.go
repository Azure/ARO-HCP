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

package framework

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20270201 "github.com/Azure/ARO-HCP/test/sdk/v20270201/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func TestAutoNodeParams20270201(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mode    *hcpsdk20270201.AutoNodeMode
		enabled bool
	}{
		{name: "omitted"},
		{name: "disabled", mode: to.Ptr(hcpsdk20270201.AutoNodeModeDisabled)},
		{name: "enabled", mode: to.Ptr(hcpsdk20270201.AutoNodeModeEnabled), enabled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			params := NewDefaultClusterParams20270201()
			params.AutoNodeMode = tt.mode
			require.Equal(t, tt.enabled, params.autoNodeEnabled(), "identity enablement must match the typed mode")
			cluster, err := BuildHCPClusterFromParams20270201(params, "test-location", nil)
			require.NoError(t, err)
			if tt.mode == nil {
				require.Nil(t, cluster.Properties.AutoNode)
			} else {
				require.NotNil(t, cluster.Properties.AutoNode)
				require.Equal(t, tt.mode, cluster.Properties.AutoNode.Mode)
			}
		})
	}
}
