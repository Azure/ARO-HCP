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

package stamp

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type schedulingPolicyRequest struct {
	SchedulingPolicy fleetapi.ManagementClusterSchedulingPolicy `json:"schedulingPolicy"`
}

// ManagementClusterSchedulingPolicyPutHandler handles PUT /admin/v1/stamps/{stampIdentifier}/managementclusters/{managementClusterName}/schedulingPolicy.
type ManagementClusterSchedulingPolicyPutHandler struct {
	fleetDBClient fleetcosmosstorage.FleetDBClient
}

func NewManagementClusterSchedulingPolicyPutHandler(fleetDBClient fleetcosmosstorage.FleetDBClient) *ManagementClusterSchedulingPolicyPutHandler {
	return &ManagementClusterSchedulingPolicyPutHandler{
		fleetDBClient: fleetDBClient,
	}
}

func (h *ManagementClusterSchedulingPolicyPutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	stampIdentifier := r.PathValue("stampIdentifier")
	managementClusterName := r.PathValue("managementClusterName")

	if err := validateStampIdentifier(stampIdentifier); err != nil {
		return err
	}

	var body schedulingPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "invalid JSON body: %v", err)
	}

	if !fleetapi.ValidManagementClusterSchedulingPolicies.Has(body.SchedulingPolicy) {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "",
			"schedulingPolicy %q must be one of %v", body.SchedulingPolicy, fleetapi.ValidManagementClusterSchedulingPolicies.UnsortedList())
	}

	managementClustersCRUD := h.fleetDBClient.Stamps().ManagementClusters(stampIdentifier)

	existing, err := managementClustersCRUD.Get(ctx, managementClusterName)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeNotFound, "",
				"Management cluster %q not found in stamp %q", managementClusterName, stampIdentifier)
		}
		return utils.TrackError(fmt.Errorf("failed to get management cluster: %w", err))
	}

	updated := existing.DeepCopy()
	updated.Spec.SchedulingPolicy = body.SchedulingPolicy

	if _, err := managementClustersCRUD.Replace(ctx, updated, existing, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to update management cluster scheduling policy: %w", err))
	}

	_, err = coreapi.WriteJSONResponse(w, http.StatusOK, schedulingPolicyRequest{SchedulingPolicy: body.SchedulingPolicy})
	return utils.TrackError(err)
}
