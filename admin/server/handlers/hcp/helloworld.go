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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	"github.com/Azure/ARO-HCP/admin/server/middleware"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/fpa"
)

func HCPHelloWorld(dbClient database.DBClient, fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		logger := middleware.LoggerFromContext(request.Context())

		// get the azure resource ID for this HCP
		resourceID, err := middleware.ResourceIDFromContext(request.Context())
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

		// get the FPA token credentials for the HCP tenant
		tokenCredential, err := fpaCredentialRetriever.RetrieveCredential(hcp.Identity.TenantID)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get FPA token credential: %v", err), http.StatusInternalServerError)
			return
		}

		// use the token credentials to get all loadbalancers in the managed resource group
		networkClient, err := armnetwork.NewLoadBalancersClient(resourceID.SubscriptionID, tokenCredential, nil)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get Azure network client: %v", err), http.StatusInternalServerError)
			return
		}
		pager := networkClient.NewListPager(hcp.CustomerProperties.Platform.ManagedResourceGroup, nil)
		for pager.More() {
			page, err := pager.NextPage(request.Context())
			if err != nil {
				logger.Error("failed to get next page", "error", err)
				http.Error(writer, fmt.Sprintf("failed to get next page: %v", err), http.StatusInternalServerError)
				return
			}

			for _, lb := range page.Value {
				fmt.Fprintln(writer, *lb.Name)
			}
		}

		// some output
		fmt.Fprintln(writer, resourceID.String())
		fmt.Fprintln(writer, clientPrincipalName)
		fmt.Fprintln(writer, hcp)
	})
}
