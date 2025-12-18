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
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	"github.com/Azure/ARO-HCP/admin/server/middleware"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/fpa"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

//
//   T H I S   I S   O N L Y   A   D E M O   E N D P O I N T
//
//   it shows various clients in use. we will remove this once we have
//   docs and other endpoints in place that can show how to use the clients
//   in tandem.
//

func HCPHelloWorld(dbClient database.DBClient, csClient ocm.ClusterServiceClientSpec, fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// get the azure resource ID for this HCP
		resourceID, err := utils.ResourceIDFromContext(request.Context())
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get resource ID: %v", err), http.StatusInternalServerError)
			return
		}

		// get client princiapal name attached to the request
		clientPrincipalName, err := middleware.ClientPrincipalNameFromContext(request.Context())
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get client principal name: %v", err), http.StatusInternalServerError)
			return
		}

		// load the HCP from the cosmos DB
		hcp, err := dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(request.Context(), resourceID.Name)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get HCP: %v", err), http.StatusInternalServerError)
			return
		}

		// get CS cluster data - once the sync from CS to cosmos is in place, we should not need this anymore
		csCluster, err := csClient.GetCluster(request.Context(), hcp.ServiceProviderProperties.ClusterServiceID)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get CS cluster data: %v", err), http.StatusInternalServerError)
			return
		}

		// get first party application token credentials for the HCP
		tokenCredential, err := fpaCredentialRetriever.RetrieveCredential(csCluster.Azure().TenantID())
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get FPA token credentials: %v", err), http.StatusInternalServerError)
			return
		}

		// fetch all loadbalancers from the managedresource group using azuresdk
		lbClient, err := armnetwork.NewLoadBalancersClient(csCluster.Azure().SubscriptionID(), tokenCredential, nil)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to create load balancer client: %v", err), http.StatusInternalServerError)
			return
		}
		pager := lbClient.NewListPager(csCluster.Azure().ManagedResourceGroupName(), nil)
		var loadBalancers []string
		for pager.More() {
			page, err := pager.NextPage(request.Context())
			if err != nil {
				http.Error(writer, fmt.Sprintf("failed to list load balancers: %v", err), http.StatusInternalServerError)
				return
			}
			for _, lb := range page.Value {
				if lb.Name != nil {
					loadBalancers = append(loadBalancers, *lb.Name)
				}
			}
		}

		// some output
		output := map[string]any{
			"resourceID":           resourceID.String(),
			"internalClusterID":    hcp.ServiceProviderProperties.ClusterServiceID,
			"clientPrincipalName":  clientPrincipalName,
			"hcp":                  hcp,
			"tenantID":             csCluster.Azure().TenantID(),
			"managedResourceGroup": csCluster.Azure().ManagedResourceGroupName(),
			"loadBalancers":        loadBalancers,
		}
		err = json.NewEncoder(writer).Encode(output)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to encode output: %v", err), http.StatusInternalServerError)
			return
		}
	})
}
