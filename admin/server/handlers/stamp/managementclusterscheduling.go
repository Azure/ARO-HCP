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
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ManagementClusterSchedulingGetHandler handles GET /admin/v1/stamps/{stampIdentifier}/managementclusters/{managementClusterName}/scheduling.
type ManagementClusterSchedulingGetHandler struct {
	fleetDBClient fleetcosmosstorage.FleetDBClient
}

func NewManagementClusterSchedulingGetHandler(fleetDBClient fleetcosmosstorage.FleetDBClient) *ManagementClusterSchedulingGetHandler {
	return &ManagementClusterSchedulingGetHandler{
		fleetDBClient: fleetDBClient,
	}
}

func (h *ManagementClusterSchedulingGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	stampIdentifier := r.PathValue("stampIdentifier")
	managementClusterName := r.PathValue("managementClusterName")

	if err := validateStampIdentifier(stampIdentifier); err != nil {
		return err
	}

	managementClusters := h.fleetDBClient.Stamps().ManagementClusters(stampIdentifier)

	_, err := managementClusters.Get(ctx, managementClusterName)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeNotFound, "",
				"Management cluster %q not found in stamp %q", managementClusterName, stampIdentifier)
		}
		return utils.TrackError(err)
	}

	scheduling, err := managementClusters.Scheduling().Get(ctx, fleetapi.SchedulingResourceName)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeNotFound, "",
				"Scheduling not found for management cluster %q in stamp %q", managementClusterName, stampIdentifier)
		}
		return utils.TrackError(err)
	}

	_, err = coreapi.WriteJSONResponse(w, http.StatusOK, scheduling.Status)
	return utils.TrackError(err)
}
