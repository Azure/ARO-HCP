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

type hcpContext struct {
	resourceID *azcorearm.ResourceID
	hcp        *api.HCPOpenShiftCluster
	spc        *api.ServiceProviderCluster
}

func resolveHCPContext(
	request *http.Request,
	resourceDBClient database.ResourcesDBClient,
) (*hcpContext, error) {
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

	return &hcpContext{
		resourceID: resourceID,
		hcp:        hcp,
		spc:        spc,
	}, nil
}

func (b *hcpContext) clusterServiceID() string {
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
		hcpCtx, err := resolveHCPContext(request, resourcesDBClient)
		if err != nil {
			return fmt.Errorf("failed to resolve HCP context: %w", err)
		}

		backupName := request.PathValue("backupName")
		if backupName == "" {
			return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "backupName is required")
		}

		if hcpCtx.spc.Status.ManagementClusterResourceID == nil {
			return arm.NewCloudError(http.StatusPreconditionFailed, arm.CloudErrorCodeInvalidResource, "",
				"management cluster placement not resolved for cluster %s", hcpCtx.resourceID.String())
		}

		kaClient := kubeApplierDBClients.For(request.Context(), hcpCtx.spc.Status.ManagementClusterResourceID)
		if kaClient == nil {
			return arm.NewCloudError(http.StatusPreconditionFailed, arm.CloudErrorCodeInvalidResource, "",
				"kube-applier client not available for management cluster %s", hcpCtx.spc.Status.ManagementClusterResourceID.String())
		}

		rdCrud, err := kaClient.ReadDesiresForCluster(hcpCtx.resourceID.SubscriptionID, hcpCtx.resourceID.ResourceGroupName, hcpCtx.resourceID.Name)
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

		clusterBackup := backupResponseFromReadDesire(rd, backupName)
		response := api.GetBackupResponse{ResourceID: hcpCtx.resourceID.String(), Backup: clusterBackup}

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
	var clusterBackup velerov1api.Backup
	if err := json.Unmarshal(rd.Status.KubeContent.Raw, &clusterBackup); err != nil {
		resp.Phase = "Unknown"
		return resp
	}
	resp.Phase = string(clusterBackup.Status.Phase)
	if clusterBackup.Status.StartTimestamp != nil {
		resp.StartTimestamp = clusterBackup.Status.StartTimestamp.Time.UTC().Format(time.RFC3339)
	}
	if clusterBackup.Status.CompletionTimestamp != nil {
		resp.CompletionTimestamp = clusterBackup.Status.CompletionTimestamp.Time.UTC().Format(time.RFC3339)
	}
	return resp
}

