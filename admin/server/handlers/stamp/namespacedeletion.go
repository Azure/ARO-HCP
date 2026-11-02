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
	"context"
	"encoding/json"
	"net/http"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/namespacedeletiongate"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// NamespaceDeletionCreateRequest is the POST body.
//
// The namespace must be named exactly; a prefix is never accepted. There is no
// cluster identifier of any kind here: the targets this API exists for have no
// live cluster record to name, so ownership is established by matching live
// Cosmos data against management-cluster and Kubernetes identity instead of by
// trusting a caller-supplied ID.
type NamespaceDeletionCreateRequest struct {
	NamespaceName     string `json:"namespaceName"`
	HostedClusterName string `json:"hostedClusterName"`
	RequestReason     string `json:"requestReason"`

	// DeleteHostedCluster authorizes deleting the HostedCluster when it has no
	// deletionTimestamp of its own. Absent or false, such a HostedCluster is a
	// refusal instead. It never overrides the liveness gate.
	DeleteHostedCluster bool `json:"deleteHostedCluster,omitempty"`
}

// NamespaceDeletionCreateResponse names the created (or already existing)
// request so the caller can poll it with the GET route.
type NamespaceDeletionCreateResponse struct {
	Name                string `json:"name"`
	NamespaceName       string `json:"namespaceName"`
	HostedClusterName   string `json:"hostedClusterName"`
	DeleteHostedCluster bool   `json:"deleteHostedCluster"`
	Created             bool   `json:"created"`
}

// NamespaceDeletionGetResponse reports progress and blockers.
//
// It carries conditions and the audit record and nothing else: the controller
// only needs "do I act or not", and a human needs one good condition string
// rather than a scattering of individual result fields.
type NamespaceDeletionGetResponse struct {
	Name                string                           `json:"name"`
	NamespaceName       string                           `json:"namespaceName"`
	HostedClusterName   string                           `json:"hostedClusterName"`
	DeleteHostedCluster bool                             `json:"deleteHostedCluster"`
	NamespaceUID        string                           `json:"namespaceUID,omitempty"`
	RequestReason       string                           `json:"requestReason,omitempty"`
	Conditions          []metav1.Condition               `json:"conditions,omitempty"`
	CompletedAt         *metav1.Time                     `json:"completedAt,omitempty"`
	Audit               *fleetapi.NamespaceDeletionAudit `json:"audit,omitempty"`
}

// Cloud error codes specific to this API. They are the stable contract that
// automation matches on; the human-readable messages beside them are not.
const (
	CloudErrorCodeProtectedNamespace        = "ProtectedNamespace"
	CloudErrorCodeClusterRecordPresent      = "ClusterRecordPresent"
	CloudErrorCodeActiveDesirePresent       = "ActiveDesirePresent"
	CloudErrorCodeOwnershipCheckUnavailable = "OwnershipCheckUnavailable"
	CloudErrorCodeOwnershipIndeterminate    = "OwnershipIndeterminate"
)

// NamespaceDeletionCreateHandler handles
// POST /admin/v1/stamps/{stampIdentifier}/managementclusters/{managementClusterName}/namespacedeletions.
//
// The handler only writes desired state: it never contacts a management cluster
// and never holds the request open while work happens.
//
// What it can and cannot check is an architectural split, not a stylistic one.
// Nothing on the API side may talk to a management cluster, and the only
// sanctioned path to management-cluster state is a ReadDesire mirrored through
// kube-applier — which does not exist until after the request is created. So
// POST checks everything answerable from Cosmos (shape, grammar, the denylist,
// ownership) and every check needing Kubernetes truth happens later in the
// controller. An accepted POST is a recorded request, never a statement that
// the cleanup is safe.
type NamespaceDeletionCreateHandler struct {
	fleetDBClient fleetcosmosstorage.FleetDBClient

	// resourcesDBClient and kubeApplierDBClients back the ownership gate. The
	// admin server is already constructed with both, so the gate needs no new
	// plumbing. The ApplyDesire lister is per-management-cluster, so it is
	// resolved per request rather than held here.
	resourcesDBClient    corecosmosstorage.ResourcesDBClient
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients
}

