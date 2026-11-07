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
	"context"
	"net/http"
	"strconv"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// LabelNames returns the shared labels for Cosmos accounting and rate limiting.
// Each call returns a new slice so callers cannot modify the shared schema.
func LabelNames() []string {
	return []string{"source_kind", "source", "cosmosdb_container", "operation", "status_code"}
}

// LabelValues returns values in LabelNames order. Informer attribution takes
// precedence over controller attribution. A nil request (a wait before CRUD
// execution) has unknown container and operation; a nil response has unknown
// status. It never labels requests with resource IDs, URLs, or error strings.
func LabelValues(ctx context.Context, req *http.Request, resp *http.Response) []string {
	sourceKind, source := sourceKindUnattributed, "unknown"
	if name, ok := utils.InformerNameFromContext(ctx); ok && name != "" {
		sourceKind, source = sourceKindInformer, name
	} else if name, ok := utils.ControllerNameFromContext(ctx); ok && name != "" {
		sourceKind, source = sourceKindController, name
	}
	container, operation, status := "unknown", "unknown", "unknown"
	if req != nil {
		container, operation = classifyRequest(req)
	}
	if resp != nil {
		status = strconv.Itoa(resp.StatusCode)
	}
	return []string{sourceKind, source, container, operation, status}
}
