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

	"github.com/google/uuid"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type RestoreRequest struct {
	BackupID   string `json:"backupID"`
	RecoveryID string `json:"recoveryID"`
}

type RestoreResponse struct {
	ResourceID    string `json:"resourceID"`
	RecoveryID    string `json:"recoveryID,omitempty"`
	RecoveryState string `json:"recoveryState"`
	BackupID      string `json:"backupID,omitempty"`
	StartedAt     string `json:"startedAt,omitempty"`
	CompletedAt   string `json:"completedAt,omitempty"`
}

func newRestoreResponse(resourceID string, recoveryID string, spc *coreapi.ServiceProviderCluster) RestoreResponse {
	resp := RestoreResponse{
		ResourceID: resourceID,
		RecoveryID: recoveryID,
	}
	for _, recovery := range spc.Status.Recoveries {
		if recovery.RecoveryId == recoveryID {
			resp.RecoveryState = string(recovery.State)
			if recovery.StartedAt != nil {
				resp.StartedAt = recovery.StartedAt.String()
			}
			if recovery.CompletedAt != nil {
				resp.CompletedAt = recovery.CompletedAt.String()
			}
			break
		}
	}
	for _, req := range spc.Spec.RecoveryRequests {
		if req.RecoveryId == recoveryID {
			resp.BackupID = req.BackupId
			break
		}
	}
	// Request exists in Spec but not yet picked up by the controller.
	if resp.RecoveryState == "" && resp.BackupID != "" {
		resp.RecoveryState = string(coreapi.RecoveryStatePending)
	}
	return resp
}

func PostRestore(dbClient corecosmosstorage.ResourcesDBClient) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		resourceID, err := utils.ResourceIDFromContext(request.Context())
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get resource ID: %v", err), http.StatusInternalServerError)
			return
		}

		var req RestoreRequest
		if err := json.NewDecoder(request.Body).Decode(&req); err != nil {
			http.Error(writer, fmt.Sprintf("invalid JSON body: %v", err), http.StatusBadRequest)
			return
		}

		if req.BackupID == "" {
			http.Error(writer, "backupID is required", http.StatusBadRequest)
			return
		}

		spc, err := corecosmosstorage.GetOrCreateServiceProviderCluster(request.Context(), dbClient, resourceID)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get service provider cluster: %v", err), http.StatusInternalServerError)
			return
		}

		terminalRecoveryIDs := make(map[string]bool)
		for _, recovery := range spc.Status.Recoveries {
			if !isTerminal(recovery.State) {
				http.Error(writer, fmt.Sprintf("recovery already in progress (id: %s, state: %s)", recovery.RecoveryId, recovery.State), http.StatusConflict)
				return
			}
			terminalRecoveryIDs[recovery.RecoveryId] = true
		}
		for _, pending := range spc.Spec.RecoveryRequests {
			if !terminalRecoveryIDs[pending.RecoveryId] {
				http.Error(writer, fmt.Sprintf("recovery already queued (id: %s)", pending.RecoveryId), http.StatusConflict)
				return
			}
		}

		recoveryID := uuid.New().String()
		spc.Spec.RecoveryRequests = append(spc.Spec.RecoveryRequests, coreapi.RecoveryRequest{
			RecoveryId: recoveryID,
			BackupId:   req.BackupID,
		})

		spcCRUD := dbClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name)
		spc, err = spcCRUD.Replace(request.Context(), spc, nil)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to update recovery state: %v", err), http.StatusInternalServerError)
			return
		}

		response := newRestoreResponse(resourceID.String(), recoveryID, spc)

		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			http.Error(writer, fmt.Sprintf("failed to encode output: %v", err), http.StatusInternalServerError)
			return
		}
	})
}

func GetRestoreStatus(dbClient corecosmosstorage.ResourcesDBClient) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		resourceID, err := utils.ResourceIDFromContext(request.Context())
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get resource ID: %v", err), http.StatusInternalServerError)
			return
		}

		recoveryID := request.URL.Query().Get("recoveryID")
		if recoveryID == "" {
			http.Error(writer, "recoveryID query parameter is required", http.StatusBadRequest)
			return
		}

		spc, err := corecosmosstorage.GetOrCreateServiceProviderCluster(request.Context(), dbClient, resourceID)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get service provider cluster: %v", err), http.StatusInternalServerError)
			return
		}

		response := newRestoreResponse(resourceID.String(), recoveryID, spc)

		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			http.Error(writer, fmt.Sprintf("failed to encode output: %v", err), http.StatusInternalServerError)
			return
		}
	})
}

func isTerminal(state coreapi.RecoveryState) bool {
	return state == coreapi.RecoveryStateCompleted || state == coreapi.RecoveryStateFailed
}
