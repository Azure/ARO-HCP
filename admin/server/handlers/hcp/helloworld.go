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

	"github.com/Azure/ARO-HCP/admin/server/middleware"
	"github.com/Azure/ARO-HCP/internal/database"
)

func HCPHelloWorld(dbClient database.DBClient) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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

		// some output
		fmt.Fprintln(writer, resourceID.String())
		fmt.Fprintln(writer, clientPrincipalName)
		fmt.Fprintln(writer, hcp)
	})
}
