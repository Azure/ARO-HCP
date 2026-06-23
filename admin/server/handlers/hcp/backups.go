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
	"path"
	"strings"
	"time"

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	"k8s.io/apimachinery/pkg/runtime"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/backup"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var (
	hostedClusterReadDesireName = strings.ToLower(string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
)

const (
	veleroBackupGroup    = "velero.io"
	veleroBackupVersion  = "v1"
	veleroBackupResource = "backups"
	veleroNamespace      = "velero"
)

func ondemandDesireName(backupName string) string {
	return backup.OndemandBackupDesireNamePrefix + backupName
}

type backupContext struct {
	resourceID                  *azcorearm.ResourceID
	hcp                         *api.HCPOpenShiftCluster
	managementClusterResourceID *azcorearm.ResourceID
	time                        utilsclock.PassiveClock
}

func resolveBackupContext(
	request *http.Request,
	resourceDBClient database.ResourcesDBClient,
	clock utilsclock.PassiveClock,
) (*backupContext, error) {
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return nil, fmt.Errorf("failed to get resource ID: %w", err)
	}

	hcp, err := resourceDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(request.Context(), resourceID.Name)
	if err != nil {
		if database.IsNotFoundError(err) {
			return nil, arm.NewCloudError(http.StatusNotFound, arm.CloudErrorCodeResourceNotFound, "", "HCP %s not found", resourceID.String())
		}
		return nil, fmt.Errorf("failed to get HCP: %w", err)
	}

	if hcp.ServiceProviderProperties.ClusterServiceID == nil {
		return nil, fmt.Errorf("cluster %s has no ClusterServiceID", resourceID.String())
	}

	spc, err := database.GetOrCreateServiceProviderCluster(request.Context(), resourceDBClient, resourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get ServiceProviderCluster: %w", err)
	}

	if spc.Status.ManagementClusterResourceID == nil {
		return nil, arm.NewCloudError(http.StatusPreconditionFailed, arm.CloudErrorCodeInvalidResource, "",
			"management cluster placement not resolved for cluster %s", resourceID.String())
	}

	return &backupContext{
		resourceID:                  resourceID,
		hcp:                         hcp,
		managementClusterResourceID: spc.Status.ManagementClusterResourceID,
		time:                        clock,
	}, nil
}

func (b *backupContext) clusterServiceID() string {
	return path.Base(b.hcp.ServiceProviderProperties.ClusterServiceID.String())
}

func buildOnDemandBackupDesires(
	subscriptionID, resourceGroupName, clusterName, backupName string,
	mcResourceID *azcorearm.ResourceID,
	backup *velerov1api.Backup,
) (*kubeapplier.ApplyDesire, *kubeapplier.ReadDesire, error) {
	desireName := ondemandDesireName(backupName)

	adResourceIDStr := kubeapplier.ToClusterScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, desireName,
	)
	adResourceID, err := azcorearm.ParseResourceID(adResourceIDStr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse ApplyDesire resource ID: %w", err)
	}

	raw, err := json.Marshal(backup)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal backup: %w", err)
	}

	partitionKey := strings.ToLower(mcResourceID.String())

	ad := &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: adResourceID, PartitionKey: partitionKey},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: mcResourceID,
			Type:              kubeapplier.ApplyDesireTypeServerSideApply,
			TargetItem: kubeapplier.ResourceReference{
				Group:     veleroBackupGroup,
				Version:   veleroBackupVersion,
				Resource:  veleroBackupResource,
				Namespace: veleroNamespace,
				Name:      backupName,
			},
			ServerSideApply: &kubeapplier.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: raw},
			},
		},
	}

	rdResourceIDStr := kubeapplier.ToClusterScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, desireName,
	)
	rdResourceID, err := azcorearm.ParseResourceID(rdResourceIDStr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse ReadDesire resource ID: %w", err)
	}

	rd := &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: rdResourceID, PartitionKey: partitionKey},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: mcResourceID,
			TargetItem: kubeapplier.ResourceReference{
				Group:     veleroBackupGroup,
				Version:   veleroBackupVersion,
				Resource:  veleroBackupResource,
				Namespace: veleroNamespace,
				Name:      backupName,
			},
		},
	}

	return ad, rd, nil
}

