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

package hcpresourcerequirements

import (
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// HCPResourceRequirementsGetHandler handles GET /admin/v1/hcpresourcerequirements/{name}.
type HCPResourceRequirementsGetHandler struct {
	fleetDBClient fleetcosmosstorage.FleetDBClient
}

func NewHCPResourceRequirementsGetHandler(fleetDBClient fleetcosmosstorage.FleetDBClient) *HCPResourceRequirementsGetHandler {
	return &HCPResourceRequirementsGetHandler{
		fleetDBClient: fleetDBClient,
	}
}

func (h *HCPResourceRequirementsGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	name := r.PathValue("name")

	requirements, err := h.fleetDBClient.HCPResourceRequirements().Get(ctx, name)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeNotFound, "",
				"HCP resource requirements %q not found", name)
		}
		return utils.TrackError(err)
	}

	_, err = coreapi.WriteJSONResponse(w, http.StatusOK, requirements.Status)
	return utils.TrackError(err)
}
