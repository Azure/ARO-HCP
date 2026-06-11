// Copyright 2025 Microsoft Corporation
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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"time"

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilsclock "k8s.io/utils/clock"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mc"
	"github.com/Azure/ARO-HCP/internal/recovery"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// MgmtClientFactory creates a controller-runtime client for a management cluster.
type MgmtClientFactory func(ctx context.Context, aksResourceID string, credential azcore.TokenCredential) (ctrlclient.Client, error)

func DefaultMgmtClientFactory(ctx context.Context, aksResourceID string, credential azcore.TokenCredential) (ctrlclient.Client, error) {
	config, err := mc.GetAKSRESTConfig(ctx, aksResourceID, credential)
	if err != nil {
		return nil, err
	}

	scheme := runtime.NewScheme()
	if err := recovery.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add DR schemes: %w", err)
	}
	client, err := ctrlclient.New(config, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller-runtime client: %w", err)
	}
	return client, nil
}

type drContext struct {
	resourceID string
	client     ctrlclient.Client
	hcp        *api.HCPOpenShiftCluster
	time       utilsclock.PassiveClock
}

// resolveDRContext resolves all the necessary DR context for the request.
func resolveDRContext(
	request *http.Request,
	resourceDBClient database.ResourcesDBClient,
	fleetDBClient database.FleetDBClient,
	azureCredential azcore.TokenCredential,
	mgmtClientFactory MgmtClientFactory,
	clock utilsclock.PassiveClock,
) (*drContext, int, error) {
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("failed to get resource ID: %w", err)
	}

	hcp, err := resourceDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(request.Context(), resourceID.Name)
	if database.IsNotFoundError(err) {
		return nil, http.StatusNotFound, fmt.Errorf("HCP %s not found", resourceID.String())
	}

	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("failed to get HCP: %w", err)
	}

	if hcp.ServiceProviderProperties.ClusterServiceID == nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("cluster %s has no ClusterServiceID", resourceID.String())
	}

	spc, err := database.GetOrCreateServiceProviderCluster(request.Context(), resourceDBClient, resourceID)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("failed to get ServiceProviderCluster: %w", err)
	}

	if spc.Status.ManagementClusterResourceID == nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("management cluster placement not resolved for cluster %s", resourceID.String())
	}

	stampID := spc.Status.ManagementClusterResourceID.Parent.Name
	mc, err := fleetDBClient.Stamps().ManagementClusters(stampID).Get(request.Context(), fleet.ManagementClusterResourceName)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("failed to get management cluster for stamp %s: %w", stampID, err)
	}

	if mc.Status.AKSResourceID == nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("AKS resource ID not set for management cluster in stamp %s", stampID)
	}

	client, err := mgmtClientFactory(request.Context(), mc.Status.AKSResourceID.String(), azureCredential)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("failed to create management cluster client: %w", err)
	}

	return &drContext{resourceID: resourceID.String(), client: client, hcp: hcp, time: clock}, http.StatusOK, nil
}

// clusterServiceID returns the short cluster ID (last path segment of the CS ID).
func (d *drContext) clusterServiceID() string {
	return path.Base(d.hcp.ServiceProviderProperties.ClusterServiceID.String())
}

func getBackup(ctx context.Context, client ctrlclient.Client, backupName string) (*velerov1api.Backup, error) {
	backup := &velerov1api.Backup{}
	key := ctrlclient.ObjectKey{Name: backupName, Namespace: "velero"}
	if err := client.Get(ctx, key, backup); err != nil {
		return nil, err
	}
	return backup, nil
}

func listBackupsForCluster(ctx context.Context, client ctrlclient.Client, clusterID string) ([]velerov1api.Backup, error) {
	backupList := &velerov1api.BackupList{}
	if err := client.List(ctx, backupList, ctrlclient.InNamespace("velero"), ctrlclient.MatchingLabels{"api.openshift.com/id": clusterID}); err != nil {
		return nil, err
	}
	return backupList.Items, nil
}

func createBackupForCluster(ctx context.Context, client ctrlclient.Client, clusterID string, timestamp string) (*velerov1api.Backup, error) {
	hc, err := getHostedCluster(ctx, client, clusterID)
	if err != nil {
		return nil, err
	}

	backupName := fmt.Sprintf("%s-%s", clusterID, timestamp)
	hcpNamespace := fmt.Sprintf("%s-%s", hc.Namespace, hc.Name)

	ttl := 7 * 24 * time.Hour
	backup := recovery.NewBackup(backupName, clusterID, ttl, hc.Namespace, hcpNamespace)
	if err := client.Create(ctx, backup); err != nil {
		return nil, err
	}

	return backup, nil
}

