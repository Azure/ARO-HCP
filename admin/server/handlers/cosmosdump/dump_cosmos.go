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
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/errorutils"
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
	logger := utils.LoggerFromContext(ctx)

	// get the azure resource ID for this HCP
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return utils.TrackError(err)
	}

	// load the HCP from the cosmos DB
	cosmosCRUD, err := h.cosmosClient.UntypedCRUD(*resourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	startingCosmosRecord, err := cosmosCRUD.Get(ctx, resourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	allCosmosRecords, err := cosmosCRUD.ListRecursive(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	errs := []error{}
	content, err := json.Marshal(startingCosmosRecord)
	if err != nil {
		errs = append(errs, err)
	}
	logger.Info(string(content))

	for _, typedDocument := range allCosmosRecords.Items(ctx) {
		content, err := json.Marshal(typedDocument)
		if err != nil {
			errs = append(errs, err)
		}
		logger.Info(string(content))
	}
	if err := allCosmosRecords.GetError(); err != nil {
		return utils.TrackError(err)
	}

	return utils.TrackError(errors.Join(errs...))

}
