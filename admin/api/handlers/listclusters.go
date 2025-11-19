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
	"fmt"
	"net/http"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func ListClustersHandler(csClient ocm.ClusterServiceClientSpec, dbClient database.DBClient) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		resourceID := "/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/bragazzi-rg-03/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/bragazzi"
		parsedArmResourceID, err := azcorearm.ParseResourceID(resourceID)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		internalCluster, err := dbClient.HCPClusters(parsedArmResourceID.SubscriptionID, parsedArmResourceID.ResourceGroupName).Get(request.Context(), resourceID)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(writer, "Internal Cluster: %+v\n", internalCluster)
		filter := fmt.Sprintf("azure.subscription_id = '%s' and azure.resource_group_name = '%s' and azure.resource_name = '%s'", parsedArmResourceID.SubscriptionID, parsedArmResourceID.ResourceGroupName, parsedArmResourceID.Name)
		fmt.Fprintf(writer, "Filter: %s\n", filter)
		clusters := csClient.ListClusters(filter)
		clusters.GetError()
		for cluster := range clusters.Items(request.Context()) {
			fmt.Fprintln(writer, cluster.ID())
		}
	})
}