func getHostedCluster(ctx context.Context, client ctrlclient.Client, clusterID string) (*hypershiftv1beta1.HostedCluster, error) {
	hostedClusters := &hypershiftv1beta1.HostedClusterList{}
	if err := client.List(ctx, hostedClusters, ctrlclient.MatchingLabels{"api.openshift.com/id": clusterID}); err != nil {
		return nil, err
	}
	if len(hostedClusters.Items) == 0 {
		return nil, fmt.Errorf("hosted cluster %s not found", clusterID)
	}
	if len(hostedClusters.Items) > 1 {
		return nil, fmt.Errorf("multiple hosted clusters found for cluster %s", clusterID)
	}
	return &hostedClusters.Items[0], nil
}

type GetBackupResponse struct {
	ResourceID string
	Backup     BackupResponse
}

func GetBackup(resourcesDBClient database.ResourcesDBClient, fleetDBClient database.FleetDBClient, azureCredential azcore.TokenCredential, mgmtClientFactory MgmtClientFactory, clock utilsclock.PassiveClock) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		drCtx, status, err := resolveDRContext(request, resourcesDBClient, fleetDBClient, azureCredential, mgmtClientFactory, clock)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to resolve DR context: %v", err), status)
			return
		}
		backupName := request.PathValue("backupName")
		if backupName == "" {
			http.Error(writer, "backupName is required", http.StatusBadRequest)
			return
		}

		veleroBackup, err := getBackup(request.Context(), drCtx.client, backupName)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				http.Error(writer, fmt.Sprintf("backup %s not found", backupName), http.StatusNotFound)
				return
			}
			http.Error(writer, fmt.Sprintf("failed to get backup: %v", err), http.StatusInternalServerError)
			return
		}

		clusterServiceID := drCtx.clusterServiceID()
		backupClusterID := veleroBackup.Labels["api.openshift.com/id"]
		if backupClusterID != clusterServiceID {
			http.Error(writer, fmt.Sprintf("backup not found for cluster %s", drCtx.resourceID), http.StatusNotFound)
			return
		}

		backup := newBackupResponse(*veleroBackup)

		response := GetBackupResponse{ResourceID: drCtx.hcp.ID.String(), Backup: backup}

		writer.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(writer).Encode(response)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to encode output: %v", err), http.StatusInternalServerError)
			return
		}
	})
}

func ListBackups(resourcesDBClient database.ResourcesDBClient, fleetDBClient database.FleetDBClient, azureCredential azcore.TokenCredential, mgmtClientFactory MgmtClientFactory, clock utilsclock.PassiveClock) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		drCtx, status, err := resolveDRContext(request, resourcesDBClient, fleetDBClient, azureCredential, mgmtClientFactory, clock)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to resolve DR context: %v", err), status)
			return
		}

		clusterServiceID := drCtx.clusterServiceID()
		backups, err := listBackupsForCluster(request.Context(), drCtx.client, clusterServiceID)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to list backups: %v", err), http.StatusInternalServerError)
			return
		}

		backupsOut := newListBackupsResponse(backups)
		response := ListBackupsResponse{ResourceID: drCtx.hcp.ID.String(), Backups: backupsOut}

		writer.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(writer).Encode(response)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to encode output: %v", err), http.StatusInternalServerError)
			return
		}
	})
}

func CreateBackup(resourcesDBClient database.ResourcesDBClient, fleetDBClient database.FleetDBClient, azureCredential azcore.TokenCredential, mgmtClientFactory MgmtClientFactory, clock utilsclock.PassiveClock) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		drCtx, status, err := resolveDRContext(request, resourcesDBClient, fleetDBClient, azureCredential, mgmtClientFactory, clock)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to resolve DR context: %v", err), status)
			return
		}

		clusterServiceID := drCtx.clusterServiceID()
		backup, err := createBackupForCluster(request.Context(), drCtx.client, clusterServiceID, drCtx.time.Now().Format("20060102150405"))
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to create backup: %v", err), http.StatusInternalServerError)
			return
		}

		response := newBackupResponse(*backup)

		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		err = json.NewEncoder(writer).Encode(response)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to encode output: %v", err), http.StatusInternalServerError)
			return
		}
	})
}

type ListBackupsResponse struct {
	ResourceID string           `json:"resourceID"`
	Backups    []BackupResponse `json:"backups"`
}

type BackupResponse struct {
	Name                string `json:"name"`
	StartTimestamp      string `json:"startTimestamp"`
	CompletionTimestamp string `json:"completionTimestamp"`
	Phase               string `json:"phase"`
}

func newListBackupsResponse(backups []velerov1api.Backup) []BackupResponse {
	out := make([]BackupResponse, len(backups))
	for i, b := range backups {
		out[i] = newBackupResponse(b)
	}
	return out
}

func newBackupResponse(backup velerov1api.Backup) BackupResponse {
	resp := BackupResponse{
		Name:  backup.Name,
		Phase: string(backup.Status.Phase),
	}
	if backup.Status.StartTimestamp != nil {
		resp.StartTimestamp = backup.Status.StartTimestamp.String()
	}
	if backup.Status.CompletionTimestamp != nil {
		resp.CompletionTimestamp = backup.Status.CompletionTimestamp.String()
	}
	return resp
}
