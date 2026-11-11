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

package informerutils

import (
	"testing"

	"github.com/stretchr/testify/require"

	cosmosclientutils "github.com/Azure/ARO-HCP/arohcp-cosmos-client/utils"
)

// Informers set their name through this package, and the Cosmos client module
// reads it through arohcp-cosmos-client/utils to attribute requests, so both
// must use the same context key.
func TestInformerNameSharedWithCosmosClient(t *testing.T) {
	name, ok := cosmosclientutils.InformerNameFromContext(ContextWithInformerName(t.Context(), "ClusterInformer"))
	require.True(t, ok)
	require.Equal(t, "ClusterInformer", name)

	name, ok = InformerNameFromContext(cosmosclientutils.ContextWithInformerName(t.Context(), "OtherInformer"))
	require.True(t, ok)
	require.Equal(t, "OtherInformer", name)
}
