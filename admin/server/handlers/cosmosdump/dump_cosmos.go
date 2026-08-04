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

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/serverutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type CosmosDumpHandler struct {
	resourcesDBClient corecosmosstorage.ResourcesDBClient
}

func NewCosmosDumpHandler(resourcesDBClient corecosmosstorage.ResourcesDBClient) *CosmosDumpHandler {
	return &CosmosDumpHandler{resourcesDBClient: resourcesDBClient}
}

func (h *CosmosDumpHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()

	// get the azure resource ID for this HCP
	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	if err := serverutils.DumpDataToLogger(ctx, h.resourcesDBClient, nil, nil, resourceID); err != nil {
		return utils.TrackError(err)
	}

	_, err = arm.WriteJSONResponse(w, http.StatusOK, map[string]any{})
	return utils.TrackError(err)
}

type BillingDumpHandler struct {
	resourcesDBClient corecosmosstorage.ResourcesDBClient
	billingDBClient   billingcosmosstorage.BillingDBClient
}

func NewBillingDumpHandler(resourcesDBClient corecosmosstorage.ResourcesDBClient, billingDBClient billingcosmosstorage.BillingDBClient) *BillingDumpHandler {
	return &BillingDumpHandler{resourcesDBClient: resourcesDBClient, billingDBClient: billingDBClient}
}

func (h *BillingDumpHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()

	// get the azure resource ID for this HCP
	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	if err := serverutils.DumpBillingToLogger(ctx, h.resourcesDBClient, h.billingDBClient, resourceID); err != nil {
		return utils.TrackError(err)
	}

	_, err = arm.WriteJSONResponse(w, http.StatusOK, map[string]any{})
	return utils.TrackError(err)
}
