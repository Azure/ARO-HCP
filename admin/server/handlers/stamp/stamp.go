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
	"fmt"
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Stamp is the API response for a stamp, without CosmosMetadata.
type Stamp struct {
	ResourceID string            `json:"resourceId"`
	Spec       fleet.StampSpec   `json:"spec"`
	Status     fleet.StampStatus `json:"status"`
}

func validateStampIdentifier(stampIdentifier string) error {
	if _, err := fleet.ToStampResourceID(stampIdentifier); err != nil {
		return arm.NewCloudError(
			http.StatusBadRequest,
			arm.CloudErrorCodeInvalidRequestContent, "stampIdentifier",
			"Invalid stamp identifier: %q", stampIdentifier,
		)
	}
	return nil
}

func toStamp(s *fleet.Stamp) (Stamp, error) {
	if s.ResourceID == nil {
		return Stamp{}, fmt.Errorf("stamp has nil resourceId")
	}
	return Stamp{
		ResourceID: s.ResourceID.String(),
		Spec:       s.Spec,
		Status:     s.Status,
	}, nil
}

// StampListHandler handles GET /admin/v1/stamps.
type StampListHandler struct {
	fleetDBClient database.FleetDBClient
}

func NewStampListHandler(fleetDBClient database.FleetDBClient) *StampListHandler {
	return &StampListHandler{
		fleetDBClient: fleetDBClient,
	}
}

func (h *StampListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	iter, err := h.fleetDBClient.GlobalListers().Stamps().List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list stamps: %w", err))
	}

	var stamps []Stamp
	for _, s := range iter.Items(ctx) {
		resp, err := toStamp(s)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to convert stamp: %w", err))
		}
		stamps = append(stamps, resp)
	}
	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("failed to iterate stamps: %w", err))
	}

	if stamps == nil {
		stamps = []Stamp{}
	}

	_, err = arm.WriteJSONResponse(w, http.StatusOK, stamps)
	return utils.TrackError(err)
}

// StampGetHandler handles GET /admin/v1/stamps/{stampIdentifier}.
type StampGetHandler struct {
	fleetDBClient database.FleetDBClient
}

func NewStampGetHandler(fleetDBClient database.FleetDBClient) *StampGetHandler {
	return &StampGetHandler{
		fleetDBClient: fleetDBClient,
	}
}

func (h *StampGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	stampIdentifier := r.PathValue("stampIdentifier")

	if err := validateStampIdentifier(stampIdentifier); err != nil {
		return err
	}

	stamp, err := h.fleetDBClient.Stamps().Get(ctx, stampIdentifier)
	if err != nil {
		if database.IsNotFoundError(err) {
			return arm.NewCloudError(http.StatusNotFound, arm.CloudErrorCodeNotFound, "", "Stamp %q not found", stampIdentifier)
		}
		return utils.TrackError(fmt.Errorf("failed to get stamp: %w", err))
	}

	resp, err := toStamp(stamp)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to convert stamp: %w", err))
	}

	_, err = arm.WriteJSONResponse(w, http.StatusOK, resp)
	return utils.TrackError(err)
}