func CreateBackup(
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	clock utilsclock.PassiveClock,
) func(http.ResponseWriter, *http.Request) error {
	return func(writer http.ResponseWriter, request *http.Request) error {
		hcpCtx, err := resolveHCPContext(request, resourcesDBClient)
		if err != nil {
			return fmt.Errorf("failed to resolve HCP context: %w", err)
		}

		clusterServiceID := hcpCtx.clusterServiceID()
		clusterName := hcpCtx.hcp.CustomerProperties.DNS.BaseDomainPrefix
		if clusterName == "" {
			return fmt.Errorf("cluster has no BaseDomainPrefix")
		}

		if hcpCtx.spc.Status.ManagementClusterResourceID == nil {
			return arm.NewCloudError(http.StatusPreconditionFailed, arm.CloudErrorCodeInvalidResource, "",
				"management cluster placement not resolved for cluster %s", hcpCtx.resourceID.String())
		}

		kaClient := kubeApplierDBClients.For(request.Context(), hcpCtx.spc.Status.ManagementClusterResourceID)
		if kaClient == nil {
			return arm.NewCloudError(http.StatusPreconditionFailed, arm.CloudErrorCodeInvalidResource, "",
				"kube-applier client not available for management cluster %s", hcpCtx.spc.Status.ManagementClusterResourceID.String())
		}

		rdCrud, err := kaClient.ReadDesiresForCluster(hcpCtx.resourceID.SubscriptionID, hcpCtx.resourceID.ResourceGroupName, hcpCtx.resourceID.Name)
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

		timestamp := clock.Now().UTC().Format("20060102150405")
		backupName := fmt.Sprintf("%s-%s", clusterServiceID, timestamp)
		ttl := 7 * 24 * time.Hour
		hcpBackup := backup.NewBackup(backupName, clusterServiceID, ttl, hcNamespace, hcpNamespace)

		adCrud, err := kaClient.ApplyDesiresForCluster(hcpCtx.resourceID.SubscriptionID, hcpCtx.resourceID.ResourceGroupName, hcpCtx.resourceID.Name)
		if err != nil {
			return fmt.Errorf("failed to get ApplyDesire CRUD: %w", err)
		}

		ad, rd, err := buildOnDemandBackupDesires(
			hcpCtx.resourceID.SubscriptionID, hcpCtx.resourceID.ResourceGroupName, hcpCtx.resourceID.Name,
			backupName, hcpCtx.spc.Status.ManagementClusterResourceID, hcpBackup,
		)
		if err != nil {
			return fmt.Errorf("failed to build desires: %w", err)
		}

		// RD is created first. If it fails, nothing is written and no cleanup is needed.
		// RD is intentionally created before AD: if AD creation fails after this point,
		// the orphaned RD is harmless — kube-applier will observe the absent Velero Backup
		// and set Successful=True with nil KubeContent, which causes ondemand_cleanup_controller
		// to delete the RD during its next reconcile (cleanupCompletedOnDemandBackupDesires,
		// the "ApplyDesire is gone" path).
		if _, err := rdCrud.Create(request.Context(), rd, nil); err != nil {
			if database.IsConflictError(err) {
				return arm.NewCloudError(http.StatusConflict, arm.CloudErrorCodeConflict, "", "backup %s already exists", backupName)
			}
			return fmt.Errorf("failed to create ReadDesire: %w", err)
		}
		if _, err := adCrud.Create(request.Context(), ad, nil); err != nil {
			if database.IsConflictError(err) {
				return arm.NewCloudError(http.StatusConflict, arm.CloudErrorCodeConflict, "", "backup %s already exists", backupName)
			}
			return fmt.Errorf("failed to create ApplyDesire: %w", err)
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
	ctx, err := resolveHCPContext(request, h.resourcesDBClient)
	if err != nil {
		return fmt.Errorf("failed to resolve HCP context: %w", err)
	}

	state := ctx.spc.Spec.BackupState
	if state == "" {
		state = api.BackupScheduleStateEnabled
	}

	response := api.BackupScheduleResponse{
		ResourceID: ctx.resourceID.String(),
		State:      state,
		Schedules:  []api.BackupScheduleDetail{},
	}

	if ctx.spc.Status.ManagementClusterResourceID == nil {
		return arm.NewCloudError(http.StatusPreconditionFailed, arm.CloudErrorCodeInvalidResource, "",
			"management cluster placement not resolved for cluster %s", ctx.resourceID.String())
	}

	kaClient := h.kubeApplierDBClients.For(request.Context(), ctx.spc.Status.ManagementClusterResourceID)
	if kaClient == nil {
		return arm.NewCloudError(http.StatusPreconditionFailed, arm.CloudErrorCodeInvalidResource, "",
			"kube-applier client not available for management cluster %s", ctx.spc.Status.ManagementClusterResourceID.String())
	}

	rdCrud, err := kaClient.ReadDesiresForCluster(ctx.resourceID.SubscriptionID, ctx.resourceID.ResourceGroupName, ctx.resourceID.Name)
	if err != nil {
		return fmt.Errorf("failed to get ReadDesire CRUD: %w", err)
	}

	iterator, err := rdCrud.List(request.Context(), nil)
	if err != nil {
		return fmt.Errorf("failed to list ReadDesires: %w", err)
	}

	for _, rd := range iterator.Items(request.Context()) {
		if rd == nil || rd.ResourceID == nil {
			continue
		}
		if !strings.HasPrefix(rd.ResourceID.Name, backup.BackupScheduleDesireNamePrefix) {
			continue
		}
		scheduleName := strings.TrimPrefix(rd.ResourceID.Name, backup.BackupScheduleDesireNamePrefix)
		detail := api.BackupScheduleDetail{Name: scheduleName}
		if rd.Status.KubeContent != nil && rd.Status.KubeContent.Raw != nil {
			var schedule velerov1api.Schedule
			err := json.Unmarshal(rd.Status.KubeContent.Raw, &schedule)
			if err != nil {
				return fmt.Errorf("failed to unmarshal Schedule: %w", err)
			}
			if schedule.Status.LastBackup != nil {
				detail.LastBackupTime = schedule.Status.LastBackup.String()
			}
			detail.BackupSchedulePhase = string(schedule.Status.Phase)
			detail.Paused = schedule.Spec.Paused
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
	var patch api.BackupSchedulePatchRequest
	if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "invalid JSON body: %v", err)
	}

	if patch.State != api.BackupScheduleStateEnabled && patch.State != api.BackupScheduleStatePaused {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "invalid state %q: must be %q or %q", patch.State, api.BackupScheduleStateEnabled, api.BackupScheduleStatePaused)
	}

	ctx, err := resolveHCPContext(request, h.resourcesDBClient)
	if err != nil {
		return fmt.Errorf("failed to resolve HCP context: %w", err)
	}

	ctx.spc.Spec.BackupState = patch.State

	spcCRUD := h.resourcesDBClient.ServiceProviderClusters(ctx.resourceID.SubscriptionID, ctx.resourceID.ResourceGroupName, ctx.resourceID.Name)
	spc, err := spcCRUD.Replace(request.Context(), ctx.spc, nil)
	if err != nil {
		return fmt.Errorf("failed to update backup state: %w", err)
	}

	response := api.BackupSchedulePatchResponse{
		ResourceID: ctx.resourceID.String(),
		State:      spc.Spec.BackupState,
	}

	writer.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(writer).Encode(response)
}
