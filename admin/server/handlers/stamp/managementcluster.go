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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ManagementClusterStatus mirrors fleet.ManagementClusterStatus but
// serializes *azcorearm.ResourceID and *api.InternalID fields as strings.
type ManagementClusterStatus struct {
	Conditions                                           []metav1.Condition `json:"conditions,omitempty"`
	AKSResourceID                                        string             `json:"aksResourceID"`
	PublicDNSZoneResourceID                              string             `json:"publicDNSZoneResourceID"`
	HostedClustersSecretsKeyVaultURL                     string             `json:"hostedClustersSecretsKeyVaultURL,omitempty"`
	HostedClustersManagedIdentitiesKeyVaultURL           string             `json:"hostedClustersManagedIdentitiesKeyVaultURL,omitempty"`
	HostedClustersSecretsKeyVaultManagedIdentityClientID string             `json:"hostedClustersSecretsKeyVaultManagedIdentityClientID,omitempty"`
	MaestroConsumerName                                  string             `json:"maestroConsumerName,omitempty"`
	MaestroRESTAPIURL                                    string             `json:"maestroRESTAPIURL,omitempty"`
	MaestroGRPCTarget                                    string             `json:"maestroGRPCTarget,omitempty"`
	ClusterServiceProvisionShardID                       string             `json:"clusterServiceProvisionShardID"`
	KubeApplierCosmosContainerName                       string             `json:"kubeApplierCosmosContainerName,omitempty"`
}

// ManagementCluster is the API response for a management cluster,
// without CosmosMetadata.
type ManagementCluster struct {
	ResourceID string                      `json:"resourceId"`
	Spec       fleet.ManagementClusterSpec `json:"spec"`
	Status     ManagementClusterStatus     `json:"status"`
}

func toManagementCluster(managementCluster *fleet.ManagementCluster) (ManagementCluster, error) {
	if managementCluster.ResourceID == nil {
		return ManagementCluster{}, fmt.Errorf("management cluster has nil resourceId")
	}
	status, err := toManagementClusterStatus(managementCluster.Status)
	if err != nil {
		return ManagementCluster{}, err
	}
	return ManagementCluster{
		ResourceID: managementCluster.ResourceID.String(),
		Spec:       managementCluster.Spec,
		Status:     status,
	}, nil
}

func toManagementClusterStatus(status fleet.ManagementClusterStatus) (ManagementClusterStatus, error) {
	if status.AKSResourceID == nil {
		return ManagementClusterStatus{}, fmt.Errorf("management cluster has nil aksResourceID")
	}
	if status.PublicDNSZoneResourceID == nil {
		return ManagementClusterStatus{}, fmt.Errorf("management cluster has nil publicDNSZoneResourceID")
	}
	if status.ClusterServiceProvisionShardID == nil {
		return ManagementClusterStatus{}, fmt.Errorf("management cluster has nil clusterServiceProvisionShardID")
	}
	return ManagementClusterStatus{
		Conditions:                                           status.Conditions,
		AKSResourceID:                                        status.AKSResourceID.String(),
		PublicDNSZoneResourceID:                              status.PublicDNSZoneResourceID.String(),
		HostedClustersSecretsKeyVaultURL:                     status.HostedClustersSecretsKeyVaultURL,
		HostedClustersManagedIdentitiesKeyVaultURL:           status.HostedClustersManagedIdentitiesKeyVaultURL,
		HostedClustersSecretsKeyVaultManagedIdentityClientID: status.HostedClustersSecretsKeyVaultManagedIdentityClientID,
		MaestroConsumerName:                                  status.MaestroConsumerName,
		MaestroRESTAPIURL:                                    status.MaestroRESTAPIURL,
		MaestroGRPCTarget:                                    status.MaestroGRPCTarget,
		ClusterServiceProvisionShardID:                       status.ClusterServiceProvisionShardID.String(),
		KubeApplierCosmosContainerName:                       status.KubeApplierCosmosContainerName,
	}, nil
}

// ManagementClusterGetHandler handles GET /admin/v1/stamps/{stampIdentifier}/managementClusters/{managementClusterName}.
type ManagementClusterGetHandler struct {
	fleetDBClient database.FleetDBClient
}

func NewManagementClusterGetHandler(fleetDBClient database.FleetDBClient) *ManagementClusterGetHandler {
	return &ManagementClusterGetHandler{
		fleetDBClient: fleetDBClient,
	}
}

func (h *ManagementClusterGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	stampIdentifier := r.PathValue("stampIdentifier")
	managementClusterName := r.PathValue("managementClusterName")

	if err := validateStampIdentifier(stampIdentifier); err != nil {
		return err
	}

	managementCluster, err := h.fleetDBClient.Stamps().ManagementClusters(stampIdentifier).Get(ctx, managementClusterName)
	if err != nil {
		if database.IsNotFoundError(err) {
			return arm.NewCloudError(http.StatusNotFound, arm.CloudErrorCodeNotFound, "",
				"Management cluster %q not found for stamp %q", managementClusterName, stampIdentifier)
		}
		return utils.TrackError(fmt.Errorf("failed to get management cluster: %w", err))
	}

	resp, err := toManagementCluster(managementCluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to convert management cluster: %w", err))
	}

	_, err = arm.WriteJSONResponse(w, http.StatusOK, resp)
	return err
}
