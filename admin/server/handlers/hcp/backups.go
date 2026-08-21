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
	"strings"
	"time"

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/backup"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// BackupScheduleResponse is the JSON response for backup schedule endpoints.
type BackupScheduleResponse struct {
	State     coreapi.BackupScheduleState `json:"state"`
	Schedules []BackupScheduleDetail      `json:"schedules"`
}

// BackupScheduleDetail holds per-schedule status from the Velero Schedule ReadDesire.
type BackupScheduleDetail struct {
	Name           string `json:"name"`
	LastBackupTime string `json:"lastBackupTime,omitempty"`
	// Phase maps to Velero schedule.state.phase. Possible values "New", "Enabled", "FailedValidation"
	Phase string `json:"phase,omitempty"`
	// BackupExecutionState maps to Velero schedule.spec.pause. Possible values: "Active"(paused=false) and "Paused"(paused=true)
	BackupExecutionState BackupExecutionState `json:"backupExecutionState,omitempty"`
}

type BackupExecutionState string

const (
	BackupExecutionStateActive BackupExecutionState = "Active"
	BackupExecutionStatePaused BackupExecutionState = "Paused"
)

// BackupSchedulePatchRequest is the JSON body for PATCH backup schedule requests.
type BackupSchedulePatchRequest struct {
	State coreapi.BackupScheduleState `json:"state"`
}

// BackupSchedulePatchResponse is the JSON response for PATCH backup schedule requests.
type BackupSchedulePatchResponse struct {
	State coreapi.BackupScheduleState `json:"state"`
}

type clusterDetails struct {
	hcpOpenShiftCluster    *coreapi.HCPOpenShiftCluster
	serviceProviderCluster *coreapi.ServiceProviderCluster
}

func getClusterDetails(
	request *http.Request,
	resourceDBClient corecosmosstorage.ResourcesDBClient,
) (*clusterDetails, error) {
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return nil, fmt.Errorf("failed to get resource ID: %w", err)
	}

	hcpOpenShiftCluster, err := resourceDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(request.Context(), resourceID.Name)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil, coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeResourceNotFound, "", "HCP %s not found", resourceID.String())
		}
		return nil, fmt.Errorf("failed to get HCP: %w", err)
	}

	serviceProviderCluster, err := resourceDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(request.Context(), coreapi.ServiceProviderClusterResourceName)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil, coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeResourceNotFound, "", "ServiceProviderCluster not found for %s", resourceID.String())
		}
		return nil, fmt.Errorf("failed to get ServiceProviderCluster: %w", err)
	}

	return &clusterDetails{
		hcpOpenShiftCluster:    hcpOpenShiftCluster,
		serviceProviderCluster: serviceProviderCluster,
	}, nil
}

// HCPGetBackupScheduleHandler handles GET requests for backup schedule.
type HCPGetBackupScheduleHandler struct {
	resourcesDBClient    corecosmosstorage.ResourcesDBClient
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients
}

// NewHCPGetBackupScheduleHandler creates a new backup schedule GET handler.
func NewHCPGetBackupScheduleHandler(resourcesDBClient corecosmosstorage.ResourcesDBClient, kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients) *HCPGetBackupScheduleHandler {
	return &HCPGetBackupScheduleHandler{
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
	}
}

func (h *HCPGetBackupScheduleHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	clusterDetails, err := getClusterDetails(request, h.resourcesDBClient)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to resolve HCP context: %w", err))
	}

	state := clusterDetails.serviceProviderCluster.Spec.BackupScheduleState
	if state == "" {
		state = coreapi.BackupScheduleStateEnabled
	}

	response := BackupScheduleResponse{
		State:     state,
		Schedules: []BackupScheduleDetail{},
	}

	if clusterDetails.serviceProviderCluster.Status.ManagementClusterResourceID == nil {
		return coreapi.NewCloudError(http.StatusPreconditionFailed, coreapi.CloudErrorCodeInvalidResource, "",
			"management cluster placement not resolved for cluster %s", clusterDetails.hcpOpenShiftCluster.ResourceID.String())
	}

	kubeApplierClient := h.kubeApplierDBClients.For(request.Context(), clusterDetails.serviceProviderCluster.Status.ManagementClusterResourceID)
	if kubeApplierClient == nil {
		return coreapi.NewCloudError(http.StatusPreconditionFailed, coreapi.CloudErrorCodeInvalidResource, "",
			"kube-applier client not available for management cluster %s", clusterDetails.serviceProviderCluster.Status.ManagementClusterResourceID.String())
	}

	readDesireCRUD, err := kubeApplierClient.ReadDesiresForCluster(clusterDetails.hcpOpenShiftCluster.ResourceID.SubscriptionID, clusterDetails.hcpOpenShiftCluster.ResourceID.ResourceGroupName, clusterDetails.hcpOpenShiftCluster.ResourceID.Name)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ReadDesire CRUD: %w", err))
	}

	iterator, err := readDesireCRUD.List(request.Context(), nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list ReadDesires: %w", err))
	}

	for _, readDesire := range iterator.Items(request.Context()) {
		if readDesire == nil || readDesire.ResourceID == nil {
			continue
		}
		if _, ok := readDesire.Tags[backup.DesireTagKeySchedule]; !ok {
			continue
		}
		scheduleName := strings.TrimPrefix(readDesire.ResourceID.Name, backup.BackupScheduleDesireNamePrefix)
		detail := BackupScheduleDetail{Name: scheduleName}
		if readDesire.Status.KubeContent != nil && readDesire.Status.KubeContent.Raw != nil {
			var schedule velerov1api.Schedule
			err := json.Unmarshal(readDesire.Status.KubeContent.Raw, &schedule)
			if err != nil {
				return utils.TrackError(fmt.Errorf("failed to unmarshal Schedule: %w", err))
			}
			if schedule.Status.LastBackup != nil {
				detail.LastBackupTime = schedule.Status.LastBackup.Time.UTC().Format(time.RFC3339)
			}
			detail.Phase = string(schedule.Status.Phase)
			if schedule.Spec.Paused {
				detail.BackupExecutionState = BackupExecutionStatePaused
			} else {
				detail.BackupExecutionState = BackupExecutionStateActive
			}
		}
		response.Schedules = append(response.Schedules, detail)
	}
	if err := iterator.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("failed to iterate ReadDesires: %w", err))
	}

	_, err = coreapi.WriteJSONResponse(writer, http.StatusOK, response)
	return utils.TrackError(err)
}

// HCPPatchBackupScheduleHandler handles PATCH requests to update backup schedule state.
type HCPPatchBackupScheduleHandler struct {
	resourcesDBClient corecosmosstorage.ResourcesDBClient
}

// NewHCPPatchBackupScheduleHandler creates a new backup schedule PATCH handler.
func NewHCPPatchBackupScheduleHandler(resourcesDBClient corecosmosstorage.ResourcesDBClient) *HCPPatchBackupScheduleHandler {
	return &HCPPatchBackupScheduleHandler{resourcesDBClient: resourcesDBClient}
}

func (h *HCPPatchBackupScheduleHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	var patch BackupSchedulePatchRequest
	if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "invalid JSON body: %v", err)
	}

	if patch.State != coreapi.BackupScheduleStateEnabled && patch.State != coreapi.BackupScheduleStateDisabled {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "", "invalid state %q: must be %q or %q", patch.State, coreapi.BackupScheduleStateEnabled, coreapi.BackupScheduleStateDisabled)
	}

	clusterDetails, err := getClusterDetails(request, h.resourcesDBClient)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to resolve HCP context: %w", err))
	}

	clusterDetails.serviceProviderCluster.Spec.BackupScheduleState = patch.State

	serviceProviderClusterCRUD := h.resourcesDBClient.ServiceProviderClusters(clusterDetails.hcpOpenShiftCluster.ResourceID.SubscriptionID, clusterDetails.hcpOpenShiftCluster.ResourceID.ResourceGroupName, clusterDetails.hcpOpenShiftCluster.ResourceID.Name)
	serviceProviderCluster, err := serviceProviderClusterCRUD.Replace(request.Context(), clusterDetails.serviceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update backup state: %w", err))
	}

	response := BackupSchedulePatchResponse{
		State: serviceProviderCluster.Spec.BackupScheduleState,
	}

	_, err = coreapi.WriteJSONResponse(writer, http.StatusOK, response)
	return utils.TrackError(err)
}