func GetBackup(
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	clock utilsclock.PassiveClock,
) func(http.ResponseWriter, *http.Request) error {
	return func(writer http.ResponseWriter, request *http.Request) error {
		bCtx, err := resolveBackupContext(request, resourcesDBClient, clock)
		if err != nil {
			return fmt.Errorf("failed to resolve backup context: %w", err)
		}

		backupName := request.PathValue("backupName")
		if backupName == "" {
			return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "backupName is required")
		}

		kaClient := kubeApplierDBClients.For(request.Context(), bCtx.managementClusterResourceID)
		if kaClient == nil {
			return arm.NewCloudError(http.StatusPreconditionFailed, arm.CloudErrorCodeInvalidResource, "",
				"kube-applier client not available for management cluster %s", bCtx.managementClusterResourceID.String())
		}

		rdCrud, err := kaClient.ReadDesiresForCluster(bCtx.resourceID.SubscriptionID, bCtx.resourceID.ResourceGroupName, bCtx.resourceID.Name)
		if err != nil {
			return fmt.Errorf("failed to get ReadDesire CRUD: %w", err)
		}

		desireName := ondemandDesireName(backupName)
		rd, err := rdCrud.Get(request.Context(), desireName)
		if err != nil {
			if database.IsNotFoundError(err) {
				return arm.NewCloudError(http.StatusNotFound, arm.CloudErrorCodeResourceNotFound, "", "backup %s not found", backupName)
			}
			return fmt.Errorf("failed to get ReadDesire: %w", err)
		}

		backup := backupResponseFromReadDesire(rd, backupName)
		response := api.GetBackupResponse{ResourceID: bCtx.hcp.ID.String(), Backup: backup}

		writer.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(writer).Encode(response)
	}
}

func backupResponseFromReadDesire(rd *kubeapplier.ReadDesire, backupName string) api.BackupResponse {
	resp := api.BackupResponse{
		Name:  backupName,
		Phase: "New",
	}
	if rd.Status.KubeContent == nil || rd.Status.KubeContent.Raw == nil {
		return resp
	}
	var backup velerov1api.Backup
	if err := json.Unmarshal(rd.Status.KubeContent.Raw, &backup); err != nil {
		resp.Phase = "Unknown"
		return resp
	}
	resp.Phase = string(backup.Status.Phase)
	if backup.Status.StartTimestamp != nil {
		resp.StartTimestamp = backup.Status.StartTimestamp.String()
	}
	if backup.Status.CompletionTimestamp != nil {
		resp.CompletionTimestamp = backup.Status.CompletionTimestamp.String()
	}
	return resp
}

func CreateBackup(
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	clock utilsclock.PassiveClock,
) func(http.ResponseWriter, *http.Request) error {
	return func(writer http.ResponseWriter, request *http.Request) error {
		bCtx, err := resolveBackupContext(request, resourcesDBClient, clock)
		if err != nil {
			return fmt.Errorf("failed to resolve backup context: %w", err)
		}

		clusterServiceID := bCtx.clusterServiceID()
		clusterName := bCtx.hcp.CustomerProperties.DNS.BaseDomainPrefix
		if clusterName == "" {
			return fmt.Errorf("cluster has no BaseDomainPrefix")
		}

		kaClient := kubeApplierDBClients.For(request.Context(), bCtx.managementClusterResourceID)
		if kaClient == nil {
			return arm.NewCloudError(http.StatusPreconditionFailed, arm.CloudErrorCodeInvalidResource, "",
				"kube-applier client not available for management cluster %s", bCtx.managementClusterResourceID.String())
		}

		rdCrud, err := kaClient.ReadDesiresForCluster(bCtx.resourceID.SubscriptionID, bCtx.resourceID.ResourceGroupName, bCtx.resourceID.Name)
		if err != nil {
			return fmt.Errorf("failed to get ReadDesire CRUD: %w", err)
		}

		hcReadDesire, err := rdCrud.Get(request.Context(), hostedClusterReadDesireName)
		if err != nil {
			if database.IsNotFoundError(err) {
				return arm.NewCloudError(http.StatusPreconditionFailed, arm.CloudErrorCodeInvalidResource, "", "HostedCluster ReadDesire not found — cluster may not be fully provisioned")
			}
			return fmt.Errorf("failed to get HostedCluster ReadDesire: %w", err)
		}

		hcNamespace := hcReadDesire.Spec.TargetItem.Namespace
		if hcNamespace == "" {
			return fmt.Errorf("HostedCluster ReadDesire has empty namespace")
		}
		hcpNamespace := fmt.Sprintf("%s-%s", hcNamespace, clusterName)

		timestamp := bCtx.time.Now().Format("20060102150405")
		backupName := fmt.Sprintf("%s-%s", clusterServiceID, timestamp)
		ttl := 7 * 24 * time.Hour
		hcpBackup := backup.NewBackup(backupName, clusterServiceID, ttl, hcNamespace, hcpNamespace)

		adCrud, err := kaClient.ApplyDesiresForCluster(bCtx.resourceID.SubscriptionID, bCtx.resourceID.ResourceGroupName, bCtx.resourceID.Name)
		if err != nil {
			return fmt.Errorf("failed to get ApplyDesire CRUD: %w", err)
		}

		ad, rd, err := buildOnDemandBackupDesires(
			bCtx.resourceID.SubscriptionID, bCtx.resourceID.ResourceGroupName, bCtx.resourceID.Name,
			backupName, bCtx.managementClusterResourceID, hcpBackup,
		)
		if err != nil {
			return fmt.Errorf("failed to build desires: %w", err)
		}

		if _, err := adCrud.Create(request.Context(), ad, nil); err != nil {
			return fmt.Errorf("failed to create ApplyDesire: %w", err)
		}
		if _, err := rdCrud.Create(request.Context(), rd, nil); err != nil {
			return fmt.Errorf("failed to create ReadDesire: %w", err)
		}

		response := api.BackupResponse{
			Name:  backupName,
			Phase: "New",
		}

		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		return json.NewEncoder(writer).Encode(response)
	}
}

