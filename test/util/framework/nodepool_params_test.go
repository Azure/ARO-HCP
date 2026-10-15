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
)

func TestNodePoolAutoRepairDefaults(t *testing.T) {
	t.Setenv("ARO_HCP_OPENSHIFT_NODEPOOL_VERSION", "4.21.0")

	t.Run("20251223preview", func(t *testing.T) {
		params := NewDefaultNodePoolParams20251223()
		nodePool := BuildNodePoolFromParams20251223(params, "eastus")
		require.NotNil(t, nodePool.Properties.AutoRepair, "default node pool must specify auto-repair")
		require.True(t, *nodePool.Properties.AutoRepair, "default node pool must enable auto-repair")

		params.AutoRepair = false
		nodePool = BuildNodePoolFromParams20251223(params, "eastus")
		require.NotNil(t, nodePool.Properties.AutoRepair, "explicit auto-repair opt-out must not be omitted")
		require.False(t, *nodePool.Properties.AutoRepair, "explicit auto-repair opt-out must be preserved")
	})

	t.Run("20260630preview", func(t *testing.T) {
		params := NewDefaultNodePoolParams20260630()
		nodePool := BuildNodePoolFromParams20260630(params, "eastus")
		require.NotNil(t, nodePool.Properties.AutoRepair, "default node pool must specify auto-repair")
		require.True(t, *nodePool.Properties.AutoRepair, "default node pool must enable auto-repair")

		params.AutoRepair = false
		nodePool = BuildNodePoolFromParams20260630(params, "eastus")
		require.NotNil(t, nodePool.Properties.AutoRepair, "explicit auto-repair opt-out must not be omitted")
		require.False(t, *nodePool.Properties.AutoRepair, "explicit auto-repair opt-out must be preserved")
	})

	t.Run("20260901preview", func(t *testing.T) {
		params := NewDefaultNodePoolParams20260901()
		nodePool := BuildNodePoolFromParams20260901(params, "eastus")
		require.NotNil(t, nodePool.Properties.AutoRepair, "default node pool must specify auto-repair")
		require.True(t, *nodePool.Properties.AutoRepair, "default node pool must enable auto-repair")

		params.AutoRepair = false
		nodePool = BuildNodePoolFromParams20260901(params, "eastus")
		require.NotNil(t, nodePool.Properties.AutoRepair, "explicit auto-repair opt-out must not be omitted")
		require.False(t, *nodePool.Properties.AutoRepair, "explicit auto-repair opt-out must be preserved")
	})

	t.Run("20261001", func(t *testing.T) {
		params := NewDefaultNodePoolParams20261001()
		nodePool := BuildNodePoolFromParams20261001(params, "eastus")
		require.NotNil(t, nodePool.Properties.AutoRepair, "default node pool must specify auto-repair")
		require.True(t, *nodePool.Properties.AutoRepair, "default node pool must enable auto-repair")

		params.AutoRepair = false
		nodePool = BuildNodePoolFromParams20261001(params, "eastus")
		require.NotNil(t, nodePool.Properties.AutoRepair, "explicit auto-repair opt-out must not be omitted")
		require.False(t, *nodePool.Properties.AutoRepair, "explicit auto-repair opt-out must be preserved")
	})
}