func NewNamespaceDeletionCreateHandler(
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
) *NamespaceDeletionCreateHandler {
	return &NamespaceDeletionCreateHandler{
		fleetDBClient:        fleetDBClient,
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
	}
}

func (h *NamespaceDeletionCreateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	stampIdentifier := r.PathValue("stampIdentifier")
	managementClusterName := r.PathValue("managementClusterName")

	if err := validateStampIdentifier(stampIdentifier); err != nil {
		return err
	}
	if err := validateManagementClusterName(managementClusterName); err != nil {
		return err
	}

	var body NamespaceDeletionCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return coreapi.NewCloudError(
			http.StatusBadRequest,
			coreapi.CloudErrorCodeInvalidRequestContent, "",
			"The request content was invalid and could not be deserialized: %q", err,
		)
	}

	// The denylist runs first and independently of the grammar below. The
	// grammar already excludes kube-system and friends, but a bug in the parser
	// must not be able to put a platform namespace in reach.
	if namespacedeletiongate.IsProtectedNamespace(body.NamespaceName) {
		return coreapi.NewCloudError(
			http.StatusBadRequest,
			CloudErrorCodeProtectedNamespace, "namespaceName",
			"Namespace %q is a platform namespace and is never a valid target, whatever its state.",
			body.NamespaceName,
		)
	}

	// Structure only: the admin API does not receive the backend's environment
	// identifier, so the environment segment is anchored in the backend and
	// only the shape is enforced here.
	relationship, err := fleetapihelpers.ClassifyNamespace(body.NamespaceName, body.HostedClusterName, "")
	if err != nil {
		return coreapi.NewCloudError(
			http.StatusBadRequest,
			coreapi.CloudErrorCodeInvalidRequestContent, "namespaceName",
			"%v", err,
		)
	}

	managementClusters := h.fleetDBClient.Stamps().ManagementClusters(stampIdentifier)
	managementCluster, err := managementClusters.Get(ctx, managementClusterName)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeNotFound, "",
				"Management cluster %q not found in stamp %q", managementClusterName, stampIdentifier)
		}
		return utils.TrackError(err)
	}

	// The namespace name is the resource name. A Kubernetes namespace name is
	// at most 63 characters of [a-z0-9-], which is a valid single ARM path
	// segment and a valid Cosmos resource name, so a hash would buy nothing and
	// cost readability.
	name := strings.ToLower(body.NamespaceName)
	requestCRUD := managementClusters.NamespaceDeletions()

	// An ordinary duplicate returns the stored request and never restarts work.
	if existing, err := requestCRUD.Get(ctx, name); err == nil {
		return writeJSON(w, http.StatusOK, namespaceDeletionCreateResponse(name, existing, false))
	} else if !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(err)
	}

	managementClusterResourceID := ""
	if managementCluster.GetResourceID() != nil {
		managementClusterResourceID = managementCluster.GetResourceID().String()
	}
	if gateErr := h.checkOwnership(ctx, relationship, body, managementCluster.GetResourceID(), managementClusterResourceID); gateErr != nil {
		return gateErr
	}

	resourceID, err := fleetapihelpers.ToNamespaceDeletionResourceID(stampIdentifier, name)
	if err != nil {
		return utils.TrackError(err)
	}

	request := &fleetapi.NamespaceDeletion{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(stampIdentifier),
		},
		Spec: fleetapi.NamespaceDeletionSpec{
			NamespaceName:       body.NamespaceName,
			HostedClusterName:   body.HostedClusterName,
			DeleteHostedCluster: body.DeleteHostedCluster,
			RequestReason:       body.RequestReason,
		},
	}

	created, err := requestCRUD.Create(ctx, request, nil)
	if err != nil {
		if cosmosstorageutils.IsConflictError(err) {
			// Lost a create race. Return the stored winner rather than echoing
			// this request back, so the caller never sees a success response
			// describing a document that was never persisted.
			winner, getErr := requestCRUD.Get(ctx, name)
			if getErr != nil {
				return utils.TrackError(getErr)
			}
			return writeJSON(w, http.StatusOK, namespaceDeletionCreateResponse(name, winner, false))
		}
		if validationErr := asCloudValidationError(err); validationErr != nil {
			return validationErr
		}
		return utils.TrackError(err)
	}

	return writeJSON(w, http.StatusCreated, namespaceDeletionCreateResponse(name, created, true))
}

