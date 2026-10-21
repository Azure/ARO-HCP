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
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type stampApprovalRequest struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason"`
	Message  string `json:"message"`
}

type StampApprovalHandler struct {
	fleetDBClient fleetcosmosstorage.FleetDBClient
}

func NewStampApprovalHandler(fleetDBClient fleetcosmosstorage.FleetDBClient) *StampApprovalHandler {
	return &StampApprovalHandler{
		fleetDBClient: fleetDBClient,
	}
}

func (h *StampApprovalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	stampIdentifier := r.PathValue("stampIdentifier")

	var body stampApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return coreapi.NewCloudError(
			http.StatusBadRequest,
			coreapi.CloudErrorCodeInvalidRequestContent, "",
			"The request content was invalid and could not be deserialized: %q", err,
		)
	}

	if err := validateApprovalRequest(body); err != nil {
		return err
	}

	if err := validateStampIdentifier(stampIdentifier); err != nil {
		return err
	}

	stampsCRUD := h.fleetDBClient.Stamps()
	existing, err := stampsCRUD.Get(ctx, stampIdentifier)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeNotFound, "", "Stamp %q not found", stampIdentifier)
		}
		return utils.TrackError(fmt.Errorf("failed to get stamp: %w", err))
	}

	conditionStatus := metav1.ConditionFalse
	if body.Approved {
		conditionStatus = metav1.ConditionTrue
	}

	// Check if this is a no-op (idempotent)
	existingCondition := apimeta.FindStatusCondition(existing.Status.Conditions, string(fleetapi.StampConditionApproved))
	if existingCondition != nil &&
		existingCondition.Status == conditionStatus &&
		existingCondition.Reason == body.Reason &&
		existingCondition.Message == body.Message {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}

	updated := existing.DeepCopy()
	apimeta.SetStatusCondition(&updated.Status.Conditions, metav1.Condition{
		Type:               string(fleetapi.StampConditionApproved),
		Status:             conditionStatus,
		Reason:             body.Reason,
		Message:            body.Message,
		LastTransitionTime: metav1.NewTime(time.Now()),
	})

	if _, err := stampsCRUD.Replace(ctx, updated, existing, nil); err != nil {
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return coreapi.NewCloudError(http.StatusConflict, coreapi.CloudErrorCodeConflict, "", "ETag conflict, retry the operation")
		}
		return utils.TrackError(err)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

func validateApprovalRequest(body stampApprovalRequest) error {
	var details []coreapi.CloudErrorBody

	if len(body.Reason) == 0 {
		details = append(details, coreapi.CloudErrorBody{
			Code:    coreapi.CloudErrorCodeInvalidRequestContent,
			Target:  "reason",
			Message: "reason is required",
		})
	}
	if len(body.Message) == 0 {
		details = append(details, coreapi.CloudErrorBody{
			Code:    coreapi.CloudErrorCodeInvalidRequestContent,
			Target:  "message",
			Message: "message is required",
		})
	}

	if len(details) == 0 {
		return nil
	}
	return coreapi.NewContentValidationError(details)
}
