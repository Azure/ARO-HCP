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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/admin/server/breakglass"
	"github.com/Azure/ARO-HCP/admin/server/mgmtinventory"
	"github.com/Azure/ARO-HCP/admin/server/middleware"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	sessiongateapiv1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	clientcmd "k8s.io/client-go/tools/clientcmd"
)

func HCPStartBreakGlassSession(dbClient database.DBClient, csClient ocm.ClusterServiceClientSpec, inventory mgmtinventory.Inventory, sessionWriter breakglass.SessionWriter) http.Handler {
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

		// get HCP details
		hcp, err := dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(request.Context(), resourceID.Name)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get HCP: %v", err), http.StatusInternalServerError)
			return
		}
		clusterHypershiftDetails, err := csClient.GetClusterHypershiftDetails(request.Context(), hcp.ServiceProviderProperties.ClusterServiceID)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get hypershift details: %v", err), http.StatusInternalServerError)
			return
		}

		// get AKS details
		// this is quite some hackery - we have no information easily available right now where an HCP is running,
		// so we leverage the fact that we only have one management cluster per region for now and just pick that one
		mgmtClusters, err := inventory.GetManagementClusters(request.Context())
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get management clusters: %v", err), http.StatusInternalServerError)
			return
		}
		if len(mgmtClusters) == 0 {
			http.Error(writer, "no management clusters found", http.StatusNotFound)
			return
		}
		if len(mgmtClusters) > 1 {
			http.Error(writer, "multiple management clusters found", http.StatusInternalServerError)
			return
		}
		mgmtCluster := mgmtClusters[0]

		// authorization level - get from group parameter
		group := request.URL.Query().Get("group")
		if group == "" {
			http.Error(writer, "group parameter is required", http.StatusBadRequest)
			return
		}

		// get TTL from query parameter
		ttlParam := request.URL.Query().Get("ttl")
		if ttlParam == "" {
			http.Error(writer, "ttl parameter is required", http.StatusBadRequest)
			return
		}
		ttl, err := time.ParseDuration(ttlParam)
		if err != nil {
			http.Error(writer, fmt.Sprintf("invalid ttl parameter: %v", err), http.StatusBadRequest)
			return
		}

		// create the session
		sessionSpec := sessiongateapiv1alpha1.SessionSpec{
			TTL: metav1.Duration{Duration: ttl},
			ManagementCluster: sessiongateapiv1alpha1.ManagementCluster{
				ResourceID: mgmtCluster.ResourceID,
			},
			HostedControlPlane: sessiongateapiv1alpha1.HostedControlPlane{
				ResourceID: resourceID.String(),
				Namespace:  clusterHypershiftDetails.HCPNamespace(),
			},
			AccessLevel: sessiongateapiv1alpha1.AccessLevel{
				Group: group,
			},
			Owner: sessiongateapiv1alpha1.Principal{
				Type: sessiongateapiv1alpha1.PrincipalTypeUser,
				UserPrincipal: &sessiongateapiv1alpha1.UserPrincipal{
					Name:  clientPrincipalName,
					Claim: "upn",
				},
			},
		}
		createdSession, err := sessionWriter.CreateSession(request.Context(), sessionSpec)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to create session: %v", err), http.StatusInternalServerError)
			return
		}

		// return 202 Accepted with location header
		locationURL := fmt.Sprintf("%s/%s", request.URL.Path, createdSession.Name)
		writer.Header().Set("Location", locationURL)
		writer.WriteHeader(http.StatusAccepted)
	})
}

func HCPBreakGlassSessionKubeconfig(sessionReader breakglass.SessionReader) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		sessionName := request.PathValue("sessionName")
		if sessionName == "" {
			http.Error(writer, "session parameter is required", http.StatusBadRequest)
			return
		}

		session, err := sessionReader.GetSession(request.Context(), sessionName)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get session: %v", err), http.StatusInternalServerError)
			return
		}

		if !session.IsReady() {
			// Return 202 Accepted with appropriate cache headers while session is being provisioned
			writer.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			writer.Header().Set("Retry-After", "5")
			writer.WriteHeader(http.StatusAccepted)
			return
		}

		// Generate and return the kubeconfig
		kubeconfig, err := session.GetKubeconfig()
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to get kubeconfig: %v", err), http.StatusInternalServerError)
			return
		}
		kubeconfigBytes, err := clientcmd.Write(kubeconfig)
		if err != nil {
			http.Error(writer, fmt.Sprintf("failed to write kubeconfig: %v", err), http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/x-yaml")
		writer.WriteHeader(http.StatusOK)
		writer.Write(kubeconfigBytes)
	})
}
