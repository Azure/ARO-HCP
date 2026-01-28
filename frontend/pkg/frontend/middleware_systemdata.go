// Copyright 2025 Microsoft Corporation
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

package frontend

import (
	"encoding/json"
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func MiddlewareSystemData(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := utils.LoggerFromContext(ctx)

	// See https://eng.ms/docs/products/arm/api_contracts/resourcesystemdata
	data := r.Header.Get(arm.HeaderNameARMResourceSystemData)
	if data != "" {
		var systemData arm.SystemData
		err := json.Unmarshal([]byte(data), &systemData)
		if err == nil {
			ctx = ContextWithSystemData(ctx, &systemData)
			r = r.WithContext(ctx)
		} else {
			logger.Error(err, "Failed to parse system data header", "header", arm.HeaderNameARMResourceSystemData)
		}
	}

	next(w, r)
}