// HCPGetBackupScheduleHandler handles GET requests for backup schedule.
type HCPGetBackupScheduleHandler struct {
	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients
}

// NewHCPGetBackupScheduleHandler creates a new backup schedule GET handler.
func NewHCPGetBackupScheduleHandler(resourcesDBClient database.ResourcesDBClient, kubeApplierDBClients database.KubeApplierDBClients) *HCPGetBackupScheduleHandler {
	return &HCPGetBackupScheduleHandler{
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
	}
}

func (h *HCPGetBackupScheduleHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return fmt.Errorf("failed to get resource ID: %w", err)
	}

	spc, err := database.GetOrCreateServiceProviderCluster(request.Context(), h.resourcesDBClient, resourceID)
	if err != nil {
		return fmt.Errorf("failed to get service provider cluster: %w", err)
	}

	state := spc.Spec.BackupState
	if state == "" {
		state = api.BackupScheduleStateEnabled
	}

	response := api.BackupScheduleResponse{
		ResourceID: resourceID.String(),
		State:      state,
		Schedules:  []api.BackupScheduleDetail{},
	}

	if spc.Status.ManagementClusterResourceID == nil {
		return arm.NewCloudError(http.StatusPreconditionFailed, arm.CloudErrorCodeInvalidResource, "",
			"management cluster placement not resolved for cluster %s", resourceID.String())
	}

	kaClient := h.kubeApplierDBClients.For(request.Context(), spc.Status.ManagementClusterResourceID)
	if kaClient == nil {
		return arm.NewCloudError(http.StatusPreconditionFailed, arm.CloudErrorCodeInvalidResource, "",
			"kube-applier client not available for management cluster %s", spc.Status.ManagementClusterResourceID.String())
	}

	rdCrud, err := kaClient.ReadDesiresForCluster(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name)
	if err != nil {
		return fmt.Errorf("failed to get ReadDesire CRUD: %w", err)
	}

	iterator, err := rdCrud.List(request.Context(), nil)
	if err != nil {
		return fmt.Errorf("failed to list ReadDesires: %w", err)
	}

	for _, rd := range iterator.Items(request.Context()) {
		if !strings.HasPrefix(rd.ResourceID.Name, backup.BackupScheduleDesireNamePrefix) {
			continue
		}
		scheduleName := strings.TrimPrefix(rd.ResourceID.Name, backup.BackupScheduleDesireNamePrefix)
		detail := api.BackupScheduleDetail{Name: scheduleName}
		if rd.Status.KubeContent != nil && rd.Status.KubeContent.Raw != nil {
			var schedule velerov1api.Schedule
			if err := json.Unmarshal(rd.Status.KubeContent.Raw, &schedule); err == nil {
				if schedule.Status.LastBackup != nil {
					detail.LastBackupTime = schedule.Status.LastBackup.String()
				}
				detail.BackupSchedulePhase = string(schedule.Status.Phase)
				detail.Paused = schedule.Spec.Paused
			}
		}
		response.Schedules = append(response.Schedules, detail)
	}
	if err := iterator.GetError(); err != nil {
		return fmt.Errorf("failed to iterate ReadDesires: %w", err)
	}

	writer.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(writer).Encode(response)
}

// HCPPatchBackupScheduleHandler handles PATCH requests to update backup schedule state.
type HCPPatchBackupScheduleHandler struct {
	resourcesDBClient database.ResourcesDBClient
}

// NewHCPPatchBackupScheduleHandler creates a new backup schedule PATCH handler.
func NewHCPPatchBackupScheduleHandler(resourcesDBClient database.ResourcesDBClient) *HCPPatchBackupScheduleHandler {
	return &HCPPatchBackupScheduleHandler{resourcesDBClient: resourcesDBClient}
}

func (h *HCPPatchBackupScheduleHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return fmt.Errorf("failed to get resource ID: %w", err)
	}

	var patch api.BackupSchedulePatchRequest
	if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "invalid JSON body: %v", err)
	}

	if patch.State != api.BackupScheduleStateEnabled && patch.State != api.BackupScheduleStatePaused {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "invalid state %q: must be %q or %q", patch.State, api.BackupScheduleStateEnabled, api.BackupScheduleStatePaused)
	}

	spc, err := database.GetOrCreateServiceProviderCluster(request.Context(), h.resourcesDBClient, resourceID)
	if err != nil {
		return fmt.Errorf("failed to get service provider cluster: %w", err)
	}

	spc.Spec.BackupState = patch.State

	spcCRUD := h.resourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name)
	spc, err = spcCRUD.Replace(request.Context(), spc, nil)
	if err != nil {
		return fmt.Errorf("failed to update backup state: %w", err)
	}

	response := api.BackupSchedulePatchResponse{
		ResourceID: resourceID.String(),
		State:      spc.Spec.BackupState,
	}

	writer.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(writer).Encode(response)
}
