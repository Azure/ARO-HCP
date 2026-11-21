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

package hcp

import (
	"fmt"
	"net/http"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// RolloutVersionPinHandler sets or clears the channel rollout's version pin.
// Unlike bulk cluster pinning, this changes the target used by normal rollout
// assignment. BestExactVersion must strictly exceed UntilExactVersion before
// selection resumes. Omitting exactVersion clears both pin fields.
type RolloutVersionPinHandler struct {
	fleetDBClient fleetcosmosstorage.FleetDBClient
}

func NewRolloutVersionPinHandler(client fleetcosmosstorage.FleetDBClient) *RolloutVersionPinHandler {
	return &RolloutVersionPinHandler{fleetDBClient: client}
}

func (h *RolloutVersionPinHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	channel := r.PathValue("channel")
	group, minor, err := parseFleetControlPlaneVersionPinChannel(channel)
	if err != nil {
		return err
	}
	pin, err := decodeVersionPinRequest(r.Body)
	if err != nil {
		return err
	}
	if err := validateClusterVersionPin(coreapi.VersionProfile{ID: minor, ChannelGroup: group}, pin); err != nil {
		return err
	}
	storage := h.fleetDBClient.ControlPlaneVersionRollouts()
	existing, err := storage.Get(r.Context(), channel)
	if cosmosstorageutils.IsNotFoundError(err) {
		return coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeResourceNotFound, "", "ControlPlaneVersionRollout %s not found", channel)
	}
	if err != nil {
		return fmt.Errorf("failed to get ControlPlaneVersionRollout: %w", err)
	}
	replacement := existing.DeepCopy()
	replacement.Spec.PinnedVersion = fleetapi.ServiceProviderClusterPinnedVersion{ExactVersion: pin.ExactVersion, UntilExactVersion: pin.UntilExactVersion}
	if _, err := storage.Replace(r.Context(), replacement, existing, nil); cosmosstorageutils.IsPreconditionFailedError(err) {
		return coreapi.NewCloudError(http.StatusConflict, coreapi.CloudErrorCodeConflict, "", "ETag conflict, retry the operation")
	} else if err != nil {
		return fmt.Errorf("failed to replace ControlPlaneVersionRollout: %w", err)
	}
	response := controlPlaneVersionPinResponse{}
	if pin.ExactVersion != nil {
		response.ExactVersion = ptr.To(pin.ExactVersion.String())
	}
	if pin.UntilExactVersion != nil {
		response.UntilExactVersion = ptr.To(pin.UntilExactVersion.String())
	}
	_, err = coreapihelpers.WriteJSONResponse(w, http.StatusOK, response)
	return utils.TrackError(err)
}
