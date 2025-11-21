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

package handlers

import (
	"net/http"

	"github.com/Azure/ARO-HCP/admin/server/inventory"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func GetHCPKubeconfig(csClient ocm.ClusterServiceClientSpec, dbClient database.DBClient, mgmtClusterInventory *inventory.MgmtClusterInventory) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// resourceID, err := middleware.ResourceIDFromContext(request.Context())
		// if err != nil {
		// 	http.Error(writer, err.Error(), http.StatusInternalServerError)
		// 	return
		// }
		// internalCluster, err := dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(request.Context(), resourceID.Name)
		// if err != nil {
		// 	http.Error(writer, err.Error(), http.StatusInternalServerError)
		// 	return
		// }

		// todo: use mgmt cluster inventory to find the HCPs MC, and kick of
		// credential minting

		// this endpoint should return 202 and a location header with a URL
		// where the kubeconfig will be available

		writer.WriteHeader(http.StatusAccepted)
		writer.Write([]byte("not implemented yet"))
	})
}
