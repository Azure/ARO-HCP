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

package cosmosmetrics

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/database/informers/informerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestLabelValuesBeforeHTTPRequest(t *testing.T) {
	ctx := utils.ContextWithControllerName(t.Context(), "Cleanup")
	require.Equal(t, []string{"controller", "Cleanup", "unknown", "unknown", "unknown"}, LabelValues(ctx, nil, nil))
	ctx = informerutils.ContextWithInformerName(ctx, "ClusterInformer")
	require.Equal(t, []string{"informer", "ClusterInformer", "unknown", "unknown", "unknown"}, LabelValues(ctx, nil, nil))
	// Both entry points share one attribution key.
	name, ok := utils.InformerNameFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "ClusterInformer", name)
	name, ok = informerutils.InformerNameFromContext(utils.ContextWithInformerName(t.Context(), "OtherInformer"))
	require.True(t, ok)
	require.Equal(t, "OtherInformer", name)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://cosmos.test/dbs/private-db/colls/Resources/docs", nil)
	require.NoError(t, err)
	req.Header.Set("x-ms-documentdb-query", "True")
	require.Equal(t, []string{"informer", "ClusterInformer", "Resources", "query", "unknown"}, LabelValues(ctx, req, nil))
	require.Equal(t, []string{"informer", "ClusterInformer", "Resources", "query", "429"}, LabelValues(ctx, req, &http.Response{StatusCode: 429}))
}

func TestLabelNamesCannotBeMutated(t *testing.T) {
	names := LabelNames()
	names[0] = "changed"
	require.Equal(t, []string{"source_kind", "source", "cosmosdb_container", "operation", "status_code"}, LabelNames())
}
