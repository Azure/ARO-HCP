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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mc"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/recovery"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type BackupClient interface {
	GetBackup(ctx context.Context, backupName string) (*velerov1api.Backup, error)
	ListBackupsForCluster(ctx context.Context, clusterId string) ([]velerov1api.Backup, error)
	CreateBackupForCluster(ctx context.Context, clusterId string) (*velerov1api.Backup, error)
}

type DrClientFactory func(ctx context.Context, aksResourceID string, credential azcore.TokenCredential) (BackupClient, error)

func DefaultDrClientFactory(ctx context.Context, aksResourceID string, credential azcore.TokenCredential) (BackupClient, error) {
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
	return recovery.NewDrClient(client), nil
}

type drContext struct {
	resourceID string
	drClient   BackupClient
	hcp        *api.HCPOpenShiftCluster
}

// Resolves all the necessary DR context for the request.
func resolveDRContext(
	request *http.Request,
	dbClient database.DBClient,
	csClient ocm.ClusterServiceClientSpec,
	azureCredential azcore.TokenCredential,
	drClientFactory DrClientFactory,
) (*drContext, int, error) {
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("failed to get resource ID: %w", err)
	}

	hcp, err := dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(request.Context(), resourceID.Name)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil, http.StatusNotFound, fmt.Errorf("HCP %s not found", resourceID.String())
	}

	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("failed to get HCP: %w", err)
	}

	shard, err := csClient.GetClusterProvisionShard(request.Context(), hcp.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("failed to get Management Cluster: %w", err)
	}

	drClient, err := drClientFactory(request.Context(), shard.AzureShard().AksManagementClusterResourceId(), azureCredential)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("failed to create DR client: %w", err)
	}

	return &drContext{resourceID: resourceID.String(), drClient: drClient, hcp: hcp}, http.StatusOK, nil
}

type GetBackupResponse struct {
	ResourceID string
	Backup     BackupResponse
}

func GetBackup(dbClient database.DBClient, csClient ocm.ClusterServiceClientSpec, azureCredential azcore.TokenCredential, drClientFactory DrClientFactory) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		drContext, status, err := resolveDRContext(request, dbClient, csClient, azureCredential, drClientFactory)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to resolve DR context: %v", err), status)
			return
		}
		backupName := request.PathValue("backupName")
		if backupName == "" {
			http.Error(writer, "backupName is required", http.StatusBadRequest)
			return
		}

		veleroBackup, err := drContext.drClient.GetBackup(request.Context(), backupName)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get backup: %v", err), http.StatusInternalServerError)
			return
		}

		expectedID := path.Base(drContext.hcp.ServiceProviderProperties.ClusterServiceID.String())
		backupClusterID := veleroBackup.Labels["api.openshift.com/id"]
		if backupClusterID != expectedID {
			http.Error(writer, fmt.Sprintf("backup %s does not belong to cluster %s", backupName, drContext.hcp.ID.String()), http.StatusBadRequest)
			return
		}

		backup := newBackupResponse(*veleroBackup)

		response := GetBackupResponse{ResourceID: drContext.hcp.ID.String(), Backup: backup}

		writer.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(writer).Encode(response)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to encode output: %v", err), http.StatusInternalServerError)
			return
		}
	})
}

func ListBackups(dbClient database.DBClient, csClient ocm.ClusterServiceClientSpec, azureCredential azcore.TokenCredential, drClientFactory DrClientFactory) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		drContext, status, err := resolveDRContext(request, dbClient, csClient, azureCredential, drClientFactory)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to resolve DR context: %v", err), status)
			return
		}

		id := path.Base(drContext.hcp.ServiceProviderProperties.ClusterServiceID.String())
		backups, err := drContext.drClient.ListBackupsForCluster(request.Context(), id)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to list backups: %v", err), http.StatusInternalServerError)
			return
		}

		backupsOut := newListBackupsResponse(backups)
		response := ListBackupsResponse{ResourceID: drContext.hcp.ID.String(), Backups: backupsOut}

		writer.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(writer).Encode(response)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to encode output: %v", err), http.StatusInternalServerError)
			return
		}
	})
}

func CreateBackup(dbClient database.DBClient, csClient ocm.ClusterServiceClientSpec, azureCredential azcore.TokenCredential, drClientFactory DrClientFactory) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		drContext, status, err := resolveDRContext(request, dbClient, csClient, azureCredential, drClientFactory)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to resolve DR context: %v", err), status)
			return
		}

		id := path.Base(drContext.hcp.ServiceProviderProperties.ClusterServiceID.String())
		backup, err := drContext.drClient.CreateBackupForCluster(request.Context(), id)
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
