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
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// BackupProfileResponse is the JSON response for backup profile endpoints.
type BackupProfileResponse struct {
	ResourceID       string `json:"resourceID"`
	State            string `json:"state"`
	LastBackupTime   string `json:"lastBackupTime,omitempty"`
	LastBackupStatus string `json:"lastBackupStatus,omitempty"`
}

// BackupProfilePatchRequest is the JSON body for PATCH requests.
type BackupProfilePatchRequest struct {
	State string `json:"state"`
}

func newBackupProfileResponse(resourceID string, spc *api.ServiceProviderCluster) BackupProfileResponse {
	state := string(spc.Status.BackupState)
	if len(state) == 0 {
		state = string(api.BackupScheduleStateActive)
	}
	resp := BackupProfileResponse{
		ResourceID:       resourceID,
		State:            state,
		LastBackupStatus: spc.Status.LastBackupStatus,
	}
	if spc.Status.LastBackupTime != nil {
		resp.LastBackupTime = spc.Status.LastBackupTime.String()
	}
	return resp
}

// GetBackupProfile returns the backup profile for a cluster.
func GetBackupProfile(resourceDBClient database.ResourcesDBClient) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		resourceID, err := utils.ResourceIDFromContext(request.Context())
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get resource ID: %v", err), http.StatusInternalServerError)
			return
		}

		spc, err := database.GetOrCreateServiceProviderCluster(request.Context(), resourceDBClient, resourceID)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get service provider cluster: %v", err), http.StatusInternalServerError)
			return
		}

		response := newBackupProfileResponse(resourceID.String(), spc)

		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			http.Error(writer, fmt.Sprintf("failed to encode output: %v", err), http.StatusInternalServerError)
			return
		}
	})
}

// PatchBackupProfile updates the backup schedule state for a cluster.
// Only the "state" field can be updated; other fields are read-only.
func PatchBackupProfile(resourcesDBClient database.ResourcesDBClient) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		resourceID, err := utils.ResourceIDFromContext(request.Context())
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get resource ID: %v", err), http.StatusInternalServerError)
			return
		}

		var patch BackupProfilePatchRequest
		if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
			http.Error(writer, fmt.Sprintf("invalid JSON body: %v", err), http.StatusBadRequest)
			return
		}

		state := api.BackupScheduleState(patch.State)
		if state != api.BackupScheduleStateActive && state != api.BackupScheduleStatePaused {
			http.Error(writer, fmt.Sprintf("invalid state %q: must be %q or %q", patch.State, api.BackupScheduleStateActive, api.BackupScheduleStatePaused), http.StatusBadRequest)
			return
		}

		spc, err := database.GetOrCreateServiceProviderCluster(request.Context(), resourcesDBClient, resourceID)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get service provider cluster: %v", err), http.StatusInternalServerError)
			return
		}

		spc.Status.BackupState = state

		spcCRUD := resourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name)
		spc, err = spcCRUD.Replace(request.Context(), spc, nil)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to update backup state: %v", err), http.StatusInternalServerError)
			return
		}

		response := newBackupProfileResponse(resourceID.String(), spc)

		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			http.Error(writer, fmt.Sprintf("failed to encode output: %v", err), http.StatusInternalServerError)
			return
		}
	})
}
