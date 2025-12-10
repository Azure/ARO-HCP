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

package cosmosdump

import (
	"net/http"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/errorutils"
	"github.com/Azure/ARO-HCP/internal/serverutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type CosmosDumpHandler struct {
	cosmosClient database.DBClient
}

func NewCosmosDumpHandler(cosmosClient database.DBClient) http.Handler {
	return &CosmosDumpHandler{cosmosClient: cosmosClient}
}

func (h *CosmosDumpHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	errorutils.ReportError(h.serveHTTP)
}

func (h *CosmosDumpHandler) serveHTTP(w http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()

	// get the azure resource ID for this HCP
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return utils.TrackError(err)
	}

	return serverutils.DumpDataToLogger(ctx, h.cosmosClient, resourceID)
}
