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

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type stampApprovalRequest struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason"`
	Message  string `json:"message"`
}

type StampApprovalHandler struct {
	fleetDBClient database.FleetDBClient
}

func NewStampApprovalHandler(fleetDBClient database.FleetDBClient) *StampApprovalHandler {
	return &StampApprovalHandler{
		fleetDBClient: fleetDBClient,
	}
}

func (h *StampApprovalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	stampIdentifier := r.PathValue("stampIdentifier")

	var body stampApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return arm.NewCloudError(
			http.StatusBadRequest,
			arm.CloudErrorCodeInvalidRequestContent, "",
			"The request content was invalid and could not be deserialized: %q", err,
		)
	}

	if err := validateApprovalRequest(body); err != nil {
		return err
	}

	if _, err := fleet.ToStampResourceID(stampIdentifier); err != nil {
		return arm.NewCloudError(
			http.StatusBadRequest,
			arm.CloudErrorCodeInvalidRequestContent, "stampIdentifier",
			"Invalid stamp identifier: %q", stampIdentifier,
		)
	}

	stampsCRUD := h.fleetDBClient.Stamps()
	existing, err := stampsCRUD.Get(ctx, stampIdentifier)
	if err != nil {
		if database.IsNotFoundError(err) {
			return arm.NewCloudError(http.StatusNotFound, arm.CloudErrorCodeNotFound, "", "Stamp %q not found", stampIdentifier)
		}
		return utils.TrackError(fmt.Errorf("failed to get stamp: %w", err))
	}

	conditionStatus := metav1.ConditionFalse
	if body.Approved {
		conditionStatus = metav1.ConditionTrue
	}

	// Check if this is a no-op (idempotent)
	existingCondition := apimeta.FindStatusCondition(existing.Status.Conditions, string(fleet.StampConditionApproved))
	if existingCondition != nil &&
		existingCondition.Status == conditionStatus &&
		existingCondition.Reason == body.Reason &&
		existingCondition.Message == body.Message {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}

	updated := existing.DeepCopy()
	apimeta.SetStatusCondition(&updated.Status.Conditions, metav1.Condition{
		Type:               string(fleet.StampConditionApproved),
		Status:             conditionStatus,
		Reason:             body.Reason,
		Message:            body.Message,
		LastTransitionTime: metav1.NewTime(time.Now()),
	})

	if _, err := stampsCRUD.Replace(ctx, updated, existing, nil); err != nil {
		if database.IsPreconditionFailedError(err) {
			return arm.NewCloudError(http.StatusConflict, arm.CloudErrorCodeConflict, "", "ETag conflict, retry the operation")
		}
		return utils.TrackError(err)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

func validateApprovalRequest(body stampApprovalRequest) error {
	var details []arm.CloudErrorBody

	if len(body.Reason) == 0 {
		details = append(details, arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInvalidRequestContent,
			Target:  "reason",
			Message: "reason is required",
		})
	}
	if len(body.Message) == 0 {
		details = append(details, arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInvalidRequestContent,
			Target:  "message",
			Message: "message is required",
		})
	}

	if len(details) == 0 {
		return nil
	}
	return arm.NewContentValidationError(details)
}