// checkOwnership runs the live Cosmos ownership gate and converts its verdict
// into the operator-facing error.
//
// A failed lookup is never reported as "no record": an Unavailable verdict is a
// retryable 503 and an Indeterminate one is a 409 asking for investigation.
func (h *NamespaceDeletionCreateHandler) checkOwnership(
	ctx context.Context,
	relationship *fleetapihelpers.NamespaceRelationship,
	body NamespaceDeletionCreateRequest,
	managementClusterID *azcorearm.ResourceID,
	managementClusterResourceID string,
) error {
	var clusterLister namespacedeletiongate.ServiceProviderClusterLister
	if h.resourcesDBClient != nil {
		clusterLister = h.resourcesDBClient.ResourcesGlobalListers().ServiceProviderClusters()
	}

	var desireLister namespacedeletiongate.ApplyDesireLister
	if h.kubeApplierDBClients != nil && managementClusterID != nil {
		kubeApplierClient, err := h.kubeApplierDBClients.For(ctx, managementClusterID)
		if err != nil {
			// Reaching the kube-applier container failed. That is an
			// unavailable check, not an absent desire.
			return coreapi.NewCloudError(
				http.StatusServiceUnavailable,
				CloudErrorCodeOwnershipCheckUnavailable, "namespaceName",
				"The ownership check for namespace %q could not reach the kube-applier store for management cluster %q: %v. This is not evidence that the namespace is unowned. No NamespaceDeletion request was created.",
				body.NamespaceName, managementClusterResourceID, err,
			)
		}
		desireLister = kubeApplierClient.Listers().ApplyDesires()
	}

	result := namespacedeletiongate.Evaluate(ctx, clusterLister, desireLister, namespacedeletiongate.Target{
		ManagementClusterResourceID: managementClusterResourceID,
		HostedClusterNamespace:      relationship.HostedClusterNamespace,
		ControlPlaneNamespace:       relationship.ControlPlaneNamespace,
		HostedClusterName:           body.HostedClusterName,
	}, "")

	switch result.Verdict {
	case namespacedeletiongate.VerdictUnowned:
		return nil

	case namespacedeletiongate.VerdictOwned:
		if len(result.ConflictingDesire) > 0 {
			return coreapi.NewCloudError(
				http.StatusConflict,
				CloudErrorCodeActiveDesirePresent, "namespaceName",
				"Namespace %q is still maintained by ApplyDesire %q, so another controller is actively asserting it should exist. Resolve that owner first. No NamespaceDeletion request was created.",
				body.NamespaceName, result.ConflictingDesire,
			)
		}
		state := "active"
		if result.OwnerDeleting {
			state = "Deleting"
		}
		return coreapi.NewCloudError(
			http.StatusConflict,
			CloudErrorCodeClusterRecordPresent, "namespaceName",
			"Namespace %q is still owned by cluster %q (state %s); use the normal cluster deletion flow. No NamespaceDeletion request was created.",
			body.NamespaceName, result.OwnerResourceID, state,
		)

	case namespacedeletiongate.VerdictIndeterminate:
		return coreapi.NewCloudError(
			http.StatusConflict,
			CloudErrorCodeOwnershipIndeterminate, "namespaceName",
			"Ownership of namespace %q could not be decided either way: %s. Investigate before resubmitting. No NamespaceDeletion request was created.",
			body.NamespaceName, result.Detail,
		)

	default:
		return coreapi.NewCloudError(
			http.StatusServiceUnavailable,
			CloudErrorCodeOwnershipCheckUnavailable, "namespaceName",
			"The ownership check for namespace %q could not complete: %s. This is not evidence that the namespace is unowned. Retry once the database is healthy. No NamespaceDeletion request was created.",
			body.NamespaceName, result.Detail,
		)
	}
}

