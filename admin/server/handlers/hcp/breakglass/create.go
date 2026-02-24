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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/set"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/admin/server/middleware"
	"github.com/Azure/ARO-HCP/internal/api/arm"
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

func (h *HCPBreakglassSessionCreationHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	// get the azure resource ID for this HCP
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "invalid resource identifier in request")
	}

	// get HCP details
	hcp, err := h.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(request.Context(), resourceID.Name)
	if err != nil {
		return fmt.Errorf("failed to get HCP from database: %w", err)
	}

	clusterHypershiftDetails, err := h.csClient.GetClusterHypershiftDetails(request.Context(), hcp.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return clusterServiceError(err, "hypershift details")
	}

	provisionShard, err := h.csClient.GetClusterProvisionShard(request.Context(), hcp.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return clusterServiceError(err, "provision shard")
	}

	group, ttl, err := h.validateSessionParameters(request)
	if err != nil {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "%s", err.Error())
	}

	clientPrincipalReference, err := middleware.ClientPrincipalFromContext(request.Context())
	if err != nil {
		return arm.NewCloudError(http.StatusUnauthorized, "Unauthorized", "", "missing client principal AAD reference")
	}

	principalName, principalType, err := mapGenevaActionClientReference(clientPrincipalReference)
	if err != nil {
		return arm.NewCloudError(http.StatusBadRequest, arm.CloudErrorCodeInvalidRequestContent, "", "%s", err.Error())
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
				Name: principalName,
				Type: principalType,
			},
		},
	}
	createdSession, err := h.sessionClient.Create(request.Context(), session, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create breakglass session: %w", err)
	}

	// return 202 Accepted with location header
	locationURL := fmt.Sprintf("%s/%s/kubeconfig", request.URL.Path, createdSession.Name)
	writer.Header().Set("Location", locationURL)
	writer.WriteHeader(http.StatusAccepted)
	return nil
}

// dSTS user identities are passed down from Geneva Actions as "dstsUser" in the X-Ms-Client-Principal-Type header, the name is the user's email address.
// AAD service principal identities are passed down from Geneva Actions as "aadServicePrincipal" in the X-Ms-Client-Principal-Type header, the name is the service principal's object ID.
func mapGenevaActionClientReference(clientPrincipalReference middleware.ClientPrincipalReference) (string, sessiongateapiv1alpha1.PrincipalType, error) {
	switch clientPrincipalReference.Type {
	case middleware.PrincipalTypeDSTSUser:
		return clientPrincipalReference.Name, sessiongateapiv1alpha1.PrincipalTypeAzureUser, nil
	case middleware.PrincipalTypeAADServicePrincipal:
		return clientPrincipalReference.Name, sessiongateapiv1alpha1.PrincipalTypeAzureServicePrincipal, nil
	}
	return "", "", fmt.Errorf("invalid client principal reference type: %s", clientPrincipalReference.Type)
}

// sessionCreationRequest represents the JSON body for creating a breakglass session.
type sessionCreationRequest struct {
	Group string `json:"group"`
	TTL   string `json:"ttl"`
}

// validateSessionParameters validates the group and TTL parameters for session creation
// by reading them from the request body.
func (h *HCPBreakglassSessionCreationHandler) validateSessionParameters(request *http.Request) (string, time.Duration, error) {
	var body sessionCreationRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		return "", 0, fmt.Errorf("failed to decode request body: %v", err)
	}

	var errs []error

	// authorization level - get from group field
	if body.Group == "" {
		errs = append(errs, fmt.Errorf("group field is required"))
	} else if ok := h.AllowedBreakglassGroups.Has(body.Group); !ok {
		errs = append(errs, fmt.Errorf("group %q is not in the allowed list %v", body.Group, h.AllowedBreakglassGroups.SortedList()))
	}

	// get TTL from body field
	var ttl time.Duration
	if body.TTL == "" {
		errs = append(errs, fmt.Errorf("ttl field is required"))
	} else {
		var err error
		ttl, err = time.ParseDuration(body.TTL)
		if err != nil {
			errs = append(errs, fmt.Errorf("invalid ttl field: %v", err))
		}
		if ttl > h.MaxSessionTTL {
			errs = append(errs, fmt.Errorf("ttl must not exceed %v", h.MaxSessionTTL))
		}
		if ttl < h.MinSessionTTL {
			errs = append(errs, fmt.Errorf("ttl must be at least %v", h.MinSessionTTL))
		}
	}

	return body.Group, ttl, utilerrors.NewAggregate(errs)
}

// clusterServiceError checks if err is an OCM not-found error and returns a
// specific CloudError. This prevents ReportError from misinterpreting it as
// "HCP resource not found" (the HCP was already found in the database).
// Non-OCM errors are wrapped for ReportError to handle as internal errors.
func clusterServiceError(err error, what string) error {
	var ocmErr *ocmerrors.Error
	if errors.As(err, &ocmErr) && ocmErr.Status() == http.StatusNotFound {
		return arm.NewCloudError(http.StatusNotFound, arm.CloudErrorCodeNotFound, "",
			"%s not found in cluster service", what)
	}
	return fmt.Errorf("failed to get %s from cluster service: %w", what, err)
}
