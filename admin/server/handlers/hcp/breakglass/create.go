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

package breakglass

import (
	"fmt"
	"net/http"
	"net/mail"
	"time"

	"github.com/google/uuid"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/set"

	"github.com/Azure/ARO-HCP/admin/server/middleware"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	sessiongateapiv1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/clientset/versioned/typed/sessiongate/v1alpha1"
)

// HCPBreakglassSessionCreationHandler handles requests to create breakglass sessions.
// This endpoint is accessed exclusively via Geneva Actions. See package documentation for security model.
type HCPBreakglassSessionCreationHandler struct {
	dbClient                database.DBClient
	csClient                ocm.ClusterServiceClientSpec
	sessionClient           sessiongatev1alpha1.SessionInterface
	AllowedBreakglassGroups set.Set[string]
	MinSessionTTL           time.Duration
	MaxSessionTTL           time.Duration
}

func NewHCPBreakglassSessionCreationHandler(dbClient database.DBClient, csClient ocm.ClusterServiceClientSpec, sessionClient sessiongatev1alpha1.SessionInterface, allowedBreakglassGroups set.Set[string], minSessionTTL time.Duration, maxSessionTTL time.Duration) *HCPBreakglassSessionCreationHandler {
	return &HCPBreakglassSessionCreationHandler{
		dbClient:                dbClient,
		csClient:                csClient,
		sessionClient:           sessionClient,
		MinSessionTTL:           minSessionTTL,
		MaxSessionTTL:           maxSessionTTL,
		AllowedBreakglassGroups: allowedBreakglassGroups,
	}
}

func (h *HCPBreakglassSessionCreationHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	logger := utils.LoggerFromContext(request.Context())

	// get the azure resource ID for this HCP
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		logger.Error(err, "failed to get resource ID from context")
		http.Error(writer, "invalid resource identifier in request", http.StatusBadRequest)
		return
	}

	// get client principal name attached to the request
	clientPrincipalName, err := middleware.ClientPrincipalNameFromContext(request.Context())
	if err != nil {
		logger.Error(err, "failed to get client principal name from context")
		http.Error(writer, "missing client principal", http.StatusUnauthorized)
		return
	}

	// get HCP details
	hcp, err := h.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(request.Context(), resourceID.Name)
	if err != nil {
		logger.Error(err, "failed to get HCP from database")
		http.Error(writer, "failed to retrieve cluster information", http.StatusInternalServerError)
		return
	}

	clusterHypershiftDetails, err := h.csClient.GetClusterHypershiftDetails(request.Context(), hcp.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		logger.Error(err, "failed to get hypershift details from cluster service", "clusterServiceID", hcp.ServiceProviderProperties.ClusterServiceID)
		http.Error(writer, "failed to retrieve cluster information", http.StatusInternalServerError)
		return
	}

	provisionShard, err := h.csClient.GetClusterProvisionShard(request.Context(), hcp.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		logger.Error(err, "failed to get provision shard from cluster service", "clusterServiceID", hcp.ServiceProviderProperties.ClusterServiceID)
		http.Error(writer, "failed to retrieve cluster information", http.StatusInternalServerError)
		return
	}

	group, ttl, err := h.validateSessionParameters(request)
	if err != nil {
		logger.Error(err, "failed to validate session parameters")
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}

	principalType, err := getPrincipalType(clientPrincipalName)
	if err != nil {
		logger.Error(err, "failed to infer principal type", "principalName", clientPrincipalName)
		http.Error(writer, "failed to infer principal type", http.StatusBadRequest)
		return
	}

	session := &sessiongateapiv1alpha1.Session{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "breakglass-",
		},
		Spec: sessiongateapiv1alpha1.SessionSpec{
			TTL: metav1.Duration{Duration: ttl},
			ManagementCluster: sessiongateapiv1alpha1.ManagementCluster{
				ResourceID: provisionShard.AzureShard().AksManagementClusterResourceId(),
			},
			HostedControlPlane: sessiongateapiv1alpha1.HostedControlPlane{
				ResourceID: resourceID.String(),
				Namespace:  clusterHypershiftDetails.HCPNamespace(),
			},
			AccessLevel: sessiongateapiv1alpha1.AccessLevel{
				Group: group,
			},
			Owner: sessiongateapiv1alpha1.Principal{
				Name: clientPrincipalName,
				Type: principalType,
			},
		},
	}
	createdSession, err := h.sessionClient.Create(request.Context(), session, metav1.CreateOptions{})
	if err != nil {
		logger.Error(err, "failed to create session")
		http.Error(writer, "failed to create breakglass session", http.StatusInternalServerError)
		return
	}

	// return 202 Accepted with location header
	locationURL := fmt.Sprintf("%s/%s/kubeconfig", request.URL.Path, createdSession.Name)
	writer.Header().Set("Location", locationURL)
	writer.WriteHeader(http.StatusAccepted)
}

// getPrincipalType determines the type of principal based on the principal name format.
//
// This function uses format inference because Geneva Actions currently only provides
// the principal name (X-Ms-Client-Principal-Name header) without indicating the principal
// type. The principal type is required because:
//
//  1. The kubeconfig's user.exec command differs for users vs service principals
//  2. Access token claims are credential-type specific (e.g., "upn" for users, "appid"
//     for service principals)
//
// The inference relies on the convention that Azure application IDs (service principals)
// are GUIDs, while user principals are UPNs (email format like user@example.com).
//
// Migration assumption: This heuristic should be replaced once Geneva Actions supports
// passing principal type metadata directly (e.g. via a dedicated header). Until then,
// the format-based inference is the only option. If the heuristic proves unreliable,
// an explicit principal type parameter could be added to the breakglass API.
func getPrincipalType(principalName string) (sessiongateapiv1alpha1.PrincipalType, error) {
	if principalName == "" {
		return "", fmt.Errorf("principal name cannot be empty")
	}

	if uuid.Validate(principalName) == nil {
		return sessiongateapiv1alpha1.PrincipalTypeAzureServicePrincipal, nil
	}

	if _, err := mail.ParseAddress(principalName); err == nil {
		return sessiongateapiv1alpha1.PrincipalTypeAzureUser, nil
	}

	return "", fmt.Errorf("principal name %q is neither a valid UUID nor email address", principalName)
}

// validateSessionParameters validates the group and TTL parameters for session creation.
func (h *HCPBreakglassSessionCreationHandler) validateSessionParameters(request *http.Request) (string, time.Duration, error) {
	var errs []error
	var err error

	// authorization level - get from group parameter
	group := request.URL.Query().Get("group")
	if group == "" {
		errs = append(errs, fmt.Errorf("group parameter is required"))
	} else if ok := h.AllowedBreakglassGroups.Has(group); !ok {
		errs = append(errs, fmt.Errorf("group %q is not in the allowed list %v", group, h.AllowedBreakglassGroups.SortedList()))
	}

	// get TTL from query parameter
	var ttl time.Duration
	ttlParam := request.URL.Query().Get("ttl")
	if ttlParam == "" {
		errs = append(errs, fmt.Errorf("ttl parameter is required"))
	} else {
		ttl, err = time.ParseDuration(ttlParam)
		if err != nil {
			errs = append(errs, fmt.Errorf("invalid ttl parameter: %v", err))
		}
		if ttl > h.MaxSessionTTL {
			errs = append(errs, fmt.Errorf("ttl must not exceed %v", h.MaxSessionTTL))
		}
		if ttl < h.MinSessionTTL {
			errs = append(errs, fmt.Errorf("ttl must be at least %v", h.MinSessionTTL))
		}
	}

	return group, ttl, utilerrors.NewAggregate(errs)
}
