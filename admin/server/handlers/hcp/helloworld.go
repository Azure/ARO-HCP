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
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	"github.com/Azure/ARO-HCP/admin/server/middleware"
	"github.com/Azure/ARO-HCP/internal/api/arm"
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

type HCPHelloWorldHandler struct {
	dbClient database.DBClient
	csClient ocm.ClusterServiceClientSpec
}

func NewHCPHelloWorldHandler(dbClient database.DBClient, csClient ocm.ClusterServiceClientSpec) *HCPHelloWorldHandler {
	return &HCPHelloWorldHandler{dbClient: dbClient, csClient: csClient}
}

func (h *HCPHelloWorldHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	// get the azure resource ID for this HCP
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return fmt.Errorf("failed to get resource ID: %w", err)
	}

	// get client princiapal name attached to the request
	clientPrincipalReference, err := middleware.ClientPrincipalFromContext(request.Context())
	if err != nil {
		return fmt.Errorf("failed to get client principal name: %w", err)
	}

	// load the HCP from the cosmos DB
	hcp, err := h.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(request.Context(), resourceID.Name)
	if err != nil {
		return fmt.Errorf("failed to get HCP: %w", err)
	}

	// get CS cluster data - once the sync from CS to cosmos is in place, we should not need this anymore
	csCluster, err := h.csClient.GetCluster(request.Context(), hcp.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return fmt.Errorf("failed to get CS cluster data: %w", err)
	}

	// some output
	output := map[string]any{
		"resourceID":           hcp.ID.String(),
		"internalClusterID":    hcp.ServiceProviderProperties.ClusterServiceID,
		"clientPrincipalName":  clientPrincipalReference.Name,
		"tenantID":             csCluster.Azure().TenantID(),
		"managedResourceGroup": csCluster.Azure().ManagedResourceGroupName(),
		"hcpName":              hcp.Name,
	}
	_, err = arm.WriteJSONResponse(writer, http.StatusOK, output)
	return err
}

type HCPDemoListLoadbalancersHandler struct {
	dbClient               database.DBClient
	csClient               ocm.ClusterServiceClientSpec
	fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever
}

func NewHCPDemoListLoadbalancersHandler(dbClient database.DBClient, csClient ocm.ClusterServiceClientSpec, fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever) *HCPDemoListLoadbalancersHandler {
	return &HCPDemoListLoadbalancersHandler{dbClient: dbClient, csClient: csClient, fpaCredentialRetriever: fpaCredentialRetriever}
}

func (h *HCPDemoListLoadbalancersHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	// get the azure resource ID for this HCP
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return fmt.Errorf("failed to get resource ID: %w", err)
	}

	// load the HCP from the cosmos DB
	hcp, err := h.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(request.Context(), resourceID.Name)
	if err != nil {
		return fmt.Errorf("failed to get HCP: %w", err)
	}

	// get first party application token credentials for the HCP
	tokenCredential, err := h.fpaCredentialRetriever.RetrieveCredential(hcp.Identity.TenantID)
	if err != nil {
		return fmt.Errorf("failed to get FPA token credentials: %w", err)
	}

	// fetch all loadbalancers from the managedresource group using azuresdk
	lbClient, err := armnetwork.NewLoadBalancersClient(hcp.ID.SubscriptionID, tokenCredential, nil)
	if err != nil {
		return fmt.Errorf("failed to create load balancer client: %w", err)
	}
	pager := lbClient.NewListPager(hcp.CustomerProperties.Platform.ManagedResourceGroup, nil)
	var loadBalancers []string
	for pager.More() {
		page, err := pager.NextPage(request.Context())
		if err != nil {
			return fmt.Errorf("failed to list load balancers: %w", err)
		}
		for _, lb := range page.Value {
			if lb.Name != nil {
				loadBalancers = append(loadBalancers, *lb.Name)
			}
		}
	}

	// some output
	output := map[string]any{"loadBalancers": loadBalancers}
	_, err = arm.WriteJSONResponse(writer, http.StatusOK, output)
	return err
}