func namespaceDeletionCreateResponse(name string, request *fleetapi.NamespaceDeletion, created bool) NamespaceDeletionCreateResponse {
	return NamespaceDeletionCreateResponse{
		Name:                name,
		NamespaceName:       request.Spec.NamespaceName,
		HostedClusterName:   request.Spec.HostedClusterName,
		DeleteHostedCluster: request.Spec.DeleteHostedCluster,
		Created:             created,
	}
}

// NamespaceDeletionGetHandler handles
// GET /admin/v1/stamps/{stampIdentifier}/managementclusters/{managementClusterName}/namespacedeletions/{name}.
type NamespaceDeletionGetHandler struct {
	fleetDBClient fleetcosmosstorage.FleetDBClient
}

func NewNamespaceDeletionGetHandler(fleetDBClient fleetcosmosstorage.FleetDBClient) *NamespaceDeletionGetHandler {
	return &NamespaceDeletionGetHandler{fleetDBClient: fleetDBClient}
}

func (h *NamespaceDeletionGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	stampIdentifier := r.PathValue("stampIdentifier")
	managementClusterName := r.PathValue("managementClusterName")
	name := r.PathValue("name")

	if err := validateStampIdentifier(stampIdentifier); err != nil {
		return err
	}
	if err := validateManagementClusterName(managementClusterName); err != nil {
		return err
	}

	request, err := h.fleetDBClient.Stamps().ManagementClusters(stampIdentifier).NamespaceDeletions().Get(ctx, name)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeNotFound, "",
				"Namespace deletion request %q not found on management cluster %q in stamp %q", name, managementClusterName, stampIdentifier)
		}
		return utils.TrackError(err)
	}

	return writeJSON(w, http.StatusOK, NamespaceDeletionGetResponse{
		Name:                name,
		NamespaceName:       request.Spec.NamespaceName,
		HostedClusterName:   request.Spec.HostedClusterName,
		DeleteHostedCluster: request.Spec.DeleteHostedCluster,
		NamespaceUID:        request.Spec.NamespaceUID,
		RequestReason:       request.Spec.RequestReason,
		Conditions:          request.Status.Conditions,
		CompletedAt:         request.Status.CompletedAt,
		Audit:               request.Status.Audit,
	})
}

// validateManagementClusterName rejects anything but the singleton name. The
// management cluster is one per stamp — ManagementClusterResourceName is the
// constant "default" and the Cosmos scope is derived from the stamp alone — so
// the path segment exists for consistency with the sibling stamp routes and
// would otherwise be silently ignored.
func validateManagementClusterName(managementClusterName string) error {
	if !strings.EqualFold(managementClusterName, fleetapi.ManagementClusterResourceName) {
		return coreapi.NewCloudError(
			http.StatusBadRequest,
			coreapi.CloudErrorCodeInvalidRequestContent, "managementClusterName",
			"Invalid management cluster name %q: the management cluster is a singleton and must be %q",
			managementClusterName, fleetapi.ManagementClusterResourceName,
		)
	}
	return nil
}

// asCloudValidationError turns the CRUD layer's validation failure into a 400
// so an operator sees which field was wrong instead of an opaque 500. The
// validating CRUD wraps the field.ErrorList aggregate with a fixed prefix,
// which is the only marker available to distinguish it from a storage error.
func asCloudValidationError(err error) error {
	if err == nil || !strings.Contains(err.Error(), "create validation failed") {
		return nil
	}
	return coreapi.NewCloudError(
		http.StatusBadRequest,
		coreapi.CloudErrorCodeInvalidRequestContent, "",
		"The namespace deletion request was rejected: %v", err,
	)
}

func writeJSON(w http.ResponseWriter, statusCode int, body any) error {
	_, err := coreapihelpers.WriteJSONResponse(w, statusCode, body)
	return utils.TrackError(err)
}
